# Frontend targets: install, type check, lint, the compiled-twin guard, the
# message completeness gate, tests with coverage, the production build with its
# prerender step, and the development server.
#
# Every target runs the command declared in frontend/package.json, so a
# developer running bun directly and the pipeline running make execute the same
# thing.

PHONY_TARGETS += frontend-install frontend-typecheck frontend-lint \
                 frontend-lint-fix frontend-format frontend-format-check \
                 frontend-check-twins frontend-i18n-check frontend-test \
                 frontend-build frontend-embed frontend-release-check \
                 frontend-dev frontend-clean
ALL_TARGETS += frontend-typecheck frontend-lint frontend-i18n-check frontend-test

FRONTEND_DIR := frontend
# The frontend build output, and the directory the binary embeds it from.
FRONTEND_DIST := $(FRONTEND_DIR)/dist
FRONTEND_EMBED_DIR := internal/web/public

# The entry asset of the bundle the binary embeds. It overrides the placeholder
# core.mk sets, and it is deferred, so the command runs only for a target that
# stamps a binary. A tree that holds no built bundle yields the placeholder
# rather than an empty stamp, and the release guard below is what refuses to
# ship that.
ASSET_HASH = $(shell go run ./tools/assethash 2>/dev/null || echo unknown)
# Installing is expensive and the inputs are two files, so the stamp turns it
# into a target that runs when the manifest or the lockfile changes.
FRONTEND_STAMP := $(FRONTEND_DIR)/node_modules/.install-stamp

# The frozen lockfile is the whole point of the install rule: CI must resolve
# the dependency tree the developer resolved, not a newer one that happens to
# satisfy the same ranges.
$(FRONTEND_STAMP): $(FRONTEND_DIR)/package.json $(FRONTEND_DIR)/bun.lock
	$(call require_tool,bun,curl -fsSL https://bun.sh/install | bash)
	cd $(FRONTEND_DIR) && bun install --frozen-lockfile
	@mkdir -p $(dir $@)
	@touch $@

frontend-install: $(FRONTEND_STAMP)

frontend-typecheck: $(FRONTEND_STAMP)
	cd $(FRONTEND_DIR) && bun run typecheck

frontend-lint: $(FRONTEND_STAMP)
	cd $(FRONTEND_DIR) && bun run lint
	cd $(FRONTEND_DIR) && bun run format:check

frontend-lint-fix: $(FRONTEND_STAMP)
	cd $(FRONTEND_DIR) && bun run lint:fix

frontend-format: $(FRONTEND_STAMP)
	cd $(FRONTEND_DIR) && bun run format

frontend-format-check: $(FRONTEND_STAMP)
	cd $(FRONTEND_DIR) && bun run format:check

# A compiled .js beside its .ts source is preferred by the module resolver, so
# the runner executes a stale build and reports a pass for code that is not the
# code under review. The guard is a prerequisite of the test target rather than
# a separate pipeline step, so no parallel run can reorder them.
frontend-check-twins: $(FRONTEND_STAMP)
	cd $(FRONTEND_DIR) && bun run check:twins

frontend-i18n-check: $(FRONTEND_STAMP)
	cd $(FRONTEND_DIR) && bun run check:i18n

frontend-test: frontend-check-twins
	cd $(FRONTEND_DIR) && bun run test

# The build bundles the client and then prerenders every public route, so the
# output holds a complete document for each of them.
frontend-build: $(FRONTEND_STAMP)
	cd $(FRONTEND_DIR) && bun run build

# The binary serves the frontend from a fixed directory. Copying is a separate
# target because the Go build and the frontend build run at different times,
# and the copy is what ties one to the other.
frontend-embed: frontend-build
	rm -rf $(FRONTEND_EMBED_DIR)
	mkdir -p $(FRONTEND_EMBED_DIR)
	cp -R $(FRONTEND_DIST)/. $(FRONTEND_EMBED_DIR)/
	@echo "embedded $(FRONTEND_DIST) into $(FRONTEND_EMBED_DIR)"

# A release binary must carry a built bundle. The placeholder document keeps the
# embedded directory present in version control, and a binary that ships it
# serves an empty application while every gate reports a pass.
frontend-release-check:
	RELEASE=1 go test -count=1 -run TestAReleaseRefusesThePlaceholderBundle ./internal/web/

# A release build embeds the bundle first and then refuses to stamp a binary
# that holds the placeholder. A development build skips both, so an ordinary
# `make build` does not require bun.
ifeq ($(RELEASE),1)
build: frontend-embed frontend-release-check
endif

frontend-dev: $(FRONTEND_STAMP)
	cd $(FRONTEND_DIR) && bun run dev

frontend-clean:
	rm -rf $(FRONTEND_DIST) $(FRONTEND_DIR)/coverage
