#!/bin/sh
# Reads the consumer declaration and refuses to run a workflow that is newer
# than the generated files it depends on.
#
# Workflows ride a moving major tag while generated files are pinned exactly,
# so a repository can call a workflow that expects a file it does not have yet.
# The comparison here is the guard the contract requires: the workflow declares
# the minimum .template.yaml version it needs, this script reads the version on
# disk, and a repository below the minimum fails with the upgrade instruction
# instead of failing later inside a job that cannot find a file.
#
# Usage: template-version.sh [declaration]
# Environment: MIN_TEMPLATE_VERSION is the minimum this workflow accepts.
# Output: key=value lines on stdout, appended to $GITHUB_OUTPUT when set.

set -eu

decl="${1:-.template.yaml}"
min="${MIN_TEMPLATE_VERSION:-}"

if [ -z "$min" ]; then
	echo "template-version: MIN_TEMPLATE_VERSION is not set" >&2
	exit 2
fi

if [ ! -f "$decl" ]; then
	echo "template-version: $decl not found" >&2
	echo "every repository that calls this workflow holds a template declaration at its root." >&2
	echo "run: template init" >&2
	exit 1
fi

# scalar reads a top level field. Quotes and trailing comments are stripped so
# the value is the same whichever spelling the declaration uses.
scalar() {
	awk -v key="$1" '
		$0 ~ "^" key ":" {
			sub("^" key ":[ \t]*", "")
			sub("[ \t]*#.*$", "")
			gsub(/^["\x27]|["\x27]$/, "")
			sub(/[ \t]+$/, "")
			print
			exit
		}
	' "$decl"
}

# flag reads one entry of the features block. An absent flag is off.
flag() {
	awk -v key="$1" '
		/^features:/ { inside = 1; next }
		/^[^ \t#]/ { inside = 0 }
		inside && $0 ~ "^[ \t]+" key ":" {
			sub("^[ \t]+" key ":[ \t]*", "")
			sub("[ \t]*#.*$", "")
			sub(/[ \t]+$/, "")
			print
			exit
		}
	' "$decl"
}

version=$(scalar version)
name=$(scalar name)
profile=$(scalar profile)

if [ -z "$version" ]; then
	echo "template-version: $decl declares no version" >&2
	exit 1
fi

# part extracts one numeric component of a version, defaulting to zero.
part() {
	printf '%s' "$1" | sed 's/^v//; s/-.*$//' | cut -d. -f"$2" | sed 's/[^0-9]//g'
}

# below reports whether $1 precedes $2. A pre-release precedes the release of
# the same number, because it may not carry the file the minimum names.
below() {
	i=1
	while [ "$i" -le 3 ]; do
		a=$(part "$1" "$i")
		b=$(part "$2" "$i")
		[ -n "$a" ] || a=0
		[ -n "$b" ] || b=0
		if [ "$a" -lt "$b" ]; then return 0; fi
		if [ "$a" -gt "$b" ]; then return 1; fi
		i=$((i + 1))
	done
	case "$1" in
	*-*)
		case "$2" in
		*-*) return 1 ;;
		*) return 0 ;;
		esac
		;;
	esac
	return 1
}

if below "$version" "$min"; then
	echo "template-version: this repository follows template $version" >&2
	echo "this workflow needs $min or newer, because it reads generated files added in $min." >&2
	echo "run: template upgrade --to $min && make template-check" >&2
	exit 1
fi

emit() {
	printf '%s=%s\n' "$1" "$2"
	if [ -n "${GITHUB_OUTPUT:-}" ]; then
		printf '%s=%s\n' "$1" "$2" >>"$GITHUB_OUTPUT"
	fi
}

emit template-version "$version"
emit name "$name"
emit profile "${profile:-service}"
for feature in frontend seo i18n database background; do
	value=$(flag "$feature")
	[ "$value" = "true" ] || value=false
	emit "feature-$feature" "$value"
done
