# NEXUS Torture Engine 2.0 — Architecture

`nexus-torture` is the first-party crash tester for NEXUS. It starts the
**real** `kvdb` executable, drives it with concurrent requests, kills it
(`TerminateProcess` on Windows), restarts it, and has an **independent
oracle** decide whether the recovered database is one that a correct
implementation could have produced. Only the oracle's verdict can produce a
PASS.

Source: [`torture2/`](../torture2). Go standard library only, about 7,500
lines including tests. How to run it: [TORTURE_CLI.md](TORTURE_CLI.md). What
it tests: [TORTURE_TEST_PLAN.md](TORTURE_TEST_PLAN.md). Measured results:
[TORTURE_RESULTS.md](TORTURE_RESULTS.md).

```
                ┌────────────── nexus-torture ───────────────┐
 seed, iter ──► │ workload ──► engine ──► proc ──► kvdb serve │ (real process, pipes)
                │   (requests)   │  history  ▲   kill / exit  │
                │                ▼           │                │
                │            oracle ◄── engine/recover ◄── kvdb check --no-repair,
                │           (verdict)        open, check, dump (real CLI)
                │                │                            │
                │   events.jsonl, results.jsonl, summary.json, failures/iter-N/ │
                └────────────────────────────────────────────┘
```

## Packages

| Package | Role | Independently tested by |
|---|---|---|
| `oracle` | history recorder and the checker that decides PASS / FAIL / INCONCLUSIVE; failure minimizer | `oracle/check_test.go`: 35 hand cases, 20,000 random histories against a brute-force checker, stress tests, injected violations |
| `proc` | launching, talking to, killing and timing the real executable | `proc/proc_test.go` with a fake `kvdb` (partial lines, hangs, garbage, stray replies) |
| `workload` | deterministic request streams per mode, seed and iteration | `workload/workload_test.go` (same seed ⇒ same requests; streams independent) |
| `engine` | one iteration: start, write, kill or close, recover, verify; reports | `e2e/e2e_test.go` against freshly built binaries |
| `scenario` | the 32 official scenarios A1–F4 | `scenario/catalog_test.go` (complete, ordered, every invariant stated); `e2e` runs a sample |
| `mutation` + `mutants/kvdb-mutant` | proof that the tester catches broken databases | `e2e/e2e_test.go` (all 6 mutations caught, control passes) |
| `cmd/nexus-torture` | command line: `run`, `scenarios`, `mutants`, `modes` | — |

The older tool in [`src/cmd/torture/`](../src/cmd/torture) is kept unchanged because the Go
scenario tests in `src/scenarios` build and use it. Torture Engine 2.0 is a
new design, not a rename. It keeps only the request protocol of `kvdb serve`
and the idea of unique values. The oracle, process handling, workloads,
scenarios, mutation testing and reports are new.

## 1. The independent oracle (`oracle/`)

**What is recorded.** Every request becomes an `Op` with a writer, a kind
(put, del, batch, get, compact), its mutations, a send time and a reply time.
Its status is one of:

| Status | Meaning | How the oracle treats it |
|---|---|---|
| acknowledged | a complete `ACK`/`VALUE`/`NOTFOUND` line arrived | must have taken effect, exactly once, atomically |
| rejected | a complete `ERR` line with a *definite* class (`full`, `invalid`, `too-large`, `failed`, `closed`, `protocol`), or never written to the pipe | must **not** be visible |
| unknown | no reply before the kill, or `ERR unknown`/`ERR io` | may or may not have taken effect, but if it did, all of it did |

Times come from one monotonic clock. A reply is stamped when its complete
line is read; half a line is never an ACK.

**Every value is unique.** Values embed the request ID and operation index
(`i3-w1-000042/2/…`), and the oracle stores a 64-bit hash. A recovered value
therefore names the request that wrote it. Corrupted or torn bytes match no
request.

**What is checked.** The oracle does not compare against submission order.
It looks for *any* legal execution:

1. Every acknowledged write, and every unknown write that is chosen to have
   happened, takes effect at a single instant between its send time and its
   reply time (an unknown write has no reply time). Writes are atomic: a
   batch's operations all take effect at that instant, in order, and the last
   operation on a key wins.
2. If request A was answered before request B was sent, A takes effect first
   (real-time order, which also gives per-writer order because each writer
   waits for its reply).
