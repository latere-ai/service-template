# The build entry point. It wires the fragments together and defines no target
# of its own, so a group of targets can be added or changed without touching
# this file.
#
# Repository-specific targets belong outside the managed region below, or in a
# fragment of their own under make/.

# >>> template: managed region, do not edit <<<
SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c

# Fragments append to these two variables. PHONY_TARGETS collects every target
# that names an action rather than a file; ALL_TARGETS collects the work that a
# bare `make` performs. Both are expanded when the rules below are read, which
# is why the include comes first.
PHONY_TARGETS :=
ALL_TARGETS :=

include make/*.mk

.DEFAULT_GOAL := all
.PHONY: all $(PHONY_TARGETS)

all: $(ALL_TARGETS)
# >>> template: end managed region <<<
