#!/usr/bin/env sh
# Cross-implementation format check: NEXUS vs the independent recovery
# implementation (kipdb-recovery). See crosscheck/README.md.
#
#   sh crosscheck/run.sh            (from the repository root; Git Bash on Windows)
#
# Clones the independent implementation at a pinned commit into
# crosscheck/.recovery (git-ignored), builds kvdb from this repository and
# the crosscheck program, and runs every check in a fresh scratch directory.
set -eu
REPO=https://github.com/sharanyashetty013-droid/kipdb-recovery.git
COMMIT=f0d8b9c   # the commit audited on 2026-11-06
ROOT=$(cd "$(dirname "$0")/.." && pwd)
CC="$ROOT/crosscheck"

if [ ! -d "$CC/.recovery/.git" ]; then
  git clone -q "$REPO" "$CC/.recovery"
fi
git -C "$CC/.recovery" -c advice.detachedHead=false checkout -q "$COMMIT"
echo "independent implementation: $REPO @ $(git -C "$CC/.recovery" log -1 --format='%h %s')"

OUT=$(mktemp -d 2>/dev/null || mktemp -d -t crosscheck)
EXT=""
case "$(go env GOOS)" in windows) EXT=".exe" ;; esac
(cd "$ROOT" && go build -o "$OUT/kvdb$EXT" ./src/cmd/kvdb)
(cd "$CC" && go build -o "$OUT/crosscheck$EXT" .)
"$OUT/crosscheck$EXT" "$OUT/kvdb$EXT" "$OUT/work"
