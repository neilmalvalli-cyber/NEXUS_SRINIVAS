# nexus-torture — command-line usage

Build (repository root, Go toolchain only):

```bash
go build -o . ./src/cmd/kvdb ./torture2/cmd/nexus-torture ./torture2/mutants/kvdb-mutant
```

This produces `kvdb.exe`, `nexus-torture.exe` and `kvdb-mutant.exe` (no
`.exe` suffix on other systems). By default `nexus-torture` uses the `kvdb`
next to it; `--kvdb PATH` chooses another one. If the executable is missing
or does not behave like `kvdb` (no usage text, wrong exit code), the command
stops with exit 2 before running anything.

Exit status of every command: **0** PASS, **1** FAIL, **2** usage or tool
error (not a verdict on NEXUS), **3** INCONCLUSIVE (the oracle hit a
resource limit; never counted as a pass).

## `run`: randomized kill / recover / verify

```powershell
.\nexus-torture.exe run --seed 12345 --iterations 500
.\nexus-torture.exe run --seed 2026 --iterations 100 --mode large-batch
```

| Flag | Meaning |
|---|---|
| `--seed N` | **mandatory**. The same seed and iteration give the same requests, values and kill time |
| `--mode M` | workload mode (`nexus-torture modes`); default `default` |
| `--iterations N` | number of iterations (default 100) |
| `--iteration I` | run only iteration I (to reproduce a failure) |
| `--from-db FILE` | start from this database (the `pre` copy saved with a failure) |
| `--keep-going` | continue after a failing iteration (default: stop at the first) |
| `--reply-timeout D` | longest wait for one reply while `serve` runs (default `60s`) |
| `--oracle-steps N` | oracle search budget per iteration (default 2e9) |
| `--dir DIR` | work directory (default `torture-runs/v2/<mode>-seed<N>`). A non-empty directory not created by nexus-torture is refused |
| `--quiet` | print only failures and the summary |

Output: one line per iteration (verdict, kill time, what was in flight,
sent/acked/rejected/unknown counts, restart, recovery and oracle times), then:

```
RESULT: PASS  mode=default seed=12345  passed=500 failed=0 inconclusive=0 of 500  acked=… rejected=… unknown=… gets=…  kills-mid-write=… …
VERIFY: attempted=… acknowledged=… (writes …) cleanly-rejected=… unknown=…  acked-writes-verified-after-restart=… reads-verified=…  batch-atomicity-violations=0 data-integrity-violations=0 recovery-failures=0 protocol/liveness-failures=0 oracle-inconclusive=0
PERF: …s total, … iterations/s, restart … ms avg / … max, recovery … ms avg / … max, oracle … ms avg / … max, verify … ms avg, serve peak … MiB, tester heap peak … MiB
report: torture-runs\v2\default-seed12345\summary.json
```

The `VERIFY` line (also `verification` in `summary.json`) is the
correctness evidence of the run:

| Counter | Meaning |
|---|---|
| `attempted` | requests written to `kvdb serve` (writes, gets, compactions) |
| `acknowledged` | requests answered with a complete `ACK` / `VALUE` / `NOTFOUND` line; `writes` = acknowledged put/del/batch |
| `cleanly-rejected` | requests answered with a definite `ERR` class (nothing changed), e.g. `full` |
| `unknown` | no reply before the kill, or `ERR unknown`/`io`: may or may not have happened, never half |
| `acked-writes-verified-after-restart` | acknowledged writes in iterations whose recovered state the oracle accepted |
| `reads-verified` | acknowledged gets checked by the oracle in those iterations |
| `batch-atomicity-violations` | iterations where the oracle found a partially visible batch |
| `data-integrity-violations` | every other oracle violation (acknowledged write lost, value never written, rejected write visible, untouched key changed, invalid read, no legal order) |
| `recovery-failures` | `check --no-repair` changed a file or gave a wrong answer, or `open`/`check`/`dump` failed |
| `protocol/liveness-failures` | bad or stray `serve` output, missing replies, hangs |
| `oracle-inconclusive` | the oracle hit a resource limit; never counted as a pass |

Every failure is listed after it as `failure: iteration N (iteration seed S)
<kind>; reproduce: <command>`. With `--keep-going`, the next iteration starts
from the database as recovered after a failure, so one bug is not counted
again as a cascade of false failures.

Files in the work directory:

| File | Contents |
|---|---|
| `events.jsonl` | every send (with its effects), reply, kill, recovery and verdict, in order |
| `results.jsonl` | one JSON record per iteration |
| `summary.json` | totals, performance, first failure |
| `failures/iter-N/` | kept on FAIL: `failure.txt`, `history.json` (full history), `minimized.json` (smallest failing history found), `pre/` (database before the iteration), `after-kill/` (files right after the kill) |

On a failure the terminal shows the violation, the expected and found
values, the explanation, the evidence directory and a ready-to-paste repro
command:

```
reproduce: nexus-torture run --mode default --seed 777 --iteration 3 --from-db "…\failures\iter-3\pre\db.kv" --kvdb "…\kvdb.exe"
```

## `scenarios`: the 32 official crash scenarios

```powershell
.\nexus-torture.exe scenarios                 # all 32, quick sizes (about 6-7 minutes)
.\nexus-torture.exe scenarios --full          # official sizes: 1,000,000 keys for B6/C1/C2/F3, 500 kills for F1
.\nexus-torture.exe scenarios --group B
.\nexus-torture.exe scenarios --only A2,B6,F4
```

| Flag | Meaning |
|---|---|
| `--group X` | only group A–F |
| `--only IDS` | comma-separated scenario IDs |
| `--full` | official sizes and more iterations |
| `--seed N` | seed for the randomized parts (default 20261106) |
| `--dir DIR` | work directory (default `torture-runs/v2/scenarios`) |

Output: one line per scenario (`[B6] PASS 28.0s kill points 15 …`), failure
details under a failing scenario, then `SCENARIOS RESULT: PASS|FAIL …`.
Files: `scenarios.jsonl` (one record per scenario), `scenarios.json` (the
whole run, with every kill point and its outcome) and `SCENARIOS.md`
(a readable table).

## `mutants`: prove the tester catches bugs

```powershell
.\nexus-torture.exe mutants --mutant .\kvdb-mutant.exe
.\nexus-torture.exe mutants --mutant .\kvdb-mutant.exe --only partial-batch,corrupt-value
```

Runs the engine against `kvdb-mutant` once per mutation (up to
`--iterations`, default 40, stopping at the first detection), plus a control
build with no mutation that must pass. Ends with `MUTATION RESULT: every
mutation detected, control passed` (exit 0) or `NOT all as expected`
(exit 1). Report: `mutation-report.json`. A single mutation can also be
driven by `run`:

```powershell
$env:KVDB_MUTANT="drop-acked"; .\nexus-torture.exe run --seed 1 --iterations 5 --kvdb .\kvdb-mutant.exe; Remove-Item Env:KVDB_MUTANT
```

## `modes`

Lists the workload modes with a one-line description each.
