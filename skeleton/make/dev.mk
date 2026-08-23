# Development, documentation, and spec targets.
#
# The rule behind every target here is that a clean clone reaches a running
# stack with one command, and that the documents in the repository are checked
# against the code rather than trusted.
#
# Ports and the container project name are derived from the directory name, so
# two checkouts of different services run at the same time. A fixed port is a
# collision waiting for the second project.

PHONY_TARGETS += dev dev-up dev-wait dev-down dev-backend dev-frontend dev-seed \
                 dev-logs dev-ports dev-images \
                 docs docs-check spec-index spec-check
ALL_TARGETS += docs-check spec-check

# The container engine. Docker is the default; another engine that speaks the
# same command line is selected by setting the variable.
DEV_ENGINE ?= $(shell command -v docker >/dev/null 2>&1 && echo docker || echo podman)
COMPOSE_FILE ?= docker-compose.yml
COMPOSE = $(DEV_ENGINE) compose -f $(COMPOSE_FILE) -p $(DEV_PROJECT)

# The project namespaces the containers, the network, and the volumes.
DEV_PROJECT ?= $(notdir $(CURDIR))

# Host ports are derived from the project name, so two checkouts do not collide
# and the same checkout always gets the same ports. Set any of them to override.
DEV_PORT_BASE ?= $(shell printf '%s' '$(DEV_PROJECT)' | cksum | awk '{print 20000 + ($$1 % 300) * 10}')
DEV_HTTP_PORT ?= $(DEV_PORT_BASE)
DEV_FRONTEND_PORT ?= $(shell expr $(DEV_PORT_BASE) + 1)
DEV_DB_PORT ?= $(shell expr $(DEV_PORT_BASE) + 2)

# The frontend project, when the repository carries one.
DEV_FRONTEND_DIR ?= frontend

DEV_DB_USER ?= service
DEV_DB_PASSWORD ?= service
DEV_DB_NAME ?= service
DEV_DATABASE_URL ?= postgres://$(DEV_DB_USER):$(DEV_DB_PASSWORD)@127.0.0.1:$(DEV_DB_PORT)/$(DEV_DB_NAME)?sslmode=disable

# Seconds between change scans of the reload loop.
DEV_WATCH_INTERVAL ?= 1

# The rebuild the reload loop runs. It is held in a variable rather than
# spelled in the recipe so the watch loop is not a recursive line: a recursive
# line runs even under `make -n`, and a dry run that starts a watch loop never
# returns.
DEV_BUILD = $(MAKE) --no-print-directory build

# The dependency install of the frontend, held in a variable for the same
# reason: a recipe line that names MAKE is executed even by a dry run, and a
# dry run must not start a development server.
DEV_FRONTEND_INSTALL = $(MAKE) --no-print-directory frontend-install

# Every target that talks to the stack exports the same values the compose file
# reads, so the stack a target addresses is the stack the previous target
# started.
DEV_COMPOSE_ENV = DEV_PROJECT=$(DEV_PROJECT) DEV_DB_PORT=$(DEV_DB_PORT) \
                  DEV_DB_USER=$(DEV_DB_USER) DEV_DB_PASSWORD=$(DEV_DB_PASSWORD) \
                  DEV_DB_NAME=$(DEV_DB_NAME)

# The service reads the same configuration mechanism locally as in production.
# Only the values differ, and they come from .env, which is a copy of the
# generated example file.
#
# These are the defaults .env overrides: a contributor who sets a log level
# keeps it.
DEV_SERVICE_ENV = ENVIRONMENT=development; LOG_FORMAT=text; LOG_LEVEL=debug

# These two describe this checkout's stack rather than a preference, so they
# are applied after .env and win. The derived port is what lets two checkouts
# run at once, and the connection string addresses the stack these targets
# started.
DEV_STACK_ENV = ADDR=:$(DEV_HTTP_PORT); DATABASE_URL=$(DEV_DATABASE_URL)

# The development data set.
#
# Every value is a literal: fixed identifiers, fixed timestamps, no random
# source and no clock. Two contributors seeding the same stack hold the same
# bytes, so a screenshot and a manual check are comparable between them.
#
# The rows cover the states an interface has to render: nothing, a typical
# record, and a record at the boundary of every limit. Clearing the table
# before the inserts is what makes a second seed converge instead of
# accumulating.
define DEV_SEED_SQL
begin;

create schema if not exists dev_seed;

create table if not exists dev_seed.example (
  id          uuid primary key,
  name        text not null,
  state       text not null,
  note        text,
  created_at  timestamptz not null
);

truncate table dev_seed.example;

insert into dev_seed.example (id, name, state, note, created_at) values
  ('00000000-0000-4000-8000-000000000001', 'minimal', 'empty', null,
   '2026-01-01T00:00:00Z'),
  ('00000000-0000-4000-8000-000000000002', 'typical', 'active',
   'A record with every optional value filled in.',
   '2026-01-02T09:30:00Z'),
  ('00000000-0000-4000-8000-000000000003',
   'boundary-name-at-the-longest-value-the-interface-has-to-render-000',
   'active',
   'A record at the boundary of every limit, so a layout that breaks on a long value breaks here first.',
   '2026-01-03T23:59:59Z');

commit;
endef
export DEV_SEED_SQL

DOCGEN := ./tools/docgen
SPECCHECK := ./tools/speccheck
SPEC_DIR ?= specs
DOCS_DIR ?= docs

