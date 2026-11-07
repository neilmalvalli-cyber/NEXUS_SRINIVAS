# Final readiness report (2026-11-07)

## Decision: **READY WITH DOCUMENTED LIMITATIONS**, provided the working tree is committed and pushed

The engine passed every test run. The remaining risks are process items (commit, prompt log, event window) and timing, not correctness.

## Acceptance gates

| Gate | Status | Evidence |
|---|---|---|
| Clean clone build | PASS (working tree) / **FAIL on current GitHub HEAD** until committed | CLEAN_CLONE_VERIFICATION.md |
| Offline build | PASS (empty caches, `GOPROXY=off`, 6 s) | same |
| Torture 200 | PASS: 4 seeds × 200, 0 failures | CRASH_SCENARIO_RESULTS.md |
| Arbitrary seed | PASS (12345, 1, 987654321, −42, 777; non-integers refused) | same; `seed_test.go` |
| Compaction mode | PASS 200/200 × 2 seeds (about 160 s each) | same |
| Recovery-crash mode | PASS 200/200 (734 s; 341 kills landed during recovery) | same; L9 timing |
| Manual kill cycles | PASS: 12 kills, 19,492 ACKs, 0 lost | MANUAL_KILL_REHEARSAL.md |
| Check after recovery | PASS (every cycle) | same |
| Dump after recovery | PASS (every ACK verified in dump) | same |
| 1M recovery < 2 s | PASS (0.55–0.80 s, warm cache; cold NOT TESTED) | ONE_MILLION_KEY_RECOVERY.md |
| File shrinks | PASS (103 MB → 10.3 MB) | COMPACTION_SHRINK_TEST.md |
| Disk-full safety | PASS (cap and injected ENOSPC); physical full disk NOT TESTED | DISK_FULL_VALIDATION.md |
| PROMPTS.md complete | **INCONCLUSIVE**: Prompts 1–21 logged verbatim; design-phase prompts missing (L13) | PROMPTS.md |
| Git history compliant | **INCONCLUSIVE**: 5 commits on 2026-11-06 13:12 → 2026-11-07 03:06 (+0530), all yours; the event window is not known to me; large uncommitted work | below |
| API uncertain outcomes | PASS (outcome unknown ≠ failed; 13/13 tests, O8 kill tests) | FRONTEND_FAILURE_TESTS.md |
| Offline dashboard | PASS (adapter off, N7); needs the local `node_modules` | NETWORK_FAILURE_TESTS.md |
| Three crash-here answers | PASS (prepared, with real line numbers) | CRASH_HERE_ANSWERS.md |
| Expiry design | PASS (design only, not implemented) | EXPIRY_EXTENSION_DESIGN.md |

Regression on 2026-11-07:
* `go vet ./...` clean;
* gofmt clean (tracked code);
* `go test ./...`: 21 packages ok, 0 failed;
* the 32 scenarios: 32/32;
* frontend `tsc` and lint clean;
* write-outcome tests 13/13.

## Defects found and fixed in this pass

1. **`./torture` did not exist in a clean clone.** It was a folder, and the build line didn't build it. The tool moved to `src/cmd/torture/`, and the README and docs were updated.
2. **`--seed 12abc` ran seed 12.** Fixed with a strict parse, exit 2, and `TestSeedMustBeExactInteger`.
3. **My own test bug.** The first version of that test passed the test binary as `--kvdb`. Run against the unfixed parser, it relaunched itself recursively in the background, from 09:37 until it was found and stopped at about 10:25. It was cleaned up (52,785 temp folders, about 52 MB). The test now passes a non-executable file, so it cannot recur. No repository files were affected.

## Git history (E)

* There is no `main` branch; `phase1-format` is the default branch on GitHub.
* The commits are large: 6k–34k lines each.
* The design-phase prompts predate the repository.

Whether this is within the event window only you or the organisers can say. Nothing was rewritten or committed by the agent.

**You must:**
1. Run `git add -A`, which records the `torture/` → `src/cmd/torture/` move.
2. Check that `demo-data/` should really be committed. It is untracked and holds a database file.
3. Commit and push.
4. Do the clone check in CLEAN_CLONE_VERIFICATION.md.

## Remaining blockers (yours)

1. Commit and push. Without it, the judge's first command fails.
2. Paste the design-phase prompts into PROMPTS.md if you can.
3. Confirm the event window against the commit times.
4. Run one PowerShell check: `./torture --seed abc`.