3. For every key, the recovered value is the effect of the **last** write to
   that key in that order. If no write applies, it is the starting value.
4. Every acknowledged `get` returned the value of the latest write before it
   in that order.
5. Keys nobody wrote are unchanged; rejected writes are invisible.

**How it searches without factorial blow-up.** Writes and reads are nodes of
a graph; "takes effect before" is an edge. Real time is encoded with one
chain of time points rather than O(n²) edges. For each key, the only real
choice is which write was last. The candidates are the writes whose net
effect equals the recovered value, plus the starting state. Every other
applied write on that key gets an edge to the chosen one. For each read, the
choice is which write it saw; every other write to that key goes either
before that write or after the read (a two-way choice, settled lazily). The
solver:

* makes all forced choices first, in bulk (most keys have exactly one
  candidate), and rejects at once if they already form a cycle;
* then backtracks over the remaining choices, reads first in send order,
  most recent candidate first, keys with fewest candidates next;
* answers reachability with a time frontier instead of a full graph search;
* filters candidates statically: an acknowledged write that was overwritten
  by a later acknowledged write cannot be last.

Unknown writes are applied only when some key or read needs them. If one is
visible on one key, it is applied on all of them, which is what catches
partial batches. Realistic histories (thousands of requests, 3–16 writers,
10,000-op batches) are decided in milliseconds. Adversarial histories (4
keys, constant overwrites, many concurrent unknown writes) are the hard case.
The search is bounded by a step limit, a time limit and a write limit. If a
limit is hit, the verdict is **INCONCLUSIVE**: it is reported separately,
the exit code is 3, and it never counts as a pass.

**Violation kinds.** `acknowledged-write-lost`, `partial-batch`,
`rejected-write-visible`, `value-never-written`, `untouched-key-changed`,
`invalid-read` and `no-legal-order`. Each comes with the key, the expected
and found values, and an explanation. A conflict is shown as a cycle with
the reason for every edge (real time, last writer, read-from). Every request
on that key is listed with its times and status.

**Minimizer.** On FAIL, `Minimize` first projects the history onto the
violating key, which is sound because a projection of a legal history is
legal. It then removes requests one at a time while the check still fails
with the same kind. The result, `minimized.json`, is usually one key and one
to three requests. The verdict on the full history stays authoritative.

**How we know the oracle is right.**
`TestOracleMatchesBruteForce` generates 20,000 small random histories (up to
3 writers, 3 keys, batches, deletes, reads, unknown and rejected requests).
It decides each one with a brute-force checker that enumerates every subset
of unknown writes and every linearization. The two agree on all 20,000
(11,967 legal, 8,033 illegal). Stress tests check that large legal histories
pass and that five kinds of injected violations fail.

## 2. Real process management (`proc/`)

* `Locate` refuses to start without a working executable: running it with no
  arguments must print the usage text and exit 2. Otherwise the run fails
  with a clear error before any iteration.
* `StartServe` launches `kvdb serve <db>` with pipes. A reader goroutine
  parses stdout **line by line**; only complete lines are replies. A reply
  for an unknown ID is recorded as stray output and fails the iteration.
* `Kill` uses `Process.Kill` (`TerminateProcess` on Windows) and reports
  whether it landed on a live process. `Wait` has a timeout.
* `Run(timeout, …)` runs one CLI command. A command that hangs is killed and
  reported as timed out, never waited on forever. `RunKillAfter` kills a
  command after a chosen delay (used for kills during `open`).
* After every kill, the engine waits for all writer goroutines with a
  timeout. A writer that does not stop is a tool error (exit 2), never
  silently abandoned. A request that gets no reply within the reply timeout
  fails the iteration on liveness.
* Peak memory of the `serve` process is read on Windows with
  `K32GetProcessMemoryInfo`.

Mocks are used only in `proc_test.go`. Everything else drives the real
`kvdb`.

## 3. Workloads (`workload/`)

A mode fixes the writers, readers, key space, value sizes, operation
weights, batch sizes, kill window and options (cap, preload, recovery
kills, graceful close). Twelve modes cover the official requirements
(`nexus-torture modes`). Randomness is derived with SplitMix64:
`IterSeed(seed, iteration)`, then one independent stream per purpose
(schedule, writer 0, writer 1, …). So:

