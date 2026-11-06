#!/usr/bin/env bash
# Targeted final validation (Prompt 14 follow-up). Runs after run-final.sh has
# finished step 08 (Go scenarios, full) and the Torture Engine 2.0 default run
# (500 iterations, seed 12345). It then stops run-final.sh before its remaining
# steps, which repeat evidence already recorded for the same engine code
# (docs/results/validation/, engine source unchanged since 19:06:59), and runs:
#   mutation testing + detection rate, reproducibility, 32 scenarios (quick),
#   kvdb-bench (100k keys, 3 runs).
# Stops at the first failing step so it can be investigated.
set -u
ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
R="$ROOT/docs/results/final"
W="$ROOT/torture-runs/final"
B="$W/bin"
cd "$ROOT"
EXE=".exe"
T="$B/nexus-torture$EXE"; K="$B/kvdb$EXE"; M="$B/kvdb-mutant$EXE"
LOG="$R/00-steps.log"
step() { echo "== [$(date '+%Y-%m-%d %H:%M:%S')] $*" | tee -a "$LOG"; }
result() { echo "   -> exit $1 ($2)" | tee -a "$LOG"; }
stop_if() { if [ "$1" != 0 ]; then step "STOPPED: $2 failed (exit $1); remaining targeted steps not run"; exit 1; fi; }

# 1. wait for run-final.sh to record the default-500 result
until grep -q 'default-500-seed12345.log' "$LOG" 2>/dev/null; do sleep 5; done

# 2. stop run-final.sh (script shells only), then the run it has just started
powershell -NoProfile -Command "Get-CimInstance Win32_Process | Where-Object { \$_.Name -eq 'bash.exe' -and \$_.CommandLine -like '*run-final.sh*' } | ForEach-Object { Stop-Process -Id \$_.ProcessId -Force }"
sleep 1
powershell -NoProfile -Command "Get-CimInstance Win32_Process | Where-Object { \$_.Name -eq 'nexus-torture.exe' -and \$_.CommandLine -like '*torture-runs*final*-100-seed2026*' } | ForEach-Object { taskkill /T /F /PID \$_.ProcessId }"
step "run-final.sh stopped after the default-500 run (remaining steps covered by docs/results/validation and validation-v2; see README)"

S08=$(grep -A1 'Go fault-injection suite, full mode' "$LOG" | grep -o 'exit [0-9]*' | head -1 | cut -d' ' -f2)
D500=$(grep 'default-500-seed12345.log' "$LOG" | grep -o 'exit [0-9]*' | head -1 | cut -d' ' -f2)
stop_if "${S08:-x}" "step 08 (Go scenarios, full)"
stop_if "${D500:-x}" "Torture Engine 2.0 default 500"

step "targeted: mutation testing (first detection, 40-iteration budget, seed 777)"
"$T" mutants --mutant "$M" --dir "$W/mutants" --iterations 40 --seed 777 > "$R/torture/mutants.log" 2>&1
rc=$?; cp "$W/mutants/mutation-report.json" "$R/torture/" 2>/dev/null; result $rc "torture/mutants.log"; stop_if $rc mutants

step "targeted: mutation detection rate (20 iterations each, --keep-going, seed 4040)"
{
  for m in ack-before-sync drop-acked partial-batch wrong-unknown bad-recovery corrupt-value; do
    mode=default; [ $m = partial-batch ] && mode=batch10
    KVDB_MUTANT=$m "$T" run --mode $mode --seed 4040 --iterations 20 --keep-going --kvdb "$M" --dir "$W/rate-$m" --quiet 2>&1 \
      | grep -E '^(RESULT|VERIFY)' | sed "s/^/$m: /"
  done
} > "$R/torture/mutation-detection-rate.log" 2>&1; result $? "torture/mutation-detection-rate.log"

step "targeted: reproducibility (seed 31337 twice, streams compared; recorded failure replayed)"
{
  "$T" run --mode default --seed 31337 --iterations 20 --kvdb "$K" --dir "$W/repro-a" --quiet | grep -E '^RESULT'; echo "run a exit ${PIPESTATUS[0]}"
  "$T" run --mode default --seed 31337 --iterations 20 --kvdb "$K" --dir "$W/repro-b" --quiet | grep -E '^RESULT'; echo "run b exit ${PIPESTATUS[0]}"
  echo "same seed twice, request streams compared:"
  python -I docs/results/final/compare_streams.py "$W/repro-a/events.jsonl" "$W/repro-b/events.jsonl"; echo "compare exit $?"
  echo "replay of a recorded failure (mutant corrupt-value, failure kept at iteration 5 of a --keep-going run):"
  KVDB_MUTANT=corrupt-value "$T" run --mode default --seed 4040 --iterations 5 --keep-going --kvdb "$M" --dir "$W/replay-src" --quiet > /dev/null 2>&1
  echo "original:"; head -1 "$W/replay-src/failures/iter-5/failure.txt"; grep -a '"ev":"start","iter":5,' "$W/replay-src/events.jsonl"
  KVDB_MUTANT=corrupt-value "$T" run --mode default --seed 4040 --iteration 5 --from-db "$W/replay-src/failures/iter-5/pre/db.kv" \
    --kvdb "$M" --dir "$W/replay" --quiet 2>&1 | grep -E '^(FAIL at|RESULT|[a-z-]+ on key)'
  grep -a '"ev":"start"' "$W/replay/events.jsonl"
} > "$R/torture/reproducibility.log" 2>&1; result $? "torture/reproducibility.log"

step "targeted: 32 official scenarios against the real executable, quick sizes"
"$T" scenarios --kvdb "$K" --dir "$W/scenarios-quick" > "$R/torture/scenarios-quick.log" 2>&1
rc=$?; cp "$W/scenarios-quick/scenarios.json" "$R/torture/scenarios-quick.json" 2>/dev/null
cp "$W/scenarios-quick/SCENARIOS.md" "$R/torture/SCENARIOS-quick.md" 2>/dev/null
result $rc "torture/scenarios-quick.log: $(grep -E '^SCENARIOS RESULT' "$R/torture/scenarios-quick.log")"; stop_if $rc "scenarios (quick)"

step "targeted: benchmark: kvdb-bench, 100,000 keys, 3 runs"
"$B/kvdb-bench$EXE" --n 100000 --value-size 100 --batch-size 1000 --runs 3 --seed 1 \
  --json "$R/bench.json" --md "$R/BENCHMARK.md" --note "NVMe SSD, Windows Defender real-time protection on" > "$R/09-bench.log" 2>&1
rc=$?; result $rc "09-bench.log"; stop_if $rc kvdb-bench

step "targeted: done"
