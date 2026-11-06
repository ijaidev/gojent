#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -lt 1 ]; then
    printf 'usage: release-notes.sh <tag> [changelog]\n' >&2
    exit 2
fi

tag="$1"
changelog="${2:-CHANGELOG.md}"
version="${tag#v}"

if [ -z "$version" ] || [ "$version" = "$tag" ]; then
    printf 'release-notes: expected a tag beginning with v, got "%s"\n' "$tag" >&2
    exit 1
fi

if [ ! -f "$changelog" ]; then
    echo "release-notes: changelog not found: $changelog" >&2
    exit 1
fi

notes="$(
    awk -v version="$version" '
        index($0, "## [" version "]") == 1 { found = 1; next }
        found && (/^## \[/ || /^\[[^]]+\]:/) { exit }
        found { print }
        END { if (!found) exit 1 }
    ' "$changelog"
)" || {
    echo "release-notes: no section for $version in $changelog" >&2
    exit 1
}

notes="$(printf '%s\n' "$notes" | sed -e '/./,$!d' | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}')"

if [ -z "${notes//[[:space:]]/}" ]; then
    echo "release-notes: section for $version is empty; add notes before tagging" >&2
    exit 1
fi

printf '%s\n' "$notes"
