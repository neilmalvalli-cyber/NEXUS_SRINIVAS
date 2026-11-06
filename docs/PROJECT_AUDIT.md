# NEXUS — project audit and frontend readiness (Prompt 13)

Date: 2026-11-06, branch `phase1-format`, last commit `8058c2d` **plus a large
uncommitted working tree** (see section 9). Assessment only: no engine code was
changed for this audit.

Method: every claim below was checked against the source code or a log file in
this repository. "Evidence" means a log under `docs/results/` written by a
command that actually ran. Where something was not run, it says so.

## 1. Executive summary

NEXUS is a single-file, append-only, crash-safe key-value store in Go with a
CRC32C-checked binary log, an in-memory hash index, fsync before every
acknowledgement, all-or-nothing batches, a size cap, crash-safe compaction and a
deterministic recovery procedure with documented rules. It ships a CLI
(`open`, `put`, `get`, `del`, `batch`, `dump`, `check [--no-repair]`, `serve`),
a fault-injection test suite for all 32 official scenarios, two independent
torture testers (`torture/` and the newer `torture2/` "Torture Engine 2.0"), a
cross-implementation verifier (`crosscheck/`), and a benchmark (`kvdb-bench`).

The storage engine is **complete and well evidenced**. The main risks before
judging are not in the engine: they are (1) the uncommitted state of most of
the recent work, (2) the unfinished "design-phase prompts" section of
PROMPTS.md, and (3) the fact that Torture Engine 2.0 and the benchmark were
written on the last day and have only one machine's evidence.

Frontend verdict: **READY WITH A SMALL ADAPTER** (section 11).

## 2. Component status

Percentages are an estimate of "what the requirement asks for that is
implemented **and** has evidence", computed as: implemented features ÷
required features, then reduced for each requirement that has no test or
evidence. They are judgement, not measurement; the explanation column says
what pulls each one below 100 %.

| Component | Code (non-test lines) | Tests | Status | % | Why not 100 % |
|---|---|---|---|---|---|
| Binary format (`internal/format`) | 1,079 | 42 test funcs, 1,360 lines | COMPLETE | 100 | — |
| In-memory index (`internal/index`) | 76 | 2 | COMPLETE | 100 | small by design; exercised by every storage test |
| Operations: Put / Get / Delete / Batch (`kvdb`, `internal/storage`) | 313 + 871 | 14 + 32 | COMPLETE | 100 | — |
| Recovery (`internal/recovery`) | 895 | 37 test funcs, 1,705 lines | COMPLETE | 95 | the rule for a damaged final header (treat as torn, C2/C3) is our assumption A7; no judge clarification received |
| Compaction | in `internal/storage` (`compact.go`) | determinism test, C1–C6, compaction torture | COMPLETE | 100 | — |
| maxBytes enforcement | `internal/admission` (99) + storage | 3 + E1–E5 + disk-full torture | COMPLETE | 90 | the cap is a per-open argument, not stored in the file (documented) |
| Concurrency | one `sync.RWMutex` per DB + process lock (`internal/locking`) | 10 + D1–D4 + every torture mode | COMPLETE | 90 | no race detector run (no cgo on this machine); correctness rests on the single-mutex design and the torture oracle |
| Platform (Windows moves, directory sync) | `internal/platform` 309 | 13 | COMPLETE | 85 | Windows has no directory fsync; documented limitation |
| CLI | `internal/cli` 963 | 15 | COMPLETE | 100 | — |
| serve protocol | `internal/cli/serve.go` | serve tests + every torture run | COMPLETE | 90 | keys/values with spaces or newlines cannot be sent (protocol is space-separated; documented) |
| Go scenario suite (`src/scenarios`) | 1,834 test lines | 32 scenarios + acceptance | COMPLETE | 100 | — |
| Torture tool v1 (`torture/`) | 1,445 | 4 | COMPLETE (superseded) | 100 | kept unchanged because `src/scenarios` uses it |
| Torture Engine 2.0 (`torture2/`) | ~5,850 | oracle 35 cases + brute-force/stress, proc 8, workload 2, engine 1, catalog 1, e2e 3 | IMPLEMENTED, NEEDS MORE VALIDATION | 85 | written today; one machine; real kills almost never tear a frame (torn tails are covered by replaying every byte prefix instead) |
| Independent verifier (`crosscheck/`) | 519 | 9 checks in the final run | COMPLETE | 90 | written by the same team, so "independent" means a separate decoder, not a separate author |
| Benchmark (`kvdb-bench`) | 473 | no unit test; smoke runs and the final run | IMPLEMENTED | 80 | one machine, Defender on, no comparison baseline (deliberately no superiority claims) |
| Documentation | README, ARCHITECTURE, DESIGN, RECOVERY, FAILURE_GUARANTEES, TEST_PLAN, TORTURE_* | — | COMPLETE | 90 | PROMPTS.md "Design-phase prompts" still `_TODO (team)_` |
| HTTP API / frontend | none | — | NOT IMPLEMENTED | 0 | not required by the core brief; spec in FRONTEND_INTEGRATION.md |
| Replication, SQL, auth, encryption | none | — | NOT IMPLEMENTED (out of scope by rule) | — | — |

