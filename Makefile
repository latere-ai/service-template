# The template's own build. It verifies three things that have to stay in step:
# the generator, the skeleton it ships, and the reference service the two
# produce together.
#
# The skeleton is a separate Go module, so every skeleton target runs inside it
# rather than from here. That is what lets the shipped code be compiled and
# tested at source instead of only after generation.

SHELL := /bin/sh

MODULE := latere.ai/x/service-template
SKELETON := skeleton
EXAMPLE := example

# The example's declaration. It is stated here because `make example` has to
# reproduce it exactly for the diff to mean anything.
EXAMPLE_MODULE := github.com/example/reference-service
EXAMPLE_NAME := reference-service
EXAMPLE_PROFILE := service
EXAMPLE_FEATURES := frontend,seo,i18n,database,background
EXAMPLE_VERSION := v0.1.0

# One lint configuration for both modules. The skeleton ships it to consumers,
# and the template holding a second copy is how the two drift apart.
LINT_CONFIG := $(CURDIR)/$(SKELETON)/.golangci.yml

# Files gofmt owns. The generated reference service is excluded: it is output,
# and formatting it here would make it differ from what the generator writes.
# The frontend dependency tree is excluded because the Go files a JavaScript
# package carries are not this repository's to fix.
GO_FILES = $(shell find . -type f -name '*.go' \
	-not -path './$(EXAMPLE)/*' -not -path '*/node_modules/*' -not -path './*/testdata/*')

.PHONY: all build test lint fmt fmt-check skeleton-test cover example example-update clean

# A bare make runs every gate that needs no network and no container engine.
all: fmt-check build test skeleton-test example lint

build:
	go build ./...

test:
	go test -race ./...

# The skeleton is compiled and tested as source, before generation, so a defect
# in shipped code fails here rather than in the first repository that scaffolds.
#
# The race detector is on, because the runtime, the request chain, and the job
# runner are all concurrent and a data race in shipped code reaches every
# consumer at once.
skeleton-test:
	cd $(SKELETON) && go build ./... && go test -race ./...

# The coverage gate runs the unit tier and the integration tier and holds the
# combined figure to the threshold. It is not part of `all`, because the
# integration tier requires the dependencies it exists to exercise: a reachable
# database, and a browser for the diagram renderer. The pipeline runs it where
# those are provided.
cover:
	cd $(SKELETON) && $(MAKE) cover

lint:
	$(call require_tool,golangci-lint,go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest)
	golangci-lint run --config $(LINT_CONFIG) ./...
	cd $(SKELETON) && golangci-lint run ./...

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

# The reference service is a generated artifact that is committed, so the
# committed tree and a fresh generation must be identical. A difference means
# either the skeleton moved without the example being refreshed, or generation
# stopped being deterministic; both are defects and both read from this diff.
example:
	@rm -rf $(EXAMPLE).check
	@$(MAKE) --no-print-directory generate-example DIR=$(EXAMPLE).check
	@if diff -ru $(EXAMPLE) $(EXAMPLE).check >/dev/null 2>&1; then \
		rm -rf $(EXAMPLE).check; \
		echo "example: matches a fresh generation"; \
	else \
		echo "the committed reference service differs from a fresh generation:"; \
		diff -ru $(EXAMPLE) $(EXAMPLE).check | head -80; \
		rm -rf $(EXAMPLE).check; \
		echo ""; \
		echo "run: make example-update"; \
		exit 1; \
	fi

example-update:
	@rm -rf $(EXAMPLE)
	@$(MAKE) --no-print-directory generate-example DIR=$(EXAMPLE)
	@echo "example: regenerated"

# generate-example scaffolds the reference service into DIR. It is one recipe
# so the check and the refresh cannot pass different arguments.
generate-example:
	@go run ./cmd/template init \
		-C $(DIR) \
		-skeleton $(SKELETON) \
		-module $(EXAMPLE_MODULE) \
		-name $(EXAMPLE_NAME) \
		-profile $(EXAMPLE_PROFILE) \
		-features $(EXAMPLE_FEATURES) \
		-version $(EXAMPLE_VERSION) >/dev/null

clean:
	rm -rf $(EXAMPLE).check

# A gate that cannot run is a failed gate, never a pass. Every target that
# shells out to a tool asserts the tool is present and names the install
# command, so a missing tool fails loudly instead of reporting nothing.
define require_tool
@command -v $(1) >/dev/null 2>&1 || { \
	echo "$(1) is not installed."; \
	echo "install it with: $(2)"; \
	exit 1; \
}
endef
