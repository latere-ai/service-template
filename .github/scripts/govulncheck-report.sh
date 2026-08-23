#!/bin/sh
# Turns a govulncheck JSON report into a verdict and a summary table.
#
# govulncheck reports call graph reachability, and the two levels mean
# different things. An advisory on a symbol the binary reaches is a defect the
# build must not carry, so it fails. An advisory on a module that is imported
# but whose vulnerable symbol is never called is information: it belongs in the
# summary and in the dependency update queue, not in a red build that nobody
# can act on today.
#
# A finding is symbol level when its first trace frame names a function, which
# is how govulncheck records the call site it proved.
#
# Usage: govulncheck-report.sh --file REPORT [--suppressions FILE]

set -eu

file=""
suppressions=""
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)

while [ "$#" -gt 0 ]; do
	case "$1" in
	--file) file="$2"; shift 2 ;;
	--suppressions) suppressions="$2"; shift 2 ;;
	*) echo "govulncheck-report: unknown argument $1" >&2; exit 2 ;;
	esac
done

[ -n "$file" ] || { echo "govulncheck-report: --file is required" >&2; exit 2; }

fail_scan() {
	echo "govulncheck: scan did not run" >&2
	echo "$1" >&2
	echo "a scanner that cannot build the project reports no findings, which is" >&2
	echo "indistinguishable from a clean result unless the run is asserted." >&2
	exit 1
}

[ -f "$file" ] || fail_scan "no report at $file."
[ -s "$file" ] || fail_scan "the report at $file is empty."

if ! jq -s -e 'any(.[]; .config.scanner_name == "govulncheck")' "$file" >/dev/null 2>&1; then
	fail_scan "the report at $file carries no govulncheck configuration message."
fi

# The bill of materials names the modules the scan resolved, which is the
# evidence that it analyzed this repository rather than failing to build it.
# A scanner version that does not emit one still emits progress messages.
if ! jq -s -e 'any(.[]; (.SBOM.roots // []) | length > 0) or any(.[]; has("progress"))' \
	"$file" >/dev/null 2>&1; then
	fail_scan "the report at $file names no analyzed module."
fi

scanned=$(jq -s -r '
	first(.[] | select(has("SBOM")) | .SBOM
		| "\(.modules | length) modules analyzed, rooted at \(.roots | join(", "))")
	// first(.[] | select(has("progress")) | .progress.message)
	// "modules scanned"
' "$file")
echo "govulncheck: $scanned"

rows=$(jq -s -r '
	[.[] | select(has("finding")) | .finding]
	| group_by(.osv)
	| map({
		osv: .[0].osv,
		module: ([.[] | .trace[0].module // empty] | first // "unknown"),
		fixed: ([.[] | .fixed_version // empty] | first // "none"),
		symbol: ([.[]
			| select(.trace[0].function != null)
			| ((.trace[0].package // .trace[0].module) + "." + .trace[0].function)]
			| first // "-"),
		reached: any(.[]; .trace[0].function != null)
	})
	| sort_by(.osv)
	| .[]
	| [.osv, .module, .fixed, (if .reached then "reached" else "not reached" end), .symbol]
	| @tsv
' "$file")

ignored=""
if [ -n "$suppressions" ] && [ -f "$suppressions" ]; then
	ignored=$(sh "$script_dir/suppressions.sh" --file "$suppressions" --ids govulncheck | tr '\n' ' ')
fi

blocking=""
table=""
if [ -n "$rows" ]; then
	printf '%-18s %-40s %-12s %-12s %s\n' OSV MODULE FIXED STATE SYMBOL
	while IFS='	' read -r osv module fixed state symbol; do
		[ -n "$osv" ] || continue
		verdict="$state"
		if [ "$state" = "reached" ] && printf ' %s ' "$ignored" | grep -qF " $osv "; then
			verdict="reached, suppressed"
		elif [ "$state" = "reached" ]; then
			blocking="$blocking $osv"
		fi
		printf '%-18s %-40s %-12s %-12s %s\n' "$osv" "$module" "$fixed" "$verdict" "$symbol"
		table="$table| \`$osv\` | $module | $fixed | $verdict | \`$symbol\` |
"
	done <<ROWS
$rows
ROWS
else
	echo "govulncheck: no advisories affect this module"
fi

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
	{
		echo "### govulncheck"
		echo
		echo "$scanned"
		echo
		if [ -n "$table" ]; then
			echo "| Advisory | Module | Fixed in | State | Symbol |"
			echo "| --- | --- | --- | --- | --- |"
			printf '%s' "$table"
		else
			echo "No advisory affects this module."
		fi
		echo
	} >>"$GITHUB_STEP_SUMMARY"
fi

if [ -n "$blocking" ]; then
	echo >&2
	echo "govulncheck: these advisories affect symbols this build reaches:$blocking" >&2
	echo "update the dependency, or move the call off the vulnerable path." >&2
	exit 1
fi

echo "govulncheck: no reachable advisory"
