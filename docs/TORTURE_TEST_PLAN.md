# NEXUS Torture Engine 2.0 — Test plan

What `nexus-torture` tests, how each official requirement is attacked, and
what counts as passing. Design: [TORTURE_ARCHITECTURE.md](TORTURE_ARCHITECTURE.md).
Commands: [TORTURE_CLI.md](TORTURE_CLI.md). Measured results:
[TORTURE_RESULTS.md](TORTURE_RESULTS.md).

## What "PASS" means

A run passes only if **every** iteration or scenario passed and nothing was
inconclusive. An iteration passes only if all of the following hold:

1. `kvdb serve` behaved: it printed its ready line, every reply was a
   complete, well-formed line for a request that was sent, no error class
   appeared that a correct database cannot produce in that mode, it did not
   exit on its own, and after a graceful close it answered every request it
   was sent and exited 0.
2. Recovery behaved: `kvdb check --no-repair` exited 0 (`OK`) or 4
   (`NEEDS REPAIR`) and changed no byte of any file; `kvdb open` succeeded;
   `kvdb check` printed `OK`; `kvdb dump` listed as many keys as `open`
   reported; the file respects `--max-bytes` when one is set.
3. The **oracle** found a legal execution that explains the recovered state
   and every acknowledged read (see the architecture document, §1).

INCONCLUSIVE (the oracle hit a step, time or size limit) and tool errors are
reported separately and never count as PASS.

## Randomized runs (`run`)

| Mode | Attacks | Official link |
|---|---|---|
| `default` | 3 writers; puts, deletes (some of missing keys), 2–20-op batches, gets, compactions; kill at 5–600 ms | F1, D, A, B |
| `batch10` | exactly 10-op batches racing single writes | B1–B4 |
| `large-values` | values of 256 KiB–1 MiB, half exactly 1 MiB | A7 |
| `large-batch` | 10,000-op batches racing single puts/deletes on the same keys | B6, D3 |
| `concurrent-disjoint` | 2 writers on disjoint key sets | D1 |
| `same-key` | 2 writers putting 1–64 KiB values to one key | D2 |
| `readers` | 10-op batches over g0..g9 while 2 readers get those keys | D4 |
| `compaction` | 1–16 KiB values, frequent compaction requests; kills during compaction | C3–C5 |
| `recovery-crash` | 200,000-key database; after each kill, `kvdb open` is killed 1–3 times during recovery | C1, C2, F2 |
| `disk-full` | `--max-bytes 262144`: writes hit the cap; deletes and compaction free space | E1–E5 |
| `close-reopen` | 60% of iterations end with a graceful close (stdin EOF), sometimes killed during it | C6 |
| `mixed-long` | everything, kill at 2–8 s | F1 |

Every failure is reproducible from its seed and iteration
(`--iteration N`), and from the saved database (`--from-db`).

## The 32 official scenarios (`scenarios`)

Every scenario runs against the real `kvdb` executable, records each kill
point with its outcome, verifies the recovered state, and is written to
`scenarios.jsonl` / `scenarios.json` / `SCENARIOS.md`. A scenario with no
kill points cannot pass. "Crash-state replay" means: run the real write on a
copy, then restart the real `kvdb` on every prefix of the bytes it appended,
which is exactly what a process kill can leave behind (see the architecture
document, §5). "Engine X" means the scenario also runs the randomized engine
in mode X and requires PASS.

