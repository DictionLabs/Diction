#!/usr/bin/env bash
# coverage.sh <module-dir> [--min N]: statement coverage of the SHIPPED packages.
# Shipped = packages the gateway binary links: `go list -deps .` (the module's root main package), filtered to this module, minus
# internal/evalcases. Tests of the whole module run with -race; cross-package coverage counts.
set -uo pipefail
dir="" min=""
while [ $# -gt 0 ]; do
  case "$1" in
    --min) min="$2"; shift 2 ;;
    *) dir="$1"; shift ;;
  esac
done
[ -n "$dir" ] || { echo "usage: coverage.sh <module-dir> [--min N]" >&2; exit 2; }
cd "$dir" || exit 2
mod="$(go list -m)"
entry=.
shipped="$(go list -deps "$entry" | grep -E "^${mod}(/|$)" | grep -v "^${mod}/internal/evalcases" | sort -u)"
[ -n "$shipped" ] || { echo "no shipped packages found" >&2; exit 2; }
pkgs="$(echo "$shipped" | paste -sd, -)"
prof="$(mktemp)"; log="$(mktemp)"
trap 'rm -f "$prof" "$log"' EXIT
go test -count=1 -race -covermode=atomic -coverpkg="$pkgs" -coverprofile="$prof" ./... >"$log" 2>&1
rc=$?
# A failing test makes the profile partial, so any percentage would be understated: fail loudly, print no total.
[ $rc -eq 0 ] || { echo "COVERAGE RUN FAILED: go test exited $rc (failing tests or build errors); no coverage number reported" >&2; grep -E '^(FAIL|---|panic|WARNING: DATA RACE)' "$log" | head -15 >&2; exit 1; }
[ -z "${COVERPROFILE:-}" ] || cp "$prof" "$COVERPROFILE"
python3 -I - "$prof" "$mod" "$min" <<'PY'
import sys, collections
prof, mod, mn = sys.argv[1], sys.argv[2], sys.argv[3]
blocks = {}
for line in open(prof):
    if line.startswith("mode:"): continue
    key, n, c = line.rsplit(" ", 2)
    n, c = int(n), int(c)
    old = blocks.get(key, (n, 0))
    blocks[key] = (n, max(old[1], c))
tot = collections.defaultdict(lambda: [0, 0])
for key, (n, c) in blocks.items():
    f = key.rsplit(":", 1)[0]
    pkg = f.rsplit("/", 1)[0]
    tot[pkg][0] += n
    if c > 0: tot[pkg][1] += n
T = C = 0
for pkg in sorted(tot):
    n, c = tot[pkg]; T += n; C += c
    print("%6.1f%%  %5d stmts  %s" % (100.0 * c / n if n else 0, n, pkg.replace(mod, ".", 1)))
pct = 100.0 * C / T if T else 0.0
print("SHIPPED TOTAL: %.1f%% (%d/%d statements)" % (pct, C, T))
if mn and pct < float(mn):
    sys.stderr.write("coverage %.1f%% is below the minimum %s%%\n" % (pct, mn)); sys.exit(1)
PY
