#!/bin/sh
# Proves a scanner actually analyzed the code.
#
# A scanner that cannot build the project reports zero findings and the job
# turns green, which is the quietest way a security gate disappears. Every scan
# therefore writes a result file, and this guard asserts the file exists, holds
# content, and names what was analyzed. An empty or missing result is a failed
# scan, never a clean one.
#
# Usage: scan-guard.sh --tool NAME --file PATH --evidence REGEX [--min-bytes N]

set -eu

tool=""
file=""
evidence=""
min_bytes=1

while [ "$#" -gt 0 ]; do
	case "$1" in
	--tool) tool="$2"; shift 2 ;;
	--file) file="$2"; shift 2 ;;
	--evidence) evidence="$2"; shift 2 ;;
	--min-bytes) min_bytes="$2"; shift 2 ;;
	*) echo "scan-guard: unknown argument $1" >&2; exit 2 ;;
	esac
done

if [ -z "$tool" ] || [ -z "$file" ] || [ -z "$evidence" ]; then
	echo "scan-guard: --tool, --file, and --evidence are all required" >&2
	exit 2
fi

fail() {
	echo "$tool: scan did not run" >&2
	echo "$1" >&2
	echo "a scan with no result is a failure, because an empty report and a clean" >&2
	echo "report look identical from the outside." >&2
	exit 1
}

[ -f "$file" ] || fail "no result file at $file."

size=$(wc -c <"$file" | tr -d ' ')
if [ "$size" -lt "$min_bytes" ]; then
	fail "the result file at $file holds $size bytes, below the $min_bytes byte minimum."
fi

if ! grep -Eq "$evidence" "$file"; then
	fail "the result file at $file names no analyzed package; it does not match /$evidence/."
fi

echo "$tool: result file $file holds $size bytes and names the analyzed packages"
