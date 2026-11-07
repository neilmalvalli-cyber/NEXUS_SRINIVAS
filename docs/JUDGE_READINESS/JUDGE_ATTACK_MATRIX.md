# Judge attack matrix — Phase 1 audit (2026-11-07)

Status: **Phase 1 inventory, with Phase 3 fix P7 applied (2026-11-07).** The Phase 1 inventory was written before any source change. The only engine change since then is the P7 fix (rule C3 amendment, row P7).

**Status legend**
* **PASS\*** — covered by an existing automated test that passed in this session's `go test ./...` run (2026-11-07, all packages ok, including `src/scenarios` and `torture2/e2e`), or by the recorded run named in the Evidence column. This is real Windows processes and filesystem unless the scenario is marked *sim*.
* **NOT TESTED** — no test exists yet. These are the Phase 2 candidates.
* **FAIL (known)** — the behaviour violates a judge-visible expectation. It is documented, not fixed.
* **N/A-local** — does not apply to the localhost demo configuration.

The "Expected" column states the documented guarantee from [FAILURE_GUARANTEES.md](../FAILURE_GUARANTEES.md) and [RECOVERY.md](../RECOVERY.md).

## A. Process kills during writes (real `kvdb serve` processes, real TerminateProcess)

| ID | Judge action | Expected | Status | Evidence |
|---|---|---|---|---|
| W1 | Kill before PUT bytes reach file | key absent or previous value | PASS\* | scenario A1 |
| W2 | Kill mid-record (torn) | previous value; torn bytes never returned | PASS\* | A2 |
| W3 | Kill after write, before fsync | either outcome; nothing else corrupted | PASS\* | A3 |
| W4 | Kill during fsync | either outcome | PASS\* *(approximated by A3/A4 kill points; a true mid-FlushFileBuffers kill cannot be injected deterministically)* | A3, A4 |
| W5 | Kill after fsync, before ACK | new value present | PASS\* | A4 |
| W6 | Kill right after ACK | new value present | PASS\* | A5; torture default 300 iterations, 144,671 acknowledged writes verified |
| W7 | Kill during DELETE | old value or absent, never torn | PASS\* | A6 |
| W8 | Kill during BATCH (partial / complete / 10k ops) | all-or-nothing | PASS\* | B1–B6; torture `large-batch` |
| W9 | Kill during 1 MiB value write | old or new, never partial | PASS\* | A7 |
| W10 | Kill with group commit active (concurrent writers) | every ACKed op durable; merged frame all-or-nothing | PASS\* | torture default (concurrent writers), `storage/concurrency_test.go`, D1–D3 |

## B. Partial writes and corruption (disposable copies)

| ID | Damage | Expected | Status | Evidence |
|---|---|---|---|---|
| P1 | Truncate last frame / incomplete header | truncate torn tail; earlier records intact | PASS\* | F4, X-RAY lab `torn-tail`, `incomplete-header`, Power-Cut C1/C4 outcomes |
| P2 | Final payload CRC flipped | C5: truncated as torn tail | PASS\* | X-RAY lab `final-payload-checksum` |
| P3 | Mid-log payload corrupted | open refuses; file untouched | PASS\* | lab `committed-frame-corrupted`, recovery tests |
| P4 | Sequence corrupted (CRC resealed) | refuse (C7) | PASS\* | lab `invalid-seq` |
| P5 | Malformed lengths / op code / entry CRC | refuse (C7) | PASS\* | lab `invalid-op`, `entry-checksum`; format fuzz tests |
| P6 | Superblock corrupted / bad magic / unsupported version | refuse, explicit error | PASS\* | lab `bad-magic`, `superblock-checksum`, `unsupported-version` |
| **P7** | **Mid-log frame header damaged (CRC fails) with valid frames after it** | refuse; file untouched; no committed frame lost | **PASS (fixed 2026-11-07)** | Was FAIL: the engine treated it as a C2 torn tail and truncated later committed frames. Fix: rule C3 amended. If a complete valid frame with seq ≥ expected follows a damaged header, open refuses with `ErrCorruption` and does not truncate (`recovery/forward.go`, shared with X-RAY). No format change. Tests: `TestTailC3MidLogHeaderDamageRefused` (4 variants), `TestTailC2LaterFrameNotProof`, `TestTailC3CraftedValueBehindLostHeaderRefusesSafely`, xray `TestMidLogHeaderDamageRefusedNotTruncated`. No false refusals after the fix: Power-Cut seeds 1–3 (3,937 images, 0 failures, 18/18 controls) and torture `run --seed 7 --iterations 200` (200/200 PASS, 87,015 acked writes verified, 0 recovery failures). Trade-off: see KNOWN_LIMITATIONS.md L1. |

