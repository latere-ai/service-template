# Configuration targets: the example environment file is derived from the
# configuration struct, so the two cannot drift.
#
# env-example rewrites the committed copy. env-example-check proves the copy in
# the working tree matches the struct and runs as part of a bare `make`, which
# is how a new field reaches the file before review rather than after a failed
# deployment.

PHONY_TARGETS += env-example env-example-check
ALL_TARGETS += env-example-check

ENV_EXAMPLE := .env.example
ENV_EXAMPLE_CMD := ./internal/config/cmd/envexample

env-example:
	@go run $(ENV_EXAMPLE_CMD) -out $(ENV_EXAMPLE)

env-example-check:
	@go run $(ENV_EXAMPLE_CMD) -out $(ENV_EXAMPLE) -check
