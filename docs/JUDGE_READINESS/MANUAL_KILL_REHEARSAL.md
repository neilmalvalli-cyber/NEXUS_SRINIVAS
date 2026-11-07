# Manual kill rehearsal (judging minutes 6–8)

Status: **PASS. In 12 hard kills of a writing `kvdb serve`, all 19,492 acknowledged operations survived, and `check` reported OK after every restart.** Rehearsed 2026-11-07 on Windows 11 with Git Bash, using disposable files. A process kill is **not** a power cut: data the process had written but not yet synced is still in the OS cache, so it survives a kill. See POWER_FAILURE_MODEL.md.

## What the judge will do, and the exact commands

Terminal 1 (start the server; it reads ops from stdin):

```bash
./kvdb serve judge.db
```

It prints `serve: ready, <n> keys, recovery <ms> ms, max-bytes 0`. Then type (or paste) lines, each acknowledged only after it is durable:

```text
w1 1 put apple red
w1 2 put banana yellow
w1 3 batch 3
put cherry dark-red
put date brown
del banana
w1 4 get apple
```

Replies (verified): `ACK 1`, `ACK 2`, `ACK 3`, `VALUE 4 red`. Then `dump` shows `apple red`, `cherry dark-red`, `date brown`. The `batch 3` line is followed by exactly 3 op lines; the batch is atomic (all or nothing) and gets one ACK. The writer id may be omitted (`5 put elder green`). Ops from different writer ids run concurrently, so their ACKs can interleave: in the rehearsal, `ACK 5` (no writer id) arrived before `ACK 2`. Within one writer, ops are applied and acknowledged in order.

Terminal 2 (hard kill, no clean shutdown):

| Shell | Command |
|---|---|
| cmd or PowerShell (Windows) | `taskkill /F /IM kvdb.exe` |
| Git Bash (Windows) | `taskkill //F //IM kvdb.exe` (Git Bash needs `//` for Windows switches) |
| Git Bash, same terminal, server started with `&` | `kill -9 %1` |
| Linux / macOS | `kill -9 $(pgrep -f "kvdb serve")` |

Ctrl+C in terminal 1 also stops it, but that is an interrupt, not the hard kill the judge wants. Use `taskkill /F` or `kill -9`.

Then restart and verify:

```bash
./kvdb open judge.db     # runs recovery: keys=<n> recovery_ms=<ms> size=<bytes>
./kvdb check judge.db    # full verification: "check: … keys, … frames …" then OK
./kvdb dump judge.db     # every key and value, sorted by key
./kvdb serve judge.db    # carry on writing; repeat the kill
```

**Expected rule.** Every op that got an `ACK` is in `dump`. An op whose ACK did not arrive before the kill is *unknown*: it may or may not be present. A batch is present completely or not at all, never half.

## Rehearsal 1: 12 automated kill cycles under load

**Method.** For each cycle:
1. A file of 3,000 ops was piped into `kvdb serve db.kv`: single puts, plus every tenth op a `batch 3` (2 puts, plus a delete of the previous put).
2. The process was killed with `taskkill /F` after a random 100–800 ms.
3. The database was reopened, then `check` and `dump` were run.

All 12 cycles used the same database, which grew across cycles. A script (`verify_manual.py`, kept in the session scratch folder) then compared every `ACK` against the final dump.

| Cycle | Kill at | ACKs | Keys after reopen | Recovery | check |
|---|---|---|---|---|---|
| 1 | 767 ms | 2,231 | 2,233 | 4.0 ms | OK |
| 2 | 636 ms | 1,926 | 4,161 | 5.4 ms | OK |
| 3 | 100 ms | 738 | 4,901 | 5.4 ms | OK |
| 4 | 764 ms | 2,154 | 7,057 | 6.9 ms | OK |
| 5 | 569 ms | 1,709 | 8,768 | 7.9 ms | OK |
| 6 | 605 ms | 1,844 | 10,614 | 8.2 ms | OK |
| 7 | 782 ms | 2,183 | 12,799 | 9.2 ms | OK |
| 8 | 498 ms | 1,339 | 14,140 | 10.1 ms | OK |
| 9 | 359 ms | 1,264 | 15,406 | 13.4 ms | OK |
| 10 | 111 ms | 685 | 16,093 | 18.4 ms | OK |
| 11 | 505 ms | 1,591 | 17,686 | 13.6 ms | OK |
| 12 | 697 ms | 1,828 | 19,516 | 13.6 ms | OK |

**Verification result:** `cycles=12 acked_ops_checked=19492 errors=0 unacked_but_durable_ops=12`.
* Every acknowledged put was present with its exact value.
* Every acknowledged batch was fully present, and its delete was applied.
* No half batch appeared.
* In each cycle, exactly **one** op without an ACK turned out to be durable. That was the op in flight when the kill landed: its bytes had been written and were synced or in the OS cache, but the ACK was never printed. This is the documented "unknown outcome" case (I1 rule): allowed, and never reported as a failure.

## Rehearsal 2: the kill commands themselves

* `taskkill /F /IM <name>.exe`, from Git Bash as `taskkill //F //IM …`: used for all 12 cycles above, and it worked every time.
* Git Bash `kill -9 <pid>` (the pid shown by `ps`): the process was killed (bash printed `Killed`). The file then held the ACKed key, and `check` reported OK.
* A second `kvdb serve` on the same file while the first was still running was refused: `ERROR (locked): kvdb: database is locked by another process`. The lock works; the judge must kill the first server before restarting.
* PowerShell (`taskkill /F /IM kvdb.exe`, `Stop-Process -Name kvdb -Force`): **NOT TESTED by the agent**, because PowerShell hangs in the agent sandbox. `taskkill` is the same Windows program in every shell, so it is expected to behave the same. Please confirm once in your own PowerShell before judging.

## Things a judge might notice (and the answers)

* **"After reopen, there is one more key than ACKs."** That is the in-flight op described above. It was durable, but its ACK was lost with the process. The guarantee is one-directional: ACKed means present; no ACK means unknown.
* **`judge.db.lock` stays on disk after the kill.** That is by design. The lock is an OS lock held on that file, not the file's existence (DESIGN §1). A killed process releases it automatically.
* **Recovery time** stays in milliseconds here. See ONE_MILLION_KEY_RECOVERY.md for 1M keys.
