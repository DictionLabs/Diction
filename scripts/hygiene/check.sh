#!/usr/bin/env bash
# check.sh <module-dir>: the gateway quality gate. Runs golangci-lint, the comment, function-length,
# file-length and private-reference checkers, govulncheck and the coverage gate (min 90).
# Exits 1 on any finding. Needs golangci-lint and govulncheck on PATH (or GOLANGCI_LINT / GOVULNCHECK).
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
dir="$(cd "${1:?usage: check.sh <module-dir>}" && pwd)"
LINT="${GOLANGCI_LINT:-golangci-lint}"
GVC="${GOVULNCHECK:-govulncheck}"
fail=0
step() { local name="$1"; echo "== $name"; shift; "$@" || { echo "FAILED: $name"; fail=1; }; }
step golangci-lint bash -c "cd '$dir' && '$LINT' run ./..."
for c in comments funclen filelen privrefs; do
  step "$c" bash -c "cd '$here' && go run $c.go '$dir'"
done
# govulncheck must analyse with the same Go toolchain the module is built with.
groot="$(go env GOROOT)"
step govulncheck bash -c "cd '$dir' && PATH='$groot/bin':\$PATH GOTOOLCHAIN=local GOROOT='$groot' '$GVC' ./..."
step "coverage (min 90)" "$here/coverage.sh" "$dir" --min 90
[ $fail = 0 ] && echo "gateway gate: pass"
exit $fail
