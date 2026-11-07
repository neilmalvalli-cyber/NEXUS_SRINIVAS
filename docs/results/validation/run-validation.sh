#!/usr/bin/env bash
# Full validation run (Prompt 10). Sequential, so timings do not interfere.
# Run from anywhere with Git Bash:  bash docs/results/validation/run-validation.sh
# Every log lands next to this script; nothing outside the repository's
# torture-runs/ (git-ignored) and the OS temp directory is written.
set -u
ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
R="$ROOT/docs/results/validation"
cd "$ROOT"
export PATH="/c/Program Files/Go/bin:$PATH"
step() { echo; echo "== $* ($(date '+%Y-%m-%d %H:%M:%S'))"; }

{
step "environment"
go version; uname -a; git log -1 --oneline; git status --short | head -50

step "gofmt -l src torture crosscheck/main.go"
gofmt -l src torture crosscheck/main.go; echo "gofmt exit $? (no file names above = clean)"
step "go vet ./..."
go vet ./... && echo "vet ok"
for os in linux darwin freebsd; do GOOS=$os go vet ./... && echo "vet ok GOOS=$os"; done
step "go build ./..."
go build ./... && echo "build ok"
GOOS=linux GOARCH=arm64 go build ./... && echo "build ok linux/arm64"
step "go test -count=1 ./..."
go test -count=1 ./... ; echo "go test exit $?"
} > "$R/01-static-and-unit.log" 2>&1
tail -25 "$R/01-static-and-unit.log"

step "crosscheck (independent recovery implementation)"
sh crosscheck/run.sh > "$R/02-crosscheck.log" 2>&1; echo "crosscheck exit $?"; tail -1 "$R/02-crosscheck.log"

step "targeted tests: windows moves, read-only check, deterministic compaction"
go test -count=1 -v ./src/internal/platform ./src/internal/recovery ./src/internal/cli ./src/internal/storage \
  -run 'Move|Inspect|CheckNoRepair|CompactDeterministic|CompactSnapshotSorted|CompactCrash|CompactFault|TestCheck' \
  > "$R/03-targeted-tests.log" 2>&1; echo "targeted exit $?"; grep -E '^(ok|FAIL)' "$R/03-targeted-tests.log"

step "32 official scenarios, full mode"
KVDB_SCENARIOS=full KVDB_SCENARIO_REPORT=docs/results/validation/SCENARIO_RESULTS.md \
  go test ./src/scenarios -run 'TestGroup' -v -count=1 -timeout 120m > "$R/04-scenarios-full.log" 2>&1
echo "scenarios exit $?"; grep -E '^(--- |ok|FAIL)' "$R/04-scenarios-full.log" | tail -10

step "1M-key recovery benchmark (KVDB_PERF)"
KVDB_PERF=1 go test ./src/internal/recovery -run 'TestOpenMillionKeysTiming' -v -count=1 -timeout 30m > "$R/05-perf-1M-recovery.log" 2>&1
echo "perf exit $?"; grep -E 'ms|^(ok|FAIL|---)' "$R/05-perf-1M-recovery.log" | tail -6

go build -o "$ROOT" ./src/cmd/kvdb ./src/cmd/torture || exit 1  # moved from ./torture on 2026-11-07
run() {
  name=$1; shift
  step "torture $*"
  ./torture.exe "$@" --dir "torture-runs/validation-$name" > "$R/torture-$name.log" 2>&1
  echo "exit $?"; tail -1 "$R/torture-$name.log"
}
run default-500-seed12345      --iterations 500 --seed 12345
run large-values-100-seed999   --iterations 100 --seed 999 --mode large-values
run large-batch-100-seed13     --iterations 100 --seed 13 --mode large-batch
run compaction-100-seed7       --iterations 100 --seed 7 --mode compaction
run recovery-crash-100-seed42  --iterations 100 --seed 42 --mode recovery-crash
run disk-full-100-seed11       --iterations 100 --seed 11 --mode disk-full
run concurrent-disjoint-100-seed21 --iterations 100 --seed 21 --mode concurrent-disjoint
run same-key-100-seed22        --iterations 100 --seed 22 --mode same-key

step "acceptance: 1M PUT, recovery, 1M DELETE, compaction, writes after"
KVDB_ACCEPTANCE=1 KVDB_ACCEPTANCE_REPORT=docs/results/validation/acceptance-1M-put-delete.txt \
  go test ./src/scenarios -run TestAcceptanceMillionPutDelete -v -count=1 -timeout 60m > "$R/06-acceptance-1M.log" 2>&1
echo "acceptance exit $?"; cat "$R/acceptance-1M-put-delete.txt"

step "done"