| ID | Scenario | Invariant checked | Method |
|---|---|---|---|
| A1 | Kill before any bytes of a put reach the file | Key absent or holds its previous value | crash-state replay at prefix length 0 of the put's frame; real kills of `kvdb put` at random moments |
| A2 | Kill after some bytes of the record are written (torn write) | Previous value; torn bytes are never returned as a value | crash-state replay at every byte offset inside the put's frame |
| A3 | Kill after the record is fully written but before fsync | Either outcome; nothing else corrupted | crash-state replay of the complete frame (a process kill keeps written bytes in the OS cache); real kills |
| A4 | Kill after fsync returns but before put returns success | Key present with the new value | crash-state replay of the complete, synced frame; real `kvdb put` kills that landed after the write was durable |
| A5 | Kill immediately after put returns success | Key present with the new value, no exceptions | real serve sessions killed right after each ACK; `kvdb put` exit 0 then restart |
| A6 | Kill during a delete | Old value or absent, never a corrupted value | crash-state replay at every byte of the delete's frame; real kills of `kvdb del` |
| A7 | Kill during a put of a 1 MiB value | Same as A1-A5 for a value spanning many disk blocks | crash-state replay at sampled offsets of a 1 MiB put frame (written through serve; every byte near the ends and 4 KiB boundaries); engine large-values (real kills) |
| B1 | Kill after 3 of 10 batch operations are written | None of the 10 visible | crash-state replay at every offset up to 30% of a 10-op batch frame |
| B2 | Kill after all 10 are written but the commit marker is not | None of the 10 visible | crash-state replay at every offset from 30% to one byte short of the frame (the frame checksum is the commit marker) |
| B3 | Kill after the commit marker is written but before fsync | All or none, never some | crash-state replay of the complete batch frame; real kills of `kvdb batch` |
| B4 | Kill after the batch returns success | All 10 visible | real serve sessions killed right after the batch ACK |
| B5 | A batch that puts and deletes the same key | In order, last op wins (README), also after a crash | batch put X/del X, del Y/put Y, put Z twice; replay at every offset; all-or-none with the documented order |
| B6 | A 10,000-op batch killed halfway | None visible; recovery under 2 seconds | database with many keys; crash-state replay around the middle of a 10,000-op frame; timed recovery; engine large-batch |
| C1 | Kill during open() while it replays the log | Next open succeeds with the same correct state | real kills of `kvdb open` at random moments of its run on a large database with a torn tail |
| C2 | Kill during open() while it truncates a torn tail | Next open succeeds | torn database restored before each kill; kills aimed at the end of recovery; truncated and untruncated states |
| C3 | Kill during compaction, before the new file replaces the old | Old file authoritative, all data intact | crash states from the real compaction output: no temp, unsealed .compact of many lengths, partial seal, sealed .compact |
| C4 | Kill during compaction, in the middle of the rename/swap | Exactly one of old or new authoritative, nothing lost | crash states between the two renames (path missing, .old + sealed .compact) and after the second |
| C5 | Kill after the swap but before temp-file cleanup | Data intact; temp files cleaned or ignored | crash states with the new file in place and .old left behind; engine compaction mode (real kills) |
| C6 | Kill during close() | Same as a kill at any other time | serve stdin closed with 20 requests still queued, process killed 0-3 ms later, oracle check; engine close-reopen mode |
| D1 | Two writers on different keys, killed at random | Every acknowledged put from either writer present | engine concurrent-disjoint: real kills, oracle per iteration |
| D2 | Two writers on the same key, killed at random | Value from one writer, never a mix | engine same-key: unique 1-64 KiB values identified by hash |
| D3 | A long batch and single puts, killed at random | Batch all-or-nothing; acknowledged puts present | engine large-batch: 10,000-op batches racing single writes |
| D4 | A reader during a batch on the same keys | Before or after, never in between | engine readers: 10-op batches over g0..g9, two readers; the oracle rejects any read order that sees half a batch |
| E1 | Size cap reached during a single put | Error; database readable; previous data intact | `kvdb put` with `--max-bytes` until `full`; state and file compared before/after |
| E2 | Cap reached halfway through a batch | Error; none of its operations visible | 10-op batch that does not fit under the cap |
| E3 | Cap reached, then process killed | Restart succeeds; acknowledged data intact | engine disk-full: `--max-bytes 262144`, real kills |
| E4 | Cap reached during compaction | Compaction fails or completes; no data lost; database opens | compact request on a full database under the cap, plus kills during it |
| E5 | Deletes free space, compaction runs, writes succeed again | Usable again without intervention | fill to `full`, delete half, write again under the same cap |
| F1 | Consecutive random kills and restarts | Invariant holds after every one | engine default mode (500 kills in full mode) |
| F2 | Kill, restart, kill during recovery, again | Invariant holds; recovery idempotent | chains: torn database, `kvdb open` killed twice, then open; engine recovery-crash |
| F3 | 1,000,000 keys written, killed, open() | Recovery under 2 seconds; acknowledged keys present | keys loaded through serve (acknowledged), more puts, real kill, timed open, oracle check |
| F4 | Database file truncated by 1 byte by hand | Open succeeds; at most the last unacknowledged record lost | truncate the last byte; `check --no-repair` reports it without changing the file; open repairs |

Sizes: quick mode uses 200,000 keys for B6, C1, C2 and F3 and 50 kills for
F1. `--full` uses 1,000,000 keys and 500 kills, plus more iterations of every
engine part.

**Scenarios where a real kill cannot hit the exact instant.** A process kill
from outside cannot be timed to land "between fsync returning and put
returning" (A4), or exactly at byte k of a frame (A2, B1, B2). For those
points the scenario replays the disk state such a kill leaves, using bytes
produced by the real executable, and restarts the real executable on it. It
also adds real random kills, which land wherever they land. The result
record says which method was used.

## Checking the checker

| Test | What it proves |
|---|---|
| `oracle.TestOracleUnitCases` (35 cases) | legal outcomes accepted (concurrent overlap, unknown applied or not, last op wins, missing-key deletes); every violation kind detected |
| `oracle.TestOracleMatchesBruteForce` | on 20,000 random small histories the oracle agrees with an exhaustive checker |
| `oracle.TestOracleStressLegalHistoriesPass` | large legal histories (8 writers × 200 keys, 16 writers with big batches, 10,000-op batches) pass within limits; the adversarial config may be INCONCLUSIVE but never FAIL |
| `oracle.TestOracleStressInjectedViolationsFail` | dropped acked put, partial batch, corrupt value, rejected write visible and stale read are all detected in large histories |
| `oracle.TestOracleInconclusiveIsNotPass` | a search that runs out of budget is INCONCLUSIVE, not PASS |
| `proc` tests (fake executable) | half a line is not an ACK; hangs are killed on timeout; stray replies and garbage are reported |
| `workload` tests | same seed ⇒ same stream; iterations and writers are independent streams |
| `e2e.TestMutationsAreDetected` | every mutation of `kvdb-mutant` is caught; the faithful control passes |
| `e2e.TestRealKvdbPasses`, `e2e.TestScenarioSampleOnRealKvdb` | the real executable passes engine runs and a scenario sample |

## Mutation testing (`mutants`)

| Mutation | A correct tester must report |
|---|---|
| `ack-before-sync` | acknowledged write lost (or a partial batch) after a kill |
| `drop-acked` | acknowledged write lost, or a read that saw it |
| `partial-batch` | partial batch |
| `wrong-unknown` | rejected write visible |
| `bad-recovery` | acknowledged write lost |
| `corrupt-value` | value never written |

The control (`none`) must PASS: it shows the mutant wrapper itself is sound,
so a detection is caused by the mutation.

## Validation run (what is executed for the results)

All in a fresh scratch directory, with freshly built binaries:

1. `go test ./torture2/...` (includes the end-to-end tests);
2. `run --mode default --seed 12345 --iterations 500`;
3. 100 iterations of each other mode (seed 2026);
4. `mutants` (seed 777, up to 40 iterations each);
5. reproducibility: the same seed run twice; the request streams are compared;
6. `scenarios --full` (all 32).
