# Judge test results

Status: **complete for this audit (2026-11-07).** Machine-readable totals: [judge-results-summary.json](judge-results-summary.json).

## Summary

| Outcome | Count | Items |
|---|---|---|
| PASS, existing evidence | 35 | see JUDGE_ATTACK_MATRIX.md |
| PASS, fixed or newly tested in this audit | 12 | P7 (bug fixed), N2 and N3 (bug fixed), N4, O7, O8, S3, S4, N6, N7, N8, PC3 |
| FAIL | 0 | |
| Inconclusive | 0 | |
| Not implemented | 0 | |
| NOT TESTED | 3 | S2 (real disk full: needs admin for a VHD), C2 (race detector: needs a C toolchain), PC4 (physical power cut: needs hardware) |
| **Total scenario rows** | **50** | |

**Bugs found and fixed:**
1. **P7, engine.** A damaged mid-log frame header made a normal open truncate later committed frames. Rule C3 was amended to refuse instead; there is no format change.
2. **N2/N3, dashboard.** A write whose answer was lost (timeout, reset, cut-off or unreadable response) was shown as "failed". It is now "outcome unknown".

**Documentation corrected:**
* POWER_CUT_EXPLORER.md overstated replay ("same crash-point sample") for concurrent runs.
* DESIGN.md, RECOVERY.md, TEST_PLAN.md, JUDGE_CLARIFICATIONS.md, XRAY_DIAGNOSTICS.md and the comparison page described the old C3 behaviour.

**Final regression (2026-11-07, after all changes):**
* gofmt clean; `go vet ./...` clean; `go test ./src/... ./torture2/... ./torture/...` 21/21 packages ok.
* Frontend: `tsc --noEmit` and `eslint src scripts` clean; `test:write-outcome` 13/13; `test:api` (live adapter) 25/25.
* `git diff --check` clean.
* After the P7 fix: Power-Cut seeds 1–3, 3,937 images, 0 failures; torture seed 7, 200/200.

**Real versus simulated:**
* Real: process kills (torture, O7, O8), the Windows filesystem and ACLs (S3/S4), Airplane mode (N7/N8, toggled by the user), browser rendering, and the restarts (N5/N6).
* Simulated: power loss (Power-Cut model), network answer loss (N2/N3: fault server or patched `fetch`), and disk full (`max_bytes` / `faultfs`).

The new results are in this file (O7, O8, S3/S4), [NETWORK_FAILURE_TESTS.md](NETWORK_FAILURE_TESTS.md) (N2–N4, N7, N8), [FRONTEND_FAILURE_TESTS.md](FRONTEND_FAILURE_TESTS.md), [POWER_FAILURE_MODEL.md](POWER_FAILURE_MODEL.md) (PC3 and the power model), and the matrix row P7.

## O8: kill nexus-api during HTTP writes (real processes)

**Harness.** [`frontend/scripts/api-kill-test.mjs`](../../frontend/scripts/api-kill-test.mjs). Each iteration:
* starts the real `nexus-api.exe` on a disposable database in a new directory (it refuses a non-empty directory);
* runs 12 concurrent HTTP writers doing PUT (~56%), DELETE (~24%) and BATCH (~20%), with values up to 4 KiB;
* kills the process with `TerminateProcess` at a seeded random moment (40–540 ms);
* restarts nexus-api on the same file;
* reads every key back over HTTP before the next round.

**Oracle.** Each key belongs to one writer, which has at most one request in flight.
* **Acknowledged (2xx):** the value must be present after restart.
* **Definite error:** the operation must not have happened.
* **No answer** (connection reset, refused or timed out): the key must hold the old value or the new one, nothing else.
* **Unknown batch:** all of its visible operations or none.
* A value that was never written must never appear.

| Run | Kills | HTTP writes | Acknowledged | Outcome unknown | Key reads verified | Unknown batches (whole / none) | Violations |
|---|---|---|---|---|---|---|---|
| seed 1 | 50 | 34,247 | 33,693 | 554 | 2,400 | 11 / 95 | **0** |
| seed 2 | 50 | 28,742 | 28,188 | 554 | 2,400 | 15 / 90 | **0** |
| seed 3 | 50 | 31,784 | 31,223 | 561 | 2,400 | 13 / 106 | **0** |
| **Total** | **150** | **94,773** | **93,104** | **1,669** | **7,200** | **39 / 291** | **0** |

* Restart to a healthy, open database took 30–80 ms per kill.
* There were no false successes: every 200 was durable.
* There were no torn or phantom values and no partially applied batches.
* Some batches with an unknown outcome were found applied whole after restart. This confirms that "outcome unknown" is a real state, not a theoretical one, and that reporting those writes as "failed" (the N2 bug) would have been wrong.

**Negative control.** `--control` runs the same harness against a deliberately broken server that acknowledges writes from memory only. It reported **FAIL, with 202 violations in 5 kills** (acknowledged values missing after restart), so the oracle does detect lost acknowledged writes.

