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

.PHONY: all build test test-race test-hermetic validate lint lint-config \
        lint-modernize lint-otel fmt fmt-check skeleton-test skeleton-lint \
        skeleton-cover spec-lint example example-update clean

# A bare make runs every gate that needs no network and no container engine.
all: fmt-check lint-modernize build test test-hermetic spec-lint validate lint

build:
	go build ./...

# go vet and then the suite, without the race detector. The shared pipeline
# runs this target on both runners in its matrix and runs test-race as a job of
# its own, so the fast signal and the expensive one are not the same wait. The
# target never depends on lint: a failing lint that hides a failing test costs
# a second push to learn the second fact.
test:
	go vet ./...
	go test ./...

# The generator is concurrent where it walks the skeleton, and the reference
# service it produces is exercised under -race by skeleton-test.
test-race:
	go test -race ./...

# Both modules with only the Go toolchain on PATH. A test that depends on what
# happens to be installed passes on a workstation and fails on a runner, which
# is the worst order to find out. .lateregate.yaml names the two directories
# each module admits and why.
test-hermetic:
	@go tool lateregate hermetic
	@cd $(SKELETON) && go tool lateregate hermetic

# The skeleton is compiled and tested as source, before generation, so a defect
# in shipped code fails here rather than in the first repository that scaffolds.
#
# The race detector is on, because the runtime, the request chain, and the job
# runner are all concurrent and a data race in shipped code reaches every
# consumer at once.
skeleton-test:
	cd $(SKELETON) && go build ./... && go test -race ./...

# The skeleton's coverage gate, reachable from here so a developer need not
# change directory. It is named for the module it measures rather than `cover`,
# because the shared pipeline probes for `cover` and would run this one with
# neither of the dependencies below present.
#
# The coverage gate runs the unit tier and the integration tier and holds the
# combined figure to the threshold. It is not part of `all`, because the
# integration tier requires the dependencies it exists to exercise: a reachable
# database, and a browser for the diagram renderer. The pipeline runs it where
# those are provided.
skeleton-cover:
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
# The version is pinned rather than `latest`, so a run cannot pick up a build
# published after this Makefile was reviewed, and `go run` fetches it, so the
# gate needs no separately installed binary to be a gate.
GOLANGCI_VERSION ?= v2.13.1
GOLANGCI = go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

lint: lint-config skeleton-lint
	$(GOLANGCI) run --allow-parallel-runners ./...

# The skeleton's half, as a target of its own because the shared pipeline lints
# only the module at the repository root. Without a name of its own the shipped
# code would be linted on a workstation and nowhere else, so validate runs it.
skeleton-lint: lint-config
	cd $(SKELETON) && $(GOLANGCI) run --allow-parallel-runners ./...

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
spec-lint:
	@go tool lateregate spec-lint
	@cd $(SKELETON) && go tool lateregate spec-lint

# An outbound client with no tracing transport loses the trace at the boundary,
# silently: no client span, no traceparent, nothing logged. This module builds
# no client today, which is exactly when the gate is cheapest to add.
lint-otel:
	@go tool lateregate otel-client
	@cd $(SKELETON) && go tool lateregate otel-client

# Everything about this repository that the shared Go pipeline cannot host: the
# shipped skeleton compiled and tested as source, linted as its own module, its
# outbound clients checked, and the committed reference service diffed against
# a fresh generation.
validate: skeleton-test skeleton-lint lint-otel example

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
