#!/bin/sh
# The single required check. It reads the result of every upstream job and
# fails unless all of them succeeded.
#
# A failed job is an obvious failure. A cancelled job and a skipped job are the
# ones that matter here: a skipped required check is reported as neutral by the
# branch protection rule, so a path filter, a matrix condition, or a missing
# secret can remove a gate from a run and the pull request still shows green.
# Treating anything other than success as a failure closes that path.
#
# Usage: gate.sh [needs.json]
# Environment: NEEDS_JSON holds the same document when no file is given.

set -eu

if [ "$#" -gt 0 ]; then
	document=$(cat "$1")
else
	document="${NEEDS_JSON:-}"
fi

if [ -z "$document" ]; then
	echo "gate: no upstream results were passed in" >&2
	echo "the gate job must receive toJSON(needs); an empty document means the" >&2
	echo "job list is wrong and no gate ran." >&2
	exit 1
fi

results=$(printf '%s' "$document" | jq -r '
	to_entries
	| sort_by(.key)
	| .[]
	| "\(.key)\t\(.value.result // "missing")"
')

if [ -z "$results" ]; then
	echo "gate: the upstream result set is empty" >&2
	echo "no job reported to the gate, so nothing was verified." >&2
	exit 1
fi

failed=""
printf '%s\n' "$results" | while IFS='	' read -r job result; do
	printf '%-24s %s\n' "$job" "$result"
done

summary() {
	[ -n "${GITHUB_STEP_SUMMARY:-}" ] || return 0
	{
		echo "### Gate"
		echo
		echo "| Job | Result |"
		echo "| --- | --- |"
		printf '%s\n' "$results" | while IFS='	' read -r job result; do
			echo "| \`$job\` | $result |"
		done
		echo
	} >>"$GITHUB_STEP_SUMMARY"
}

failed=$(printf '%s\n' "$results" | awk -F'\t' '$2 != "success" { printf "%s(%s) ", $1, $2 }')

summary

if [ -n "$failed" ]; then
	echo >&2
	echo "gate: these jobs did not succeed: $failed" >&2
	echo "a cancelled or skipped job fails the gate on purpose. A required check" >&2
	echo "that did not run has verified nothing, so it cannot report a pass." >&2
	if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
		echo "Gate failed: $failed" >>"$GITHUB_STEP_SUMMARY"
	fi
	exit 1
fi

echo "gate: every upstream job succeeded"
