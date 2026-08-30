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

# Both modules render their lint configuration from the shared template in
# latere.ai/x/ci-gate rather than committing one. golangci-lint cannot inherit
# a config, so the only way two modules cannot drift is for neither to hold a
# copy.

.PHONY: all build test lint lint-config lint-modernize fmt fmt-check \
        skeleton-test skeleton-lint spec-check cover example example-update clean

# A bare make runs every gate that needs no network and no container engine.
all: fmt-check lint-modernize build test skeleton-test spec-check example lint

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

# Each module renders its own .golangci.yml from the shared template. Both are
# gitignored, so the copy on disk is always what the template says.
lint-config:
	@go tool lateregate golangci
	@cd $(SKELETON) && go tool lateregate golangci

# Two modules means two runs, and golangci-lint holds a lock that outlives the
# process briefly. The runs are sequential, so the flag admits what is already
# true rather than asking for concurrency: without it the second run fails on
# the first one's lock.
lint: lint-config
	$(call require_tool,golangci-lint,go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest)
	golangci-lint run --allow-parallel-runners ./...
	cd $(SKELETON) && golangci-lint run --allow-parallel-runners ./...

lint-modernize:
	@go tool lateregate modernize
	@cd $(SKELETON) && go tool lateregate modernize

fmt:
	gofmt -w .

# The file list comes from git, so the committed reference service under
# example/ is checked as the tracked output it is, and a nested checkout
# parked inside the tree contributes nothing.
fmt-check:
	@go tool lateregate fmt-check
	@cd $(SKELETON) && go tool lateregate fmt-check

# Both spec trees, held to the conventions each module declares.
spec-check:
	@go tool lateregate spec-lint
	@cd $(SKELETON) && go tool lateregate spec-lint

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