## 3. Test evidence

### 3.1 What ran in the final validation

Command: `bash docs/results/final/run-final.sh`, logs in
`docs/results/final/`. Per-step exit codes are in `00-steps.log`; the summary
of numbers is in [results/final/README.md](results/final/README.md) and
[TORTURE_RESULTS.md](TORTURE_RESULTS.md).

Two kinds of evidence, kept apart:

**Current engine and current tester build** (22:15–23:13; engine source
unchanged since 19:06:59, tester built 22:15):

| Check | Exit | Result |
|---|---|---|
| Clean build (`go build -a`), gofmt, vet on 4 OSes, build incl. linux/arm64 | 0 | clean |
| `go test -count=1 ./...` | 0 | 18 packages ok |
| crosscheck (independent decoder) | 0 | 9 / 9 |
| Targeted compaction / size cap / read-only check / Windows moves | 0 | 375 subtests PASS |
| 1,000,000-key open | 0 | 474 ms |
| 32 scenarios, Go fault-injection suite, full mode | 0 | 32 / 32 PASS (918 s) |
| `nexus-torture run --seed 12345 --iterations 500` | 0 | PASS 500/500, 0 inconclusive, 148,100 acked writes verified after restart, 0 violations |
| `nexus-torture mutants` | 0 | 6 / 6 detected at iteration 1, control passed |
| Detection rate (20 iterations each) | 0 | 20/20 ×4; partial-batch 13/20; bad-recovery 5/7 valid (13 iterations invalid: an OS-level file stall on a mutant-only file) |
| Reproducibility (same seed twice, failure replay) | 0 | 0 mismatches in 14,883 requests; replay reproduced the failure |
| `nexus-torture scenarios` (quick) | 0 | 32 / 32 PASS, 1,385 kill points |
| `kvdb-bench` 100k × 3 | 0 | put 2,699/s, delete 2,668/s (synced), get 165,652/s, reopen 45 ms |

**Same engine, earlier runs** (still valid because the engine did not change):
Prompt 10 validation 19:11–19:58 (first-gen torture ×8, 1M PUT/DELETE
acceptance, scenarios full) and Torture 2.0 all 12 modes with the 21:30
tester build (all PASS).

**Stopped or invalid, not counted:** the 21:30 build's `scenarios --full`
(stopped by us; failures from C2 on are exit 0xC0000142 artifacts of the
stop); `bad-recovery` rate iterations 4–16; the batch10 run interrupted
when run-final.sh was stopped.

### 3.2 Real processes or mocks?

| Suite | Process | Kill |
|---|---|---|
| `src/scenarios` (32 scenarios) | mostly in-process with a fault-injecting file system; F1, F3 and several kill tests start real `kvdb` children | `TerminateProcess` for the child tests |
| `torture/` and `torture2/` | real `kvdb serve` child built from `./src/cmd/kvdb` (the official binary, unmodified) | real `os.Process.Kill` (`TerminateProcess`) at random times |
| `torture2` scenario catalog | real `kvdb` CLI on real files; torn-write states built by replaying every byte prefix of the real appended bytes through the real CLI | real kills where the scenario needs them |
| mutation testing | a **separate** binary `kvdb-mutant` that wraps the engine; the official binary is not modified | same as torture |
| `crosscheck/` | reads files written by the real engine with its own decoder | — |
| unit tests | in-process, temp directories, real files | — |

### 3.3 Is the verifier independent?

- The torture2 oracle (`torture2/oracle`) imports nothing from `src/`; it
  receives only the request and reply lines of the history and the final
  database contents read through the CLI. It checks every possible order of
  acknowledged, rejected and unknown operations (a bounded search). If the
  bound is reached it returns INCONCLUSIVE, which is counted separately and
  never as PASS.
- The oracle was itself tested: unit cases with known answers, a brute-force
  comparison against exhaustive enumeration on small histories, and the
  mutation run (six deliberately broken engines, each caught).
- `crosscheck/` decodes the file format independently of `internal/format`.

### 3.4 Reproducibility

Every torture run takes `--seed`; each iteration derives its own seed and the
summary prints a one-line repro command per failure. The final run includes a
reproducibility step (two runs with the same seed, request streams compared,
and a mutant failure replayed with `--iteration N --from-db`). Kill timing
depends on the OS scheduler, so the *workload* is reproducible exactly and the
*kill point* only approximately; the docs say this.

### 3.5 Never run, or only partly

- Race detector (`go test -race`): needs cgo, not available on this machine.
- Real power loss or a disk that lies about flushes: not testable here; we make
  no power-loss claim.
- Linux and macOS: only cross-compiled and `go vet`-ed, not executed.
- Long soak (hours): not run; the longest single torture run was 500
  iterations.

## 4. Torture tester assessment

Strengths: real binary, real kills, a sound checker that understands unknown
outcomes, batch atomicity checking, a minimizer, JSONL event logs, repro
commands, and mutation evidence that the checker is not rigged.

