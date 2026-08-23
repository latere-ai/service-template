#!/bin/sh
# Measures the built frontend bundle and states the change against a baseline.
#
# Bundle size regresses one dependency at a time and is noticed on a slow
# device months later. The number, and the delta against the default branch,
# belong in the run summary where a reviewer already is.
#
# Usage: bundle-size.sh --dist DIR --out FILE [--baseline FILE]
# The output file records the measurement so the next run can compare against
# it; the default branch uploads it as the baseline.

set -eu

dist=""
out=""
baseline=""

while [ "$#" -gt 0 ]; do
	case "$1" in
	--dist) dist="$2"; shift 2 ;;
	--out) out="$2"; shift 2 ;;
	--baseline) baseline="$2"; shift 2 ;;
	*) echo "bundle-size: unknown argument $1" >&2; exit 2 ;;
	esac
done

[ -n "$dist" ] || { echo "bundle-size: --dist is required" >&2; exit 2; }
[ -n "$out" ] || { echo "bundle-size: --out is required" >&2; exit 2; }

if [ ! -d "$dist" ]; then
	echo "bundle-size: no bundle at $dist" >&2
	echo "the build step produced no output, so there is nothing to measure." >&2
	exit 1
fi

# Sizes are read per file so the summary can name the entry that grew, and
# hashed file names are folded to their extension so a rebuild compares.
measure() {
	find "$dist" -type f | LC_ALL=C sort | while IFS= read -r path; do
		bytes=$(wc -c <"$path" | tr -d ' ')
		printf '%s\t%s\n' "${path#"$dist"/}" "$bytes"
	done
}

rows=$(measure)
if [ -z "$rows" ]; then
	echo "bundle-size: the bundle at $dist holds no files" >&2
	exit 1
fi

total=$(printf '%s\n' "$rows" | awk -F'\t' '{ sum += $2 } END { print sum + 0 }')

printf '%s\n' "$rows" | awk -F'\t' -v total="$total" '
	BEGIN { printf "%-48s %12s\n", "FILE", "BYTES" }
	{ printf "%-48s %12d\n", $1, $2 }
	END { printf "%-48s %12d\n", "total", total }
'

{
	printf '{\n  "total": %s,\n  "files": [\n' "$total"
	printf '%s\n' "$rows" | awk -F'\t' '
		NR > 1 { printf ",\n" }
		{ printf "    {\"path\": \"%s\", \"bytes\": %d}", $1, $2 }
		END { printf "\n" }
	'
	printf '  ]\n}\n'
} >"$out"

delta_line="no baseline, so this run becomes the baseline"
if [ -n "$baseline" ] && [ -f "$baseline" ]; then
	before=$(jq -r '.total // 0' "$baseline")
	delta=$((total - before))
	if [ "$before" -gt 0 ]; then
		percent=$(awk -v d="$delta" -v b="$before" 'BEGIN { printf "%+.2f", d * 100 / b }')
	else
		percent="+0.00"
	fi
	sign=""
	if [ "$delta" -ge 0 ]; then sign="+"; fi
	delta_line="$total bytes, $sign$delta bytes against the baseline $before ($percent%)"
fi

echo "bundle-size: $delta_line"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
	{
		echo "### Frontend bundle"
		echo
		echo "$delta_line"
		echo
		echo "| File | Bytes |"
		echo "| --- | ---: |"
		printf '%s\n' "$rows" | while IFS='	' read -r path bytes; do
			echo "| \`$path\` | $bytes |"
		done
		echo "| **total** | **$total** |"
		echo
	} >>"$GITHUB_STEP_SUMMARY"
fi