* the same seed and iteration always produce the same requests, values and
  kill time, whatever happened in other iterations;
* `--iteration N` replays exactly iteration N of a longer run;
* `--from-db` restarts from the database copy saved with a failure.

Timing jitter can change how many requests were sent before the kill. The
request *stream* is identical (checked by the reproducibility test in
[TORTURE_RESULTS.md](TORTURE_RESULTS.md)).

## 4. One iteration (`engine/`)

1. Copy the database to `evidence/pre/`.
2. Start `kvdb serve`, wait for its `ready` line, then start the writers (and
   readers). Each writer sends one request, waits for the reply, records it
   and continues.
3. At the kill time, either kill the process, or (close-reopen mode) close
   its input for a graceful shutdown and sometimes kill it during the close.
4. Wait for the process and the writers (both with timeouts). In-flight
   requests become *unknown*. Unexpected exit, stray output, protocol
   errors, impossible error classes and replies missing after a graceful
   close each fail the iteration.
5. Copy the files to `evidence/after-kill/`. In recovery-crash mode, kill
   `kvdb open` 1–3 times during recovery.
6. **Recover** with the real CLI: `check --no-repair` (exit 0 or 4, and the
   files must be byte-identical afterwards), then `open` (timed), `check`
   (must say `OK`), `dump` (key count must match and the file must respect
   the cap).
7. **Oracle.** PASS only if the oracle says PASS. On FAIL the evidence is
   kept in `failures/iter-N/` with `history.json`, `failure.txt`,
   `minimized.json`, the pre and post files, and the exact repro command.

Outputs per run: `events.jsonl` (every send, reply, kill, recovery and
verdict), `results.jsonl` (one record per iteration), `summary.json` (totals,
the `verification` evidence block and performance), and a terminal summary
with `RESULT:`, `VERIFY:` and `PERF:` lines ([TORTURE_CLI.md](TORTURE_CLI.md)
lists every counter). After a failure, the next iteration starts from the
recovered database, so `--keep-going` never turns one failure into a cascade.
Exit status: 0 PASS, 1 FAIL, 2 tool error, 3 INCONCLUSIVE.

## 5. Official scenarios (`scenario/`)

Each of the 32 scenarios has a stable ID, a title, an explicit invariant, a
method and a body that runs against the real executable. Every kill point is
recorded with its outcome. The result is written to `scenarios.jsonl`,
`scenarios.json` and `SCENARIOS.md`. A scenario with zero kill points cannot
pass.

Two techniques are used:

* **Real kills** of `kvdb serve`, `kvdb put/del/batch` and `kvdb open` at
  random moments, judged by the oracle or by exact state comparison.
* **Crash-state replay.** Killing a process at a chosen byte inside a write
  is not possible from outside. But a killed process leaves exactly a
  *prefix* of the bytes it appended: the OS keeps what was written and drops
  nothing else. The scenario runs the real write on a copy, then for each
  prefix length (every byte for small frames; ends, 4 KiB boundaries and a
  sample for 1 MiB) writes that prefix and restarts the real `kvdb`.
  Compaction states (unsealed, partly sealed and sealed temp file, between
  the two renames, before cleanup) are built from the real compaction
  output.

## 6. Mutation testing (`mutation/`, `mutants/kvdb-mutant`)

`kvdb-mutant` is a separate executable: the NEXUS library plus the same CLI
surface, with one bug switched on by `KVDB_MUTANT`. The official `kvdb`
binary is never modified. `nexus-torture mutants` runs the engine against
each mutation, plus a faithful control (`none`) that must pass:

| Mutation | Simulates |
|---|---|
| `ack-before-sync` | ACK sent before the write is durable (written 25 ms later) |
| `drop-acked` | every 7th write acknowledged but never written |
| `partial-batch` | batches applied one operation at a time |
| `wrong-unknown` | a durable put reported as a definite failure |
| `bad-recovery` | recovery rolls back the last acknowledged put |
| `corrupt-value` | every 6th put stores a value with one byte changed |

## 7. Performance numbers

Every iteration records restart time (wall time of `kvdb open`), recovery
time (as reported by `kvdb open`), oracle time, verification time and peak
`serve` memory. The summary adds iterations per second, the tester's peak
heap and total duration. Measured values: [TORTURE_RESULTS.md](TORTURE_RESULTS.md).
