#!/usr/bin/env bash
# Final validation (Prompt 12). Sequential, so timings do not interfere.
#   bash docs/results/final/run-final.sh
# Logs land next to this script. Work directories go to torture-runs/final
# (git-ignored) and the OS temp directory. Nothing is committed.
set -u
ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
R="$ROOT/docs/results/final"
W="$ROOT/torture-runs/final"
cd "$ROOT"
export PATH="/c/Program Files/Go/bin:$PATH"
EXE=""; [ "$(go env GOOS)" = windows ] && EXE=".exe"
LOG="$R/00-steps.log"
: > "$LOG"
step() { echo "== [$(date '+%Y-%m-%d %H:%M:%S')] $*" | tee -a "$LOG"; }
result() { echo "   -> exit $1 ($2)" | tee -a "$LOG"; }

step "environment"
{ go version; uname -a; git log -1 --oneline; echo "git status (uncommitted work is included in this run):"; git status --short; } > "$R/01-environment.log" 2>&1

step "clean build from scratch (go build -a into an empty directory)"
rm -rf "$W" && mkdir -p "$W/bin"
go build -a -o "$W/bin/" ./src/cmd/kvdb ./src/cmd/kvdb-bench ./torture2/cmd/nexus-torture ./torture2/mutants/kvdb-mutant > "$R/02-clean-build.log" 2>&1
rc=$?; ls -l "$W/bin" >> "$R/02-clean-build.log"; result $rc "02-clean-build.log"
B="$W/bin"

step "gofmt, go vet (windows linux darwin freebsd), go build ./..."
{
  echo "gofmt -l (excluding the teammate copy crosscheck/.recovery):"
  gofmt -l src torture torture2 crosscheck/main.go; echo "gofmt done"
  go vet ./... && echo "vet ok windows"
  for os in linux darwin freebsd; do GOOS=$os go vet ./... && echo "vet ok GOOS=$os"; done
  go build ./... && echo "build ok"
  GOOS=linux GOARCH=arm64 go build ./... && echo "build ok linux/arm64"
} > "$R/03-static.log" 2>&1; result $? "03-static.log"

step "unit and integration tests: go test -count=1 ./..."
go test -count=1 -timeout 60m ./... > "$R/04-go-test.log" 2>&1; result $? "04-go-test.log"

step "cross-implementation verification (crosscheck)"
sh crosscheck/run.sh > "$R/05-crosscheck.log" 2>&1; result $? "05-crosscheck.log"

step "targeted: compaction, disk full / size cap, read-only check, Windows moves"
go test -count=1 -v ./src/internal/storage ./src/internal/recovery ./src/internal/cli ./src/internal/platform ./src/kvdb \
  -run 'Compact|DiskFull|MaxBytes|Full|Inspect|CheckNoRepair|Move|KillDuringCompacting' > "$R/06-targeted.log" 2>&1
result $? "06-targeted.log"

step "1,000,000-key recovery timing (KVDB_PERF)"
KVDB_PERF=1 go test -count=1 -v ./src/internal/recovery -run TestOpenMillionKeysTiming -timeout 30m > "$R/07-recovery-1M.log" 2>&1
result $? "07-recovery-1M.log"

step "32 official scenarios, Go fault-injection suite, full mode"
KVDB_SCENARIOS=full KVDB_SCENARIO_REPORT=docs/results/final/SCENARIO_RESULTS_gotest.md \
  go test ./src/scenarios -run TestGroup -v -count=1 -timeout 120m > "$R/08-scenarios-gotest-full.log" 2>&1
result $? "08-scenarios-gotest-full.log"

step "Torture Engine 2.0: randomized runs"
T="$B/nexus-torture$EXE"; K="$B/kvdb$EXE"
mkdir -p "$R/torture"
trun() {
  local name=$1; shift
  "$T" run "$@" --kvdb "$K" --dir "$W/$name" --quiet > "$R/torture/$name.log" 2>&1
  local rc=$?; cp "$W/$name/summary.json" "$R/torture/$name.summary.json" 2>/dev/null
  result $rc "torture/$name.log: $(grep -E '^RESULT' "$R/torture/$name.log" | cut -c1-90)"
}
trun default-500-seed12345 --mode default --seed 12345 --iterations 500
for m in batch10 large-values large-batch concurrent-disjoint same-key readers compaction recovery-crash disk-full close-reopen mixed-long; do
  trun "$m-100-seed2026" --mode "$m" --seed 2026 --iterations 100
