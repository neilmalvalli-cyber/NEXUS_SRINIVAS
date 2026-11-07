# Crash scenario results (final pass, 2026-11-07)

All results come from real runs on 2026-11-07: Windows 11, Ryzen 7 5800H, Go 1.27.3. The kills are real process kills (`TerminateProcess`). They are **not** power cuts (see POWER_FAILURE_MODEL.md).

## Torture, 200 iterations per run (`./torture --iterations 200 --seed S [--mode M]`)

| Mode | Seed | Result | Acked | Unknown | Notable | Time |
|---|---|---|---|---|---|---|
| default | 12345 | PASS 200/200 | 95,056 | 587 | 118 kills during compaction | 73 s |
| default | 1 | PASS 200/200 | 89,980 | 585 | | 70 s |
| default | 987654321 | PASS 200/200 | 88,607 | 587 | | 68 s |
| default | −42 | PASS 200/200 | 93,950 | 582 | | 72 s |
| compaction | 12345 | PASS 200/200 | 109,266 | 594 | 168 compactions in flight at the kill | 161 s |
| compaction | 777 | PASS 200/200 | 70,296 | 593 | 150 compactions in flight | 164 s* |
| recovery-crash | 12345 | PASS 200/200 | 2,554 | 595 | 341 of 409 kills landed *during* recovery | 734 s |
| disk-full | 12345 | PASS 200/200 | 21,497 | 597 | 11,434 writes rejected `full`, all correct | 132 s* |
| large-values | 12345 | PASS 200/200 | 12,860 | 219 | 1 torn tail discarded | 172 s* |
| large-batch | 12345 | PASS 200/200 | 173,678 | 550 | 10,000-op batches | 379 s* |
| concurrent-disjoint | 12345 | PASS 200/200 | 66,898 | 376 | | 87 s* |
| same-key | 12345 | PASS 200/200 | 48,032 | 382 | | 80 s* |

\* These six ran in parallel, so their times are inflated.

**Total: 12 runs, 2,400 iterations, 0 failures.**

"Unknown" means the kill landed before the ACK. The oracle accepts either outcome for those ops and checks every acknowledged op. Reports were saved in the session scratch folder (`reh/`, `reh2/`).

**Timing caveat (L9).** recovery-crash takes about 3.7 s per iteration, so 200 iterations took 12 minutes. Each iteration verifies a 200,000-key database after 1–3 kills during recovery. For the judging slot, use a second terminal or `--iterations 50`.

**Disclosure.** During the first seven of these runs, a runaway test-process chain was also using CPU on the machine. It was my own test bug, now fixed: `seed_test.go` no longer passes an executable as `--kvdb`. It cannot change a verdict, only timing, so the recovery-crash time above is pessimistic.

## The 32 official scenarios (`go test ./src/scenarios`, quick mode)

**32 / 32 PASS.** The full table, with per-scenario method and diagnostics, is in [scenario-results-2026-11-07.md](scenario-results-2026-11-07.md).
* A1–A7: kill points in a put.
* B1–B6: batches, including a 10,000-op batch killed halfway on a 1M-key database, recovered in 787 ms.
* C1–C6: kills during recovery, truncation, compaction (before, during and after the renames) and close.
* D1–D4: concurrency.
* E1–E5: the cap or disk full.
* F1–F4: 500-kill run (30 iterations in quick mode), repeated crashes during recovery, 1M keys plus kill (recovery 562 ms), and a file truncated by hand.

## Manual and targeted runs

| Scenario | Result | Doc |
|---|---|---|
| 12 manual `kvdb serve` hard kills | 19,492 ACKs, 0 lost; check OK every time | MANUAL_KILL_REHEARSAL.md |
| Kill during a 103 MB compaction at 20–1000 ms | old or new file, never mixed; 60,000/60,000 keys | COMPACTION_SHRINK_TEST.md |
| `--max-bytes` cap: put, batch, serve; deletes free space | `ERROR (full)`, file unchanged; recovers by itself | DISK_FULL_VALIDATION.md |
| 1M keys, one frame per key | open 0.78 s, check OK | ONE_MILLION_KEY_RECOVERY.md |

## Not tested

Physical power cut (PC4), a physically full volume (S2), and the race detector (C2) are all **NOT TESTED**. See KNOWN_LIMITATIONS.md.
