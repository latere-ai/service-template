#!/bin/sh
# Enforces the suppression rules for every scanner in the pipeline.
#
# A suppression is an entry in one tracked file naming what is silenced, the
# tool it silences, a reason, and an expiry date. Two rules make suppressions
# survivable: an entry past its expiry fails the build, so a silence is
# revisited rather than inherited, and an inline suppression comment in the
# source is accepted only when an entry covers it, so silences cannot
# accumulate where nobody reads them.
#
# An entry names either an advisory identifier, which an inline comment repeats
# as "suppression:<id>", or a source path, which covers the markers in that
# file. The first form is what a scanner report reads; the second is for a
# linter directive that has no identifier of its own.
#
# Usage: suppressions.sh [--file FILE] [--root DIR] [--ids TOOL]
# --ids prints the identifiers of unexpired entries for one tool and checks
# nothing else, which is how a scanner report asks what it may ignore.

set -eu

file=".github/suppressions.yml"
root="."
ids_for=""

while [ "$#" -gt 0 ]; do
	case "$1" in
	--file) file="$2"; shift 2 ;;
	--root) root="$2"; shift 2 ;;
	--ids) ids_for="$2"; shift 2 ;;
	*) echo "suppressions: unknown argument $1" >&2; exit 2 ;;
	esac
done

# Dates are compared as integers. String comparison in test is not portable,
# and a comparison that silently fails would let an expired entry pass, which
# is the one outcome this file exists to prevent.
today=$(date -u +%Y%m%d)

# entries prints one tab separated record per declared suppression: identifier,
# path, tool, expiry, reason, and the line the entry starts on. Keys may appear
# in any order. An absent value is written as a dash, because a tab is field
# whitespace to the shell that reads these records and an empty field would
# collapse into its neighbour.
entries() {
	[ -f "$file" ] || return 0
	awk '
		function nz(value) { return value == "" ? "-" : value }
		function flush() {
			if (line != 0) {
				printf "%s\t%s\t%s\t%s\t%s\t%d\n",
					nz(id), nz(path), nz(tool), nz(expires), nz(reason), line
			}
			id = ""; path = ""; tool = ""; expires = ""; reason = ""; line = 0
		}
		function value(text) {
			sub(/^[^:]*:[ \t]*/, "", text)
			sub(/[ \t]*#.*$/, "", text)
			gsub(/^["\x27]|["\x27]$/, "", text)
			sub(/[ \t]+$/, "", text)
			return text
		}
		/^[ \t]*#/ { next }
		/^[ \t]*$/ { next }
		/^[ \t]*-[ \t]/ {
			flush()
			line = NR
			sub(/^[ \t]*-[ \t]*/, "")
		}
		/^[ \t]*id:/ { id = value($0); next }
		/^[ \t]*path:/ { path = value($0); next }
		/^[ \t]*tool:/ { tool = value($0); next }
		/^[ \t]*expires:/ { expires = value($0); next }
		/^[ \t]*reason:/ { reason = value($0); next }
		END { flush() }
	' "$file"
}

# live prints the records that are complete and unexpired.
live() {
	entries | awk -F'\t' -v today="$today" '
		function numeric(date,   digits) {
			digits = date
			gsub(/-/, "", digits)
			return (digits ~ /^[0-9]{8}$/) ? digits + 0 : 0
		}
		$3 != "-" && $5 != "-" && ($1 != "-" || $2 != "-") && numeric($4) >= today
	'
}

if [ -n "$ids_for" ]; then
	live | awk -F'\t' -v tool="$ids_for" '$3 == tool && $1 != "-" { print $1 }'
	exit 0
fi

status=0
records=$(entries)

if [ -f "$file" ]; then
	echo "suppressions: reading $file"
else
	echo "suppressions: no $file, so nothing is suppressed"
fi

if [ -n "$records" ]; then
	printf '%-32s %-14s %-12s %s\n' SUBJECT TOOL EXPIRES STATE
	while IFS='	' read -r id path tool expires reason line; do
		[ -n "$line" ] || continue
		if [ "$id" = "-" ]; then id=""; fi
		if [ "$path" = "-" ]; then path=""; fi
		if [ "$tool" = "-" ]; then tool=""; fi
		if [ "$expires" = "-" ]; then expires=""; fi
		if [ "$reason" = "-" ]; then reason=""; fi

		subject="${id:-$path}"
		state=active
		if [ -z "$subject" ] || [ -z "$tool" ] || [ -z "$expires" ] || [ -z "$reason" ]; then
			state=incomplete
		elif ! printf '%s' "$expires" | grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'; then
			state=bad-expiry
		elif [ "$(printf '%s' "$expires" | tr -d -)" -lt "$today" ]; then
			state=expired
		fi
		printf '%-32s %-14s %-12s %s\n' "${subject:-?}" "${tool:-?}" "${expires:-?}" "$state"
		case "$state" in
		incomplete)
			echo "suppressions: $file:$line declares an entry without id or path, tool, reason, and expires" >&2
			status=1
			;;
		bad-expiry)
			echo "suppressions: $file:$line has expiry \"$expires\", which is not YYYY-MM-DD" >&2
			status=1
			;;
		expired)
			echo "suppressions: $file:$line suppresses $subject past its expiry $expires" >&2
			echo "  fix the finding, or renew the entry with a current reason" >&2
			status=1
			;;
		esac
	done <<RECORDS
$records
RECORDS
fi

liveIDs=$(live | awk -F'\t' '$1 != "-" { print $1 }' | tr '\n' ' ')
livePaths=$(live | awk -F'\t' '$2 != "-" { print $2 }' | tr '\n' ' ')

# An inline marker is accepted only when the tracked file covers it. Without
# this rule an inline comment silences a finding permanently and invisibly.
# The pipeline checkout is excluded: it holds the scripts, not the code under
# review, and this file names every marker it looks for.
markers=$(
	grep -REn '(nolint|lint:ignore|eslint-disable|codeql\[|#nosec)' "$root" \
		--exclude-dir=.git --exclude-dir=node_modules --exclude-dir=vendor \
		--exclude-dir=dist --exclude-dir=out --exclude-dir=coverage \
		--exclude-dir=.template-pipeline \
		--exclude="$(basename "$file")" 2>/dev/null || true
)

orphans=$(
	printf '%s\n' "$markers" | while IFS= read -r hit; do
		[ -n "$hit" ] || continue
		covered=no
		id=$(printf '%s' "$hit" | sed -n 's/.*suppression:\([A-Za-z0-9._-]*\).*/\1/p')
		if [ -n "$id" ] && printf ' %s ' "$liveIDs" | grep -qF " $id "; then
			covered=yes
		fi
		if [ "$covered" = no ]; then
			hitPath=${hit%%:*}
			for declared in $livePaths; do
				# The leading parenthesis keeps the pattern unambiguous
				# inside the command substitution this loop runs in.
				case "$hitPath" in
				(*"$declared") covered=yes ;;
				esac
			done
		fi
		if [ "$covered" = no ]; then
			echo "$hit"
			if [ -n "$id" ]; then
				echo "    names $id, which $file does not declare or has expired"
			else
				echo "    no live entry covers it; add one with an id or this path"
			fi
		fi
	done
)

if [ -n "$orphans" ]; then
	echo "suppressions: these inline suppressions have no live entry in $file:" >&2
	printf '%s\n' "$orphans" | sed 's/^/  /' >&2
	status=1
fi

if [ "$status" -ne 0 ]; then
	echo "suppressions: the suppression rules failed" >&2
	exit 1
fi

echo "suppressions: every entry is complete and unexpired"
