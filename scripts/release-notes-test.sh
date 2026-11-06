#!/usr/bin/env bash
# Tests for scripts/release-notes.sh. These stay dependency-free so CI can run
# them on a bare runner.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
script="$root/scripts/release-notes.sh"
tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT

passed=0
failed=0

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    failed=$((failed + 1))
}

run_ok() {
    local name=$1 expected=$2
    shift 2
    local status=0
    "$@" >"$tmp/out" 2>"$tmp/err" || status=$?
    if [ "$status" -ne 0 ]; then
        fail "$name: exit $status, want 0"
        sed 's/^/  /' "$tmp/err" >&2
        return
    fi
    if diff -u "$expected" "$tmp/out" >&2; then
        passed=$((passed + 1))
    else
        fail "$name: output mismatch"
    fi
}

run_fail() {
    local name=$1 want_status=$2 want_message=$3
    shift 3
    local status=0
    "$@" >"$tmp/out" 2>"$tmp/err" || status=$?
    if [ "$status" -ne "$want_status" ]; then
        fail "$name: exit $status, want $want_status"
        sed 's/^/  /' "$tmp/err" >&2
        return
    fi
    if ! grep -Fq "$want_message" "$tmp/err"; then
        fail "$name: stderr missing: $want_message"
        sed 's/^/  /' "$tmp/err" >&2
        return
    fi
    passed=$((passed + 1))
}

if bash -n "$script"; then
    passed=$((passed + 1))
else
    fail "syntax check"
fi

changelog="$tmp/CHANGELOG.md"
cat >"$changelog" <<'EOF'
# Changelog

## [Unreleased]

- Pending work.

## [0.2.0] - 2026-11-01

### Added

- A new thing.

### Fixed

- A bug.

## [0.1.0] - 2026-10-30

- Initial release.

[Unreleased]: https://example.test/compare/v0.1.0...HEAD
[0.2.0]: https://example.test/releases/tag/v0.2.0
[0.1.0]: https://example.test/releases/tag/v0.1.0
EOF

cat >"$tmp/expected-0.2.0" <<'EOF'
### Added

- A new thing.

### Fixed

- A bug.
EOF

cat >"$tmp/expected-0.1.0" <<'EOF'
- Initial release.
EOF

run_ok "middle section, exact output" "$tmp/expected-0.2.0" \
    "$script" v0.2.0 "$changelog"
run_ok "last section stops before link refs" "$tmp/expected-0.1.0" \
    "$script" v0.1.0 "$changelog"

cat >"$tmp/empty.md" <<'EOF'
# Changelog

## [0.2.0] - 2026-11-01

## [0.1.0] - 2026-10-30

- Initial release.
EOF

run_fail "missing version is rejected" 1 "no section for 9.9.9" \
    "$script" v9.9.9 "$changelog"
run_fail "empty section is rejected" 1 "section for 0.2.0 is empty" \
    "$script" v0.2.0 "$tmp/empty.md"
run_fail "missing changelog is rejected" 1 "changelog not found" \
    "$script" v0.2.0 "$tmp/missing.md"
run_fail "tag without v is rejected" 1 "expected a tag beginning with v" \
    "$script" 0.2.0 "$changelog"
run_fail "bare v tag is rejected" 1 "expected a tag beginning with v" \
    "$script" v "$changelog"
run_fail "empty tag is rejected" 1 "expected a tag beginning with v" \
    "$script" "" "$changelog"
run_fail "missing argument prints usage" 2 "usage: release-notes.sh" \
    "$script"

spaced="$tmp/change log.md"
printf '# Changelog\n\n## [0.1.0] - 2026-10-30\n\n- Initial release.\n' >"$spaced"
run_ok "path containing spaces" "$tmp/expected-0.1.0" \
    "$script" v0.1.0 "$spaced"

eof="$tmp/eof.md"
printf '# Changelog\n\n## [0.2.0] - 2026-11-01\n\n### Added\n\n- Last line.' >"$eof"
printf '### Added\n\n- Last line.\n' >"$tmp/expected-eof"
run_ok "section at EOF without final newline" "$tmp/expected-eof" \
    "$script" v0.2.0 "$eof"

if [ "$failed" -ne 0 ]; then
    printf '\n%d passed, %d failed\n' "$passed" "$failed" >&2
    exit 1
fi

printf 'release-notes tests: %d passed\n' "$passed"
