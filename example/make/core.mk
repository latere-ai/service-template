# Core Go targets: build, formatting, lint, static analysis, tests, coverage,
# and the template drift check.

PHONY_TARGETS += build fmt fmt-check hooks lint lint-fix vet vuln \
                 test test-integration cover template-check
ALL_TARGETS += fmt-check vet lint test

# The module path is read from go.mod rather than restated, so the link-time
# variable paths below stay correct whatever the repository is called.
MODULE := $(shell go list -m)
# The service name follows the single entry point under cmd/, so the binary
# name is never a second place the layout is declared.
CMD_DIR := $(firstword $(wildcard cmd/*))
SERVICE := $(notdir $(CMD_DIR))

OUT_DIR := out
COVER_UNIT := $(OUT_DIR)/cover-unit.out
COVER_INTEGRATION := $(OUT_DIR)/cover-integration.out
SKIP_REPORT := $(OUT_DIR)/test-skips.txt
INTEGRATION_TAGS ?= integration

# Build metadata. Each is deferred, so the git and date calls run only for a
# target that stamps a binary.
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
# A working tree with uncommitted changes produces a binary that cannot be
# reproduced from its commit alone, so the commit carries the fact.
DIRTY = $(shell test -n "$$(git status --porcelain 2>/dev/null)" && echo -dirty)
ASSET_HASH ?= unknown

VERSION_PKG = $(MODULE)/internal/version
LDFLAGS = -X $(VERSION_PKG).version=$(VERSION) \
          -X $(VERSION_PKG).commit=$(COMMIT)$(DIRTY) \
          -X $(VERSION_PKG).buildTime=$(BUILD_TIME) \
          -X $(VERSION_PKG).assetHash=$(ASSET_HASH)

# RELEASE=1 strips the symbol table and DWARF. A development build keeps them
# so a debugger and a profiler still work.
RELEASE ?= 0
ifeq ($(RELEASE),1)
LDFLAGS += -s -w
endif

# A gate that cannot run is a failed gate, never a pass. Every target that
# shells out to a tool asserts the tool is present and names the install
# command, so a missing scanner fails loudly instead of reporting nothing.
define require_tool
@command -v $(1) >/dev/null 2>&1 || { \
	echo "$(1) is not installed."; \
	echo "install it with: $(2)"; \
	exit 1; \
}
endef

# Files gofmt owns. Generated trees and vendored code are excluded because
# their formatting is not this repository's to fix.
GO_FILES = $(shell find . -type f -name '*.go' \
	-not -path './vendor/*' -not -path './out/*' -not -path './*/testdata/*')

build:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' \
		-o $(OUT_DIR)/$(SERVICE) ./cmd/$(SERVICE)
	@echo "built $(OUT_DIR)/$(SERVICE)"

fmt:
	gofmt -w -l $(GO_FILES)

fmt-check:
	@unformatted=$$(gofmt -l $(GO_FILES)); \
	if [ -n "$$unformatted" ]; then \
		echo "these files are not gofmt clean, run make fmt:"; \
		echo "$$unformatted" | sed 's/^/  /'; \
		exit 1; \
	fi
	@echo "gofmt: clean"
	go fix -diff ./...

hooks:
	chmod +x .githooks/*
	git config core.hooksPath .githooks
	@echo "git hooks installed from .githooks"

lint:
	$(call require_tool,golangci-lint,go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest)
	golangci-lint run ./...

lint-fix:
	$(call require_tool,golangci-lint,go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest)
	golangci-lint run --fix ./...

vet:
	go vet ./...

vuln:
	$(call require_tool,govulncheck,go install golang.org/x/vuln/cmd/govulncheck@latest)
	govulncheck ./...

test:
	go test -race -count=1 ./...

# The dependency mode is read from the environment. CI exports
# TEST_DEPENDENCY_MODE=required so a missing database fails the tier instead of
# skipping it; a local run leaves it unset and skips.
test-integration:
	@mkdir -p $(OUT_DIR)
	@rm -f $(SKIP_REPORT)
	TEST_SKIP_REPORT=$(abspath $(SKIP_REPORT)) \
		go test -race -count=1 -tags=$(INTEGRATION_TAGS) ./...
	@if [ "$${TEST_DEPENDENCY_MODE:-}" = "required" ] && [ -s $(SKIP_REPORT) ]; then \
		echo "a required tier skipped these dependencies:"; \
		sed 's/^/  /' $(SKIP_REPORT); \
		exit 1; \
	fi

# Coverage spans the unit and integration tiers, because a unit-only figure
# understates a service whose logic sits behind a database boundary.
# -coverpkg covers every package so one with no test of its own still appears
# in the denominator instead of vanishing from the report.
cover:
	@mkdir -p $(OUT_DIR)
	go test -race -covermode=atomic -coverpkg=./... \
		-coverprofile=$(COVER_UNIT) -count=1 ./...
	go test -race -covermode=atomic -coverpkg=./... \
		-coverprofile=$(COVER_INTEGRATION) -count=1 -tags=$(INTEGRATION_TAGS) ./...
	go run ./tools/coverage -profile=$(COVER_UNIT) -profile=$(COVER_INTEGRATION)

template-check:
	$(call require_tool,template,go install github.com/latere-ai/service-template/cmd/template@v1)
	template check
