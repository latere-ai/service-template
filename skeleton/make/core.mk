# Core Go targets: build, formatting, lint, static analysis, tests, coverage,
# and the template drift check.
#
# The gates themselves are not written here. They live in latere.ai/x/ci-gate,
# pinned in go.mod as a tool dependency and configured in .lateregate.yaml, so
# a gate runs the same on a workstation as on a runner and every service gets
# a fix to one by bumping a version. What stays here is the wiring: which
# gates this repository runs, and which of them a bare `make` performs.

PHONY_TARGETS += build fmt fmt-check hooks lint lint-config lint-fix \
                 lint-modernize vet vuln test test-integration cover \
                 test-hermetic test-tempdir lint-otel template-check
ALL_TARGETS += fmt-check lint-modernize vet lint test lint-otel

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

# The file list comes from git rather than from walking the tree, so a nested
# checkout parked inside the repository cannot contribute a file this
# repository neither owns nor can fix.
fmt-check:
	@go tool lateregate fmt-check

# Code a standard library call already covers. The disabled fixers are named
# in .lateregate.yaml, and each one is verified to still exist before the flag
# is trusted: `go fix` rejects an unknown -name=false, so a fixer dropped by
# the toolchain would otherwise turn the whole check green over nothing.
lint-modernize:
	@go tool lateregate modernize

hooks:
	chmod +x .githooks/*
	git config core.hooksPath .githooks
	@echo "git hooks installed from .githooks"

# .golangci.yml is generated and gitignored. golangci-lint cannot inherit a
# shared configuration, so the file is rendered from the org's template on
# every run. Regenerating rather than committing is what makes drift
# impossible instead of merely detectable.
lint-config:
	@go tool lateregate golangci

lint: lint-config
	$(call require_tool,golangci-lint,go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest)
	golangci-lint run ./...

lint-fix: lint-config
	$(call require_tool,golangci-lint,go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest)
	golangci-lint run --fix ./...

# An outbound HTTP client built without a tracing transport calls out on the
# stdlib one: no client span is recorded, no traceparent is sent, and the
# service on the other end opens a fresh trace. Nothing fails and nothing is
# logged; the gap only shows up later as a trace that stops at a boundary.
lint-otel:
	@go tool lateregate otel-client

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
# The two profiles merge as a union of statement blocks rather than a sum: with
# -coverpkg the same block appears in both tiers, so a block either tier
# covered is covered. The gate judges every package against the floor in
# .lateregate.yaml, and reports a package that produced no data at all rather
# than letting it clear the floor by being absent.
cover:
	@mkdir -p $(OUT_DIR)
	go test -race -covermode=atomic -coverpkg=./... \
		-coverprofile=$(COVER_UNIT) -count=1 ./...
	go test -race -covermode=atomic -coverpkg=./... \
		-coverprofile=$(COVER_INTEGRATION) -count=1 -tags=$(INTEGRATION_TAGS) ./...
	@go tool lateregate cover -profile=$(COVER_UNIT) -profile=$(COVER_INTEGRATION)

# The suite with only the Go toolchain on PATH. A test that depends on what
# happens to be installed passes on a workstation and fails on a runner.
test-hermetic:
	@go tool lateregate hermetic

# The suite against an empty TMPDIR, failing on whatever is left behind. A
# test that makes a temporary directory and does not remove it leaks it for
# the life of the machine, and nothing else in the suite goes red for it.
test-tempdir:
	@go tool lateregate tempdir

template-check:
	$(call require_tool,template,go install github.com/latere-ai/service-template/cmd/template@v1)
	template check