# One command from a clean clone to a serving stack: dependencies, migrations,
# seed data, the backend with live reload, and the frontend development server.
# Running it against a stack that is already up converges instead of failing.
dev: dev-up
	@if printf '%s\n' $(PHONY_TARGETS) | grep -qx migrate; then \
		DATABASE_URL=$(DEV_DATABASE_URL) $(MAKE) --no-print-directory migrate; \
	else \
		echo "dev: no migrate target in this repository, skipping migrations"; \
	fi
	@$(MAKE) --no-print-directory dev-seed
	@echo "dev: backend on http://localhost:$(DEV_HTTP_PORT), database on port $(DEV_DB_PORT)"
	@$(MAKE) --no-print-directory -j 2 dev-backend dev-frontend

# Start the dependency stack and wait until it answers. The wait is on a fact
# reported by the dependency, not on a duration, so a slow machine waits longer
# instead of failing, and an engine that does not implement a wait of its own
# still reaches a stack that answers.
dev-up:
	@$(DEV_COMPOSE_ENV) $(COMPOSE) up -d
	@$(MAKE) --no-print-directory dev-wait

dev-wait:
	@for i in $$(seq 1 60); do \
		if $(DEV_COMPOSE_ENV) $(COMPOSE) exec -T postgres \
			pg_isready -U $(DEV_DB_USER) -d $(DEV_DB_NAME) >/dev/null 2>&1; then \
			echo "dev: the dependency stack is ready"; \
			exit 0; \
		fi; \
		sleep 1; \
	done; \
	echo "dev: the dependency stack did not become ready"; \
	$(DEV_COMPOSE_ENV) $(COMPOSE) ps; \
	exit 1

# Stop everything and remove the volumes. Local state is disposable on purpose:
# a contributor debugging a stale volume is debugging a problem that is not in
# the code.
dev-down:
	@$(DEV_COMPOSE_ENV) $(COMPOSE) down --volumes --remove-orphans
	@echo "dev: containers and volumes removed"

# Apply the development data set. It is piped into the database rather than
# mounted, so the same script runs on a fresh stack and on a running one, and no
# container engine has to implement a file-mounting feature for `make dev` to
# reach seeded data.
dev-seed:
	@printf '%s\n' "$$DEV_SEED_SQL" | $(DEV_COMPOSE_ENV) $(COMPOSE) exec -T postgres \
		psql -v ON_ERROR_STOP=1 -q -U $(DEV_DB_USER) -d $(DEV_DB_NAME)
	@echo "dev: seed data applied"

dev-logs:
	@$(DEV_COMPOSE_ENV) $(COMPOSE) logs -f

# Rebuild and restart on a change, keeping the dependency stack. The rebuild
# runs the same build target the pipeline runs, so a change that builds here
# builds there.
dev-backend:
	@trap 'if [ -n "$$pid" ]; then kill $$pid 2>/dev/null || true; fi; exit 0' INT TERM; \
	pid=""; fingerprint=""; \
	while true; do \
		current=$$(find . -type f -name '*.go' -not -path './$(OUT_DIR)/*' \
			-not -path './vendor/*' -exec ls -ld {} + | cksum); \
		if [ "$$current" != "$$fingerprint" ]; then \
			fingerprint="$$current"; \
			if [ -n "$$pid" ]; then kill $$pid 2>/dev/null || true; wait $$pid 2>/dev/null || true; fi; \
			if $(DEV_BUILD) >/dev/null; then \
				echo "dev: starting $(SERVICE) on :$(DEV_HTTP_PORT)"; \
				( set -a; $(DEV_SERVICE_ENV); \
				  if [ -f .env ]; then . ./.env; fi; \
				  $(DEV_STACK_ENV); set +a; \
				  $(OUT_DIR)/$(SERVICE) ) & pid=$$!; \
			else \
				echo "dev: the build failed, waiting for the next change"; \
			fi; \
		fi; \
		sleep $(DEV_WATCH_INTERVAL); \
	done

# The frontend development server. It binds the derived port and carries the
# backend address, which is what the development proxy forwards interface calls
# to. A repository with no frontend says so and succeeds, because the target is
# part of `make dev`.
dev-frontend:
	@if [ ! -f $(DEV_FRONTEND_DIR)/package.json ]; then \
		echo "dev: no frontend in this repository, serving the backend only"; \
		exit 0; \
	fi; \
	if printf '%s\n' $(PHONY_TARGETS) | grep -qx frontend-install; then \
		$(DEV_FRONTEND_INSTALL); \
	fi; \
	cd $(DEV_FRONTEND_DIR) && \
	VITE_BACKEND_URL=http://localhost:$(DEV_HTTP_PORT) \
	bun run dev -- --port $(DEV_FRONTEND_PORT) --strictPort

# The derived ports and project name. It prints and changes nothing, so a test
# and a contributor can both ask what this checkout will bind.
dev-ports:
	@echo "DEV_PROJECT=$(DEV_PROJECT)"
	@echo "DEV_HTTP_PORT=$(DEV_HTTP_PORT)"
	@echo "DEV_FRONTEND_PORT=$(DEV_FRONTEND_PORT)"
	@echo "DEV_DB_PORT=$(DEV_DB_PORT)"

# Prove every dependency image is pinned by digest and that the pin still
# resolves. The second half reaches the registry, so it runs where the network
# is available rather than in every build.
dev-images:
	@go run $(DOCGEN) images -compose $(COMPOSE_FILE) -resolve

# Regenerate the documents that are derived from the code.
docs:
	@go run $(DOCGEN) generate -docs $(DOCS_DIR)

# Prove the committed documents match the code, that every internal link
# resolves, that every diagram renders, and that no published document cites a
# reference a reader outside the team cannot resolve.
docs-check:
	@go run $(DOCGEN) check -root . -docs $(DOCS_DIR) -compose $(COMPOSE_FILE)

spec-index:
	@go run $(SPECCHECK) -dir $(SPEC_DIR) -write-index

spec-check:
	@go run $(SPECCHECK) -dir $(SPEC_DIR)