Weaknesses a judge can find:
1. Real kills on this machine almost never land between two writes of one
   frame, so "torn tails discarded" in torture runs is close to zero. Torn
   frames are proven by the byte-prefix replay and the fault-injection suite,
   not by kills. This is stated in BREAKING.md §3 and TORTURE_ARCHITECTURE.md.
2. Two mutants are not caught in every iteration: partial-batch 13/20 in
   the final run (19/20 in an earlier run of the same command), because the
   fault is only visible when the kill lands between the frames of an
   in-flight batch; bad-recovery 5 of 7 valid iterations (13 iterations were
   invalidated by an OS-level stall on a file only the mutant writes). Every
   mutant is still detected at iteration 1 by `nexus-torture mutants`.
   Reported as measured.
3. Written on the last day, one machine.
4. Two torture tools exist (`torture/`, `torture2/`). The README explains that
   v1 is kept for the Go scenario suite and that v2 is the one to demo.

## 5. Competition readiness

**Strongest points to show:** the recovery rules table (RECOVERY.md); the
failure-guarantees table with evidence links; the 32 scenarios; the torture
oracle catching six broken engines; `check --no-repair` showing what recovery
would do before it does it; 1,000,000-key recovery in about half a second.

**Ways to lose marks:**
1. Uncommitted work (most of Prompts 10–13 is not in git). Commit before
   judging.
2. PROMPTS.md "Design-phase prompts" is still a TODO; the rules ask for AI
   prompt logs.
3. Claims that are stronger than the evidence (we removed the ones we found;
   keep it that way: no "power-loss safe", no "faster than X").
4. Throughput (~2,600 synced puts/s) may look low next to unsynced databases;
   answer: every ACK is an fsync, there is no group commit, by design.

**Demo failure risks:** a long torture run during the demo (use
`--iterations 20`); Defender slowing the first build (build before the demo);
the CLI failing with `locked` while another process holds the database (show it
as a feature).

**Hard questions to prepare:**
- "Checking linearizability is NP-complete — how does your oracle finish?"
  Bounded search with a step and time budget; beyond it the verdict is
  INCONCLUSIVE and is not counted as PASS.
- "A bit flip in the last frame — torn write or corruption?" Indistinguishable
  at the tail; we treat a bad final frame as torn (rule C1/C2), a bad frame
  followed by valid frames as corruption (refuse to open). Assumption A7.
- "Windows has no directory fsync — is a rename durable?" We use
  `MoveFileEx(MOVEFILE_WRITE_THROUGH)`; documented as the best Windows offers.
- "Why hold the lock through fsync?" Simplicity and a total order; the cost is
  throughput, stated in ARCHITECTURE.md trade-offs.

**Fix before judging (small):** commit; fill the PROMPTS design-phase section;
make sure README numbers match `docs/results/final/`.

**Leave out of scope:** group commit, ordered index, persisted maxBytes,
replication, SQL, auth, encryption, a new format.

## 6. Frontend integration

See [FRONTEND_INTEGRATION.md](FRONTEND_INTEGRATION.md) for the full contract
(endpoints, request and response examples, errors, states, service layer).
Summary: no HTTP server or frontend exists; every number the five UI sections
need is available from the existing library, CLI, torture tool and benchmark;
a ~600-line standard-library adapter is enough, with no engine change.

## 7. Product evaluation

As a product NEXUS is an embeddable, single-process, single-file store with
strong durability semantics and unusually thorough failure evidence. Its limits
are honest and typical of the design: whole index in RAM, one writer at a time,
throughput bound by fsync latency, no ordered scans, no network server. For a
competition about crash safety this is the right trade.

## 8. Remaining limitations

Listed in README "Limitations" and ARCHITECTURE.md "Design trade-offs": RAM
index; global write lock held through fsync; no group commit; `Range` sorts all
keys; maxBytes not persisted; serve cannot carry spaces/newlines; no directory
fsync on Windows; no power-loss testing; no race detector run.

## 9. Issues needing manual attention

1. **Commit the working tree** (16 modified, many untracked files: `torture2/`,
   `crosscheck/`, `src/cmd/kvdb-bench/`, docs, results). Not done by us, per
   instructions.
2. **PROMPTS.md "Design-phase prompts" `_TODO (team)_`** — only the team can
   fill it.
3. **JUDGE_CLARIFICATIONS.md** has no official answers; assumptions A1–A7
   stand.
4. A "design extension" requirement was mentioned; no such text exists in the
   repository or the prompts. Confirm with the organisers what it refers to.

## 10. Practical next steps (in order, with remaining time in mind)

1. Commit (5 min).
2. PROMPTS design-phase section (15 min, team).
3. If a UI is wanted: build the adapter per FRONTEND_INTEGRATION.md, Database +
   Overview + Torture Lab first (about 3–4 h with tests).
4. Rehearse the judge demo from README with `--iterations 20`.

## 11. Verdict

**READY WITH A SMALL ADAPTER.** The engine and its evidence are ready for
judging now. A frontend needs only the HTTP adapter described in
FRONTEND_INTEGRATION.md; no new engine capability is required.