**Reproduce** (the directory must be new):

```bash
node frontend/scripts/api-kill-test.mjs --api PATH/TO/nexus-api.exe --dir NEW_EMPTY_DIR --iterations 50 --seed 1 --json o8.json
```

```bash
node frontend/scripts/api-kill-test.mjs --control --dir NEW_EMPTY_DIR
```

* Machine-readable results: [o8-kill-seed-1.json](o8-kill-seed-1.json), [o8-kill-seed-2.json](o8-kill-seed-2.json), [o8-kill-seed-3.json](o8-kill-seed-3.json), [o8-kill-control.json](o8-kill-control.json).
* The seed fixes the workload choices and kill times. Thread and network timing are not replayable, so per-iteration counts vary between runs.

**What is real and what is not.**
* Real: the processes, the kills, the HTTP traffic on 127.0.0.1, and the Windows filesystem and fsync.
* Not covered: a physical power cut (the OS cache survives a process kill; see PC4), and the browser itself (the client's handling of an unknown outcome is tested in N2/N3).

## S3 / S4: permissions, read-only files, missing directory (real Windows filesystem)

**Tests.** `src/kvdb/fs_errors_test.go`, run through the public `kvdb.Open`, each inside its own `t.TempDir()`.

**Result: 5/5 PASS. No code change was needed.**

| Test | Condition | Error the user sees (abridged) |
|---|---|---|
| `TestOpenMissingParentDirectory` | parent folder (one or two levels) missing | `locking: acquire …\missing\db.kv.lock: … The system cannot find the path specified.` (`fs.ErrNotExist`); nothing is created anywhere |
| `TestOpenReadOnlyFile` | `FILE_ATTRIBUTE_READONLY` on the database | `recovery: opening …\db.kv: … Access is denied.` (`fs.ErrPermission`); file unchanged; reopens after the attribute is cleared |
| `TestOpenPathIsDirectory` | the path is a folder | `not a valid database file: … (rule A2): not a regular file` (`ErrBadFormat`) |
| `TestOpenNewDatabaseInDeniedDirectory` | Windows Deny ACE (write/add file) for the current user | `locking: acquire …\db.kv.lock: … Access is denied.`; the folder stays empty |
| `TestOpenExistingDatabaseInDeniedDirectory` | the same Deny ACE applied to an existing database | `Access is denied.`; file and folder unchanged; with the ACE removed, the acknowledged key is still readable |

Every error names the path and the OS reason, and none is misreported as corruption, bad format or locked. The one exception is the folder case, which is correctly reported as bad format.

The test removes its Deny ACE again before the temp folder is deleted. The ACL change only touches that disposable folder; no system security setting is changed.

**CLI check** (scratch folder):
* `kvdb put nope/missing/db.kv k v` and `kvdb put`/`get` on a read-only file both exit **1** ("failed, nothing changed") with `ERROR (io): …` and the OS reason.
* No `nope` folder was created.
* `kvdb xray` on the read-only file runs in read-only mode and reports HEALTHY.

**Reproduce:**

```bash
go test ./src/kvdb -run 'TestOpen(Missing|ReadOnly|PathIs|New|Existing)' -v -count=1
```

These tests need Windows `whoami.exe` and `icacls.exe` and are skipped elsewhere. They pass without administrator rights.

## O7: two nexus-api instances (real processes)

**Harness.** [`frontend/scripts/api-double-start-test.mjs`](../../frontend/scripts/api-double-start-test.mjs). It uses real `nexus-api.exe` processes on disposable databases. nexus-api opens the database, which takes the OS lock, before it listens, and exits on either failure.

**Result: 11/11 PASS. No code change was needed.** Machine-readable results: [o7-double-start.json](o7-double-start.json).

| Check | Result |
|---|---|
| Same database, second instance on another port | exits **1** after ~1.03 s (lock retry budget, 15 attempts) with `kvdb: database is locked by another process: …db.kv.lock` |
| …first instance meanwhile | stays healthy, accepts new writes, and its data is intact |
| Same port, different database | the second exits **1** with `listen tcp 127.0.0.1:…: bind: Only one usage of each socket address … is normally permitted.`; the first keeps answering with its own data; the second's database is released (another instance opens it) |
| Both started at the same instant on one database, 10 races | exactly one instance serves and the other exits "locked", **10/10** (loser exits after 1,028–1,032 ms) |
| Winner killed, then restarted | the new instance opens the database with all data (stale-lock check, O5) |

**Reproduce:**

```bash
node frontend/scripts/api-double-start-test.mjs --api PATH/TO/nexus-api.exe --dir NEW_EMPTY_DIR --races 10
```

**Not tested.**
* Same port *and* same database was not run separately. The database is opened first, so it is the locked case above.
* A wildcard bind (`--addr :8080`) next to a loopback bind was not tested. Listening on a non-loopback address makes Windows Defender Firewall show a system prompt, which the agent must not answer, and nexus-api defaults to, and documents, loopback only.
