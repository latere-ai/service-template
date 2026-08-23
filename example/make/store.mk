# Database targets: apply the migrations, and check them without a database.
#
# migrate is a deployment step, not part of a build. It runs before the new
# version starts, because two replicas starting at once would race and a
# start-up migration ties the schema change to rollout timing.
#
# migrate-check needs no database and runs as part of a bare `make`. It reads
# the migration files, rejects a set that cannot be read exactly, and rejects a
# migration that removes a column or a table the previous release still reads.
# That rule only fails in production otherwise, during the window where the old
# and the new code share one schema.

PHONY_TARGETS += migrate migrate-check
ALL_TARGETS += migrate-check

MIGRATIONS_DIR := migrations
MIGRATE_CMD := ./internal/store/cmd/migrate

# DATABASE_URL is read from the environment, so the connection string is never
# written into a target.
migrate:
	@go run $(MIGRATE_CMD) -dir $(MIGRATIONS_DIR)

migrate-check:
	@go run $(MIGRATE_CMD) -dir $(MIGRATIONS_DIR) -check
