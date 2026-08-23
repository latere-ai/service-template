#!/bin/sh
# Renders the lint report as findings per linter.
#
# A pass mark tells a reviewer nothing about what the run inspected. The count
# per linter states which rule sets produced findings and which produced none,
# and the report section proves the linters ran at all rather than the run
# ending early on a configuration error.
#
# Usage: lint-summary.sh --file REPORT

set -eu

file=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--file) file="$2"; shift 2 ;;
	*) echo "lint-summary: unknown argument $1" >&2; exit 2 ;;
	esac
done

[ -n "$file" ] || { echo "lint-summary: --file is required" >&2; exit 2; }

fail_scan() {
	echo "golangci-lint: scan did not run" >&2
	echo "$1" >&2
	exit 1
}

[ -f "$file" ] || fail_scan "no report at $file."
[ -s "$file" ] || fail_scan "the report at $file is empty."

if ! jq -e 'has("Report")' "$file" >/dev/null 2>&1; then
	fail_scan "the report at $file names no linter, so the run did not reach the analysis."
fi

enabled=$(jq -r '[.Report.Linters[]? | select(.Enabled == true) | .Name] | length' "$file")
total=$(jq -r '[.Issues[]?] | length' "$file")

echo "golangci-lint: $enabled linters enabled, $total findings"

rows=$(jq -r '
	[.Issues[]? | .FromLinter]
	| group_by(.)
	| map({linter: .[0], count: length})
	| sort_by(-.count, .linter)
	| .[]
	| [.linter, (.count | tostring)]
	| @tsv
' "$file")

if [ -n "$rows" ]; then
	printf '%-24s %s\n' LINTER FINDINGS
	printf '%s\n' "$rows" | while IFS='	' read -r linter count; do
		printf '%-24s %s\n' "$linter" "$count"
	done
fi

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
	{
		echo "### Lint"
		echo
		echo "$enabled linters enabled, $total findings."
		echo
		if [ -n "$rows" ]; then
			echo "| Linter | Findings |"
			echo "| --- | --- |"
			printf '%s\n' "$rows" | while IFS='	' read -r linter count; do
				echo "| \`$linter\` | $count |"
			done
			echo
			echo "<details><summary>First findings</summary>"
			echo
			echo '```'
			jq -r '.Issues[]? | "\(.Pos.Filename):\(.Pos.Line): \(.FromLinter): \(.Text)"' "$file" | head -50
			echo '```'
			echo
			echo "</details>"
		fi
		echo
	} >>"$GITHUB_STEP_SUMMARY"
fi

if [ "$total" -gt 0 ]; then
	echo "golangci-lint: $total findings, listed above" >&2
	exit 1
fi