## C. Process and OS

| ID | Action | Expected | Status | Evidence / gap |
|---|---|---|---|---|
| O1 | Graceful close / kill during close | consistent | PASS\* | C6 |
| O2 | Kill during recovery (replay / tail truncation) | next open succeeds | PASS\* | C1, C2, F2 |
| O3 | Repeated kill/restart | consistent | PASS\* | F1 |
| O4 | Second process opens same DB | `ErrLocked`, no damage | PASS\* | locking tests, lab `lock-conflict` |
| O5 | Stale lock after abnormal exit | reopen succeeds (OS releases share-mode lock) | PASS\* (implicit in every kill scenario) | scenarios A–F |
| O6 | Kill nexus-api while the dashboard is open | banner "Backend disconnected"; actions disabled; recovers on restart | PASS\* (manual, this session) | browser check during Prompt 19 |
| O7 | Two nexus-api instances started together on the same DB | second fails with locked / port in use; first unaffected | **PASS (new test 2026-11-07)** | `frontend/scripts/api-double-start-test.mjs`, 11/11 with real processes. Same database on another port: the second exits 1 after ~1 s with "database is locked by another process", while the first keeps serving and writing. Same port: the second exits 1 with the Windows address-in-use error and releases its database. Simultaneous start: exactly one winner in 10/10 races. Restart after a kill works. [JUDGE_TEST_RESULTS.md](JUDGE_TEST_RESULTS.md#o7-two-nexus-api-instances-real-processes) |
| O8 | Kill nexus-api *during an HTTP PUT* | client sees network error; write either durable or absent; no false success | **PASS (new test 2026-11-07)** | `frontend/scripts/api-kill-test.mjs`: 150 real TerminateProcess kills of nexus-api (3 seeds), 94,773 HTTP writes, 93,104 acknowledged, all durable, 1,669 outcome-unknown resolved to old or new value, 0 partial batches, **0 violations**. Negative control (memory-only server) caught 202 violations. [JUDGE_TEST_RESULTS.md](JUDGE_TEST_RESULTS.md#o8-kill-nexus-api-during-http-writes-real-processes) |

## D. Storage / filesystem

| ID | Action | Expected | Status | Evidence / gap |
|---|---|---|---|---|
| S1 | Disk full (cap) during PUT / BATCH / compaction | `ErrFull`, nothing ACKed that isn't durable | PASS\* *(simulated with `--max-bytes` and faultfs, not a physically full volume)* | E1–E5, torture `disk-full` |
| S2 | Physically full volume (real ENOSPC) | `ErrFull` / I/O error, no false ACK | **NOT TESTED** | needs a small VHD or RAM disk |
| S3 | Read-only file / directory, access denied | open fails with a clear error | **PASS (new tests 2026-11-07)** | `src/kvdb/fs_errors_test.go`: read-only attribute, path is a directory, Windows ACL deny on a new and an existing database. Each case gives an error naming the path plus the OS reason (`fs.ErrPermission`), creates nothing, leaves the file byte-for-byte unchanged and leaves no stale lock; the database reopens once the cause is removed. CLI: exit 1, `ERROR (io)`. No read-only open mode exists (KNOWN_LIMITATIONS L2); X-RAY reads a read-only file. |
| S4 | Missing parent directory | clear error, no creation elsewhere | **PASS (new test 2026-11-07)** | `TestOpenMissingParentDirectory`: `fs.ErrNotExist` with the path; no directory or file is created, either there or in the working directory. CLI: exit 1. nexus-api deliberately creates its own `--db` directory (documented flag behaviour). |
| S5 | Missing DB file | created (open) or `DB_MISSING` (X-RAY) | PASS\* | xray `TestMissingAndUsage`, kvdb tests |
| S6 | Leftover `.compact` / `.old` / `.new`; old+new both present | authority rules A1–A7 | PASS\* | C3–C5, lab `interrupted-compaction`, `compaction-leftover`, Power-Cut A3/A5 outcomes |
| S7 | Antivirus/indexer briefly locks the file during rename | bounded retry, then error | PASS\* (unit) | `fileio/retry` tests; not reproduced with a real external process |

## E. Compaction × recovery

| ID | Action | Status | Evidence |
|---|---|---|---|
| K1 | Crash before / during output / after sync / mid-swap / after swap | PASS\* | C3, C4, C5; Power-Cut (compaction is in the workload) |
| K2 | Compaction during heavy writes and reads | PASS\* | concurrency test, torture `compaction` 100 iterations |
| K3 | Crash during recovery after compaction | PASS\* | F2 together with C-group |

## F. Concurrency

| ID | Action | Status | Gap |
|---|---|---|---|
| C1 | Readers / writers / same key / batches / compaction concurrently | PASS\* | `storage/concurrency_test.go`, D1–D4, `nexus-api` concurrent scan test |
| C2 | **Go race detector** | **NOT TESTED** | needs cgo (gcc), which is not installed |

## G. Network and frontend (highest gap density)

| ID | Action | Expected | Status |
|---|---|---|---|
| N1 | API down → judge clicks PUT | no request sent (buttons disabled) or a clear "backend unreachable" error; no success toast | PASS\* (disabled state verified manually); click-path while health is still "ok" for ≤5 s **NOT TESTED** |
| N2 | Connection dropped after server committed, before response | UI must say "outcome unknown", not "failed" | **PASS (fixed 2026-11-07)**. Was FAIL: the client reported dropped, cut-off or unreadable write responses as "failed". Fixed in `client.ts`; harness 13/13 (pre-fix client: 8 fail); real UI + real nexus-api shows "PUT outcome unknown" while the value is durable. *Network loss simulated.* [NETWORK_FAILURE_TESTS.md](NETWORK_FAILURE_TESTS.md) |
| N3 | Client timeout on PUT (30 s for PUT) | same as N2 | **PASS (fixed 2026-11-07)**. Timeout on PUT/DELETE/BATCH is now outcome-unknown (harness, simulated hang) |
| N4 | Retry after uncertain PUT | no automatic retry; manual retry safe | **PASS (analysis + verified no auto-retry)**. Mutations use `retry: false` and `request()` never retries (exactly one request in every harness case and in the browser test). All operations are absolute, so a manual repeat leaves the same state. |
| N5 | Restart API with frontend open | auto-reconnect via 5 s health poll | PASS\* (manual) |
| N6 | Restart frontend with API up | reload works | **PASS (real, 2026-11-07)** | Dashboard dev server stopped (HTTP 000 while down; API stayed healthy), restarted, then `offline-check.mjs` 14/14 ([n6-frontend-restarted.json](n6-frontend-restarted.json)); Database Explorer showed Backend Connected with live data. |
| N7 | Disable network adapter | localhost (127.0.0.1) unaffected | **PASS (real, 2026-11-07)**. Airplane mode toggled by the user. `offline-check.mjs` verified the state itself: only the virtual VirtualBox adapter up, internet unreachable (`EHOSTUNREACH`). 16/16: API health, durable round trip, X-RAY, all 11 pages. The browser PUT/GET with the badge Connected is user-reported. A first attempt was invalid (network still on) and is kept, labelled, not counted. [NETWORK_FAILURE_TESTS.md](NETWORK_FAILURE_TESTS.md) |
| N8 | Internet off | no external dependency at runtime | **PASS (real, 2026-11-07)**. Dependency check (no external URLs or fonts; 323 page resources, all local; no outbound Go calls) plus the real offline run above (internet unreachable, 16/16). Caveat: Next dev telemetry is sent and forgotten. |

## H. Power-Cut Explorer (simulation only)

| ID | Check | Status |
|---|---|---|
| PC1 | Records writes, syncs, creates, moves, removes, truncates, dirsyncs | PASS\* (`powercut_test.go`, recorded run) |
| PC2 | Images opened with the real recovery; oracle checks ACKed ops, no phantoms, batch atomicity | PASS\* (1,278 images, 0 failures; 6/6 controls) |
| PC3 | Seed replay identical | **PASS with `--writers 1`; documented limit with concurrency** (2026-11-07). Two `--seed 11 --writers 1` runs were identical in every figure (146 events, 162,711 bytes, 147 points, 623 images, same outcomes and variants). Three `--writers 4` runs had the same 240-op workload but different interleavings (1,249–1,325 images), all passing. POWER_CUT_EXPLORER.md corrected (it said the crash-point sample repeats). Evidence: [pc3-replay.json](pc3-replay.json). |
| PC4 | Physical power cut | **NOT TESTED** (requires hardware; never claimed) |

## I. X-RAY

All listed cases (healthy, torn tail, corrupted frame, checksum, sequence, superblock, missing, interrupted compaction, lock held) are **PASS\*** in `xray_test.go` (16 lab cases, read-only verified byte-for-byte, repair verified or refused). The "unreadable file (permission denied)" case is **NOT TESTED**.

## Counts (Phase 1)

| | Count |
|---|---|
| Scenario rows (I counted as one row) | 50 |
| PASS\* (existing evidence; N1 only partially) | 35 |
| PASS (fixed or newly tested in Phase 2/3) | 12 (P7, N2, N3, N4, O8, S3, S4, O7, N7, N8, N6, PC3) |
| FAIL (known) | 0 |
| NOT TESTED | 3 (S2 needs admin, C2 needs a C toolchain, PC4 needs hardware) |
| Inconclusive | 0 |
| Not implemented | 0 |

(W4, S1 and S7 pass only in an approximated or simulated form, as noted in their rows.)

## Highest-priority gaps for Phase 2

1. ~~**P7 — mid-log header damage silently truncates committed frames.**~~ **Fixed 2026-11-07** (approved by the user). See row P7.
2. ~~**N2/N3 — uncertain write outcome in the UI.**~~ **Fixed 2026-11-07**, see NETWORK_FAILURE_TESTS.md. Verify how `lib/api/client.ts` classifies an abort or timeout on PUT/DELETE/BATCH. If it shows "failed", change it to "outcome unknown — read the key back".
3. ~~**O8 — kill nexus-api mid-HTTP-PUT.**~~ **Done 2026-11-07**, 0 violations in 150 real kills. A harness that fires PUTs, kills the API process tree, restarts it, and checks every 200-acknowledged key.
4. ~~**S3/S4 — permissions and missing directory.**~~ **Done 2026-11-07**, no bug found. Small Go tests on a disposable directory (read-only attribute and ACL deny on Windows).
5. ~~**N7 — adapter disabled.**~~ **Done 2026-11-07** (real Airplane mode, 16/16). A documented manual procedure plus a scripted 127.0.0.1 check. Toggling the adapter requires admin and is a system setting, so the judge or you would do it, not the agent.
6. **S2 — real ENOSPC.** Mount a small VHD (needs admin), so this is manual. Otherwise it stays marked simulated.
7. ~~**O7 — double API start.**~~ **Done 2026-11-07**, no bug found.
8. **C2 — race detector.** Install a gcc toolchain or document the limitation.

Deliverables still to write after Phase 2–6: JUDGE_TEST_RESULTS.md, NETWORK_FAILURE_TESTS.md, POWER_FAILURE_MODEL.md, FRONTEND_FAILURE_TESTS.md, KNOWN_LIMITATIONS.md, LIVE_JUDGE_DEMO.md.
