# Repository settings as code: the local checks, the dry run, and the apply.
#
# The verify pipeline can only report; what makes its gate binding is
# repository configuration. These targets are the path from the declaration in
# .github/settings.yml to the live repository, and settings-verify is the part
# that needs no credentials, so it runs in every verify run.

PHONY_TARGETS += settings-verify settings-plan settings-apply settings-report \
                 settings-required-check
ALL_TARGETS += settings-verify

SETTINGS_CMD := ./tools/settings
SETTINGS_FILE := .github/settings.yml
CODEOWNERS_FILE := .github/CODEOWNERS
# The repository defaults to the one the pipeline runs in. A workstation run
# passes REPO=owner/name.
REPO ?= $(GITHUB_REPOSITORY)

# Ownership coverage and the declaration itself. No network, no token.
settings-verify:
	@go run $(SETTINGS_CMD) -mode=verify \
		-file=$(SETTINGS_FILE) -codeowners=$(CODEOWNERS_FILE)

# The difference between the declaration and the live repository. Writes
# nothing, so it is the safe way to see what an apply would do.
settings-plan:
	@go run $(SETTINGS_CMD) -mode=plan -file=$(SETTINGS_FILE) -repo=$(REPO)

# Idempotent. It reads first and writes only the settings that differ, so a
# repeat run issues no request.
settings-apply:
	@go run $(SETTINGS_CMD) -mode=apply -file=$(SETTINGS_FILE) -repo=$(REPO)

# The scheduled drift report. It states what differs and reverts nothing,
# because a setting changed deliberately during an incident must not be undone
# without notice.
settings-report:
	@go run $(SETTINGS_CMD) -mode=report -file=$(SETTINGS_FILE) -repo=$(REPO)

# Fails when the gate is not a required status check on the default branch.
# Until it passes, every gate in the pipeline is advisory.
settings-required-check:
	@go run $(SETTINGS_CMD) -mode=required-check -file=$(SETTINGS_FILE) -repo=$(REPO)
