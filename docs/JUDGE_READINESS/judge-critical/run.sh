#!/usr/bin/env bash
# Runs the 8 judge-critical test cases (16 official scenarios) and records
# the real results. Every run gets its own folder under runs/; nothing is
# overwritten. All databases are disposable temp folders created by
# `go test` (t.TempDir) and deleted afterwards; no user data is touched.
#
#   bash docs/JUDGE_READINESS/judge-critical/run.sh            # quick sizes
#   KVDB_SCENARIOS=full bash docs/JUDGE_READINESS/judge-critical/run.sh
#
# Case -> scenarios: 1 torn writes A2 | 2 batch atomicity B2 B6 |
# 3 compaction crashes C3 C4 | 4 interrupted recovery C1 C2 F2 |
# 5 post-fsync/pre-ACK A4 | 6 concurrent writers D2 | 7 1M-key recovery F3 |
# 8 disk full E1 E2 E3 E4 E5
set -u
cd "$(dirname "$0")/../../.." || exit 2          # repository root
IDS='A2|A4|B2|B6|C1|C2|C3|C4|D2|E1|E2|E3|E4|E5|F2|F3'
RUN='TestGroup[A-F]$/^('"$IDS"')$'
STAMP=$(date +%Y%m%d-%H%M%S)
OUT="docs/JUDGE_READINESS/judge-critical/runs/$STAMP"
mkdir -p "$OUT" || exit 2
CMD="go test -count=1 -v -timeout 30m ./src/scenarios -run '$RUN'"
echo "running: $CMD"
start=$(date +%s)
KVDB_SCENARIO_REPORT="$OUT/results.md" KVDB_SCENARIO_JSON="$OUT/results.json" \
  go test -count=1 -v -timeout 30m ./src/scenarios -run "$RUN" > "$OUT/run.log" 2>&1
rc=$?
secs=$(( $(date +%s) - start ))
head=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)
dirty=$(git status --porcelain 2>/dev/null | wc -l | tr -d ' ')
cat > "$OUT/meta.json" <<META
{"stamp":"$STAMP","command":"$CMD","mode":"${KVDB_SCENARIOS:-quick}","exit_code":$rc,"elapsed_s":$secs,"git_head":"$head","uncommitted_files":$dirty}
META
grep -E '^(--- |    --- |ok|FAIL)' "$OUT/run.log"
if [ -f "$OUT/results.json" ]; then
  # The dashboard reads the latest run from here (a copy; the evidence stays in runs/).
  node -e '
    const fs=require("fs"); const [r,m,dst]=process.argv.slice(1);
    const j=JSON.parse(fs.readFileSync(r,"utf8")); j.run=JSON.parse(fs.readFileSync(m,"utf8"));
    j.run.evidence_dir=process.argv[4]; fs.writeFileSync(dst, JSON.stringify(j,null,2)+"\n");
  ' "$OUT/results.json" "$OUT/meta.json" frontend/src/data/judge-critical-results.json "$OUT"
  echo "dashboard data updated: frontend/src/data/judge-critical-results.json"
fi
echo "exit $rc after ${secs}s; evidence in $OUT"
exit $rc