done

step "Torture Engine 2.0: mutation testing (first detection, then detection rate)"
"$T" mutants --mutant "$B/kvdb-mutant$EXE" --dir "$W/mutants" --iterations 40 --seed 777 > "$R/torture/mutants.log" 2>&1
rc=$?; cp "$W/mutants/mutation-report.json" "$R/torture/" 2>/dev/null; result $rc "torture/mutants.log"
{
  for m in ack-before-sync drop-acked partial-batch wrong-unknown bad-recovery corrupt-value; do
    mode=default; [ $m = partial-batch ] && mode=batch10
    KVDB_MUTANT=$m "$T" run --mode $mode --seed 4040 --iterations 20 --keep-going --kvdb "$B/kvdb-mutant$EXE" --dir "$W/rate-$m" --quiet 2>&1 \
      | grep -E '^(RESULT|VERIFY)' | sed "s/^/$m: /"
  done
} > "$R/torture/mutation-detection-rate.log" 2>&1; result $? "torture/mutation-detection-rate.log"

step "Torture Engine 2.0: reproducibility"
{
  "$T" run --mode default --seed 31337 --iterations 20 --kvdb "$K" --dir "$W/repro-a" --quiet | grep -E '^RESULT'
  "$T" run --mode default --seed 31337 --iterations 20 --kvdb "$K" --dir "$W/repro-b" --quiet | grep -E '^RESULT'
  echo "same seed twice, request streams compared:"
  python -I docs/results/final/compare_streams.py "$W/repro-a/events.jsonl" "$W/repro-b/events.jsonl" || echo "(python missing or streams differ)"
  echo "replay of a recorded failure (mutant corrupt-value, failure kept at iteration 5 of a --keep-going run):"
  KVDB_MUTANT=corrupt-value "$T" run --mode default --seed 4040 --iterations 5 --keep-going --kvdb "$B/kvdb-mutant$EXE" --dir "$W/replay-src" --quiet > /dev/null 2>&1
  echo "original:"; head -1 "$W/replay-src/failures/iter-5/failure.txt"; grep -a '"ev":"start","iter":5,' "$W/replay-src/events.jsonl"
  KVDB_MUTANT=corrupt-value "$T" run --mode default --seed 4040 --iteration 5 --from-db "$W/replay-src/failures/iter-5/pre/db.kv" \
    --kvdb "$B/kvdb-mutant$EXE" --dir "$W/replay" --quiet 2>&1 | grep -E '^(FAIL at|RESULT|[a-z-]+ on key)'
  grep -a '"ev":"start"' "$W/replay/events.jsonl"
} > "$R/torture/reproducibility.log" 2>&1; result $? "torture/reproducibility.log"

step "Torture Engine 2.0: 32 official scenarios against the real executable, full sizes"
"$T" scenarios --kvdb "$K" --dir "$W/scenarios-full" --full > "$R/torture/scenarios-full.log" 2>&1
rc=$?; cp "$W/scenarios-full/scenarios.json" "$W/scenarios-full/SCENARIOS.md" "$R/torture/" 2>/dev/null
result $rc "torture/scenarios-full.log: $(grep -E '^SCENARIOS RESULT' "$R/torture/scenarios-full.log")"

step "benchmark: kvdb-bench, 100,000 keys, 3 runs"
"$B/kvdb-bench$EXE" --n 100000 --value-size 100 --batch-size 1000 --runs 3 --seed 1 \
  --json "$R/bench.json" --md "$R/BENCHMARK.md" --note "NVMe SSD, Windows Defender real-time protection on" > "$R/09-bench.log" 2>&1
result $? "09-bench.log"

step "acceptance: 1M PUT, recovery, 1M DELETE, compaction, writes after compaction"
KVDB_ACCEPTANCE=1 KVDB_ACCEPTANCE_REPORT=docs/results/final/acceptance-1M-put-delete.txt \
  go test ./src/scenarios -run TestAcceptanceMillionPutDelete -v -count=1 -timeout 90m > "$R/10-acceptance-1M.log" 2>&1
result $? "10-acceptance-1M.log"

step "done"
