# Final command reference (verified)

Every command here was run on 2026-11-07 on the development laptop (Windows 11, Git Bash, Go 1.27.3), in a clean export of the working tree and in the repository. A command marked **(unverified)** was not run by the agent. Commands are for bash (Git Bash, Linux, macOS). For Windows `cmd`, write `kvdb.exe` / `torture.exe` and use `\` paths. PowerShell should accept `./kvdb` and `./torture` exactly as written, but that was **(unverified)**, because PowerShell hangs in the agent sandbox. Check it once yourself.

## 0–1 min: build (offline, no prebuilt binaries)

```bash
go build -o . ./src/cmd/kvdb ./src/cmd/torture
```

* Standard library only; no module downloads.
* The verification used empty `GOCACHE`/`GOMODCACHE` with `GOPROXY=off GOTOOLCHAIN=local`: it built in 8 s.
* Needs Go ≥ 1.22.
* Produces `kvdb` and `torture` (`kvdb.exe` and `torture.exe` on Windows).

Optional extras: `go build -o . ./src/cmd/kvdb-perf ./torture2/cmd/nexus-torture ./src/cmd/kvdb-bench`

## 1–4 min: torture, any seed

```bash
./torture --iterations 200 --seed 12345
```

* Ends `RESULT: PASS  passed=200 failed=0 of 200 planned …`, exit 0. It takes about 70 s here.
* Any int64 works: rehearsed with seeds 12345, 1, 987654321 and −42, all PASS 200/200.
* Non-integers (`12abc`, `1.5`, `0x10`) are refused with exit 2 before anything runs.
* Useful flags: `--quiet` (summary only), `--dir <folder>` (default `torture-runs/<mode>-seed<seed>`, recreated on each run), `--writers N`.
* On failure: exit 1, `FAIL at iteration N`, and the database is kept in `--dir`. Same seed, same requests and kill delays.

## 4–6 min: compaction and recovery-crash modes

```bash
./torture --iterations 200 --seed 12345 --mode compaction
./torture --iterations 200 --seed 12345 --mode recovery-crash
```

* Compaction mode took about 160 s for 200 iterations, and recovery-crash about the same (see CRASH_SCENARIO_RESULTS.md). Together that is longer than the 2-minute slot.
* To fit the slot, run the two in **two terminals at once**, or ask whether `--iterations 50` is acceptable.
* Other modes: `default, large-values, large-batch, compaction, recovery-crash, disk-full, concurrent-disjoint, same-key`.

## 6–8 min: manual serve, kill, restart, check, dump

Terminal 1:

```bash
./kvdb serve judge.db
```

Type:

```text
1 put apple red
2 put banana yellow
3 batch 3
put cherry dark-red
put date brown
del banana
4 get apple
```

Replies: `ACK 1`, `ACK 2`, `ACK 3`, `VALUE 4 red`.

Terminal 2 (hard kill):

```bash
taskkill //F //IM kvdb.exe        # Git Bash
```

```bat
taskkill /F /IM kvdb.exe          :: cmd / PowerShell
```

```bash
kill -9 $(pgrep -f "kvdb serve")   # Linux/macOS (unverified here)
```

Then:

```bash
./kvdb open judge.db      # keys=<n> recovery_ms=<ms> size=<bytes>
./kvdb check judge.db     # check: … then OK
./kvdb dump judge.db      # sorted key value lines, then "dump: N keys" (stderr)
```

Repeat 5–10 times. Every ACKed op must be in `dump`. An op without an ACK may or may not be there (unknown). This was rehearsed: 12 kills, 19,492 ACKs, 0 lost (MANUAL_KILL_REHEARSAL.md).

A second `serve` on the same file while the first is alive prints `ERROR (locked)`, which is expected.

## All CLI commands

| Command | Notes |
|---|---|
| `./kvdb open <db> [--max-bytes N]` | create or recover; prints `keys= recovery_ms= size=` |
| `./kvdb put <db> <k> <v>` | `OK` |
| `./kvdb get <db> <k>` | value or `NOTFOUND` |
| `./kvdb del <db> <k>` | deleting a missing key is not an error |
| `./kvdb batch <db> <file>` | one op per line, `put k v` / `del k`; atomic; at most 10,000 ops |
| `./kvdb dump <db>` | all keys sorted |
| `./kvdb check <db>` | recovers like open, then re-verifies everything |
| `./kvdb check <db> --no-repair` | never writes; `NEEDS REPAIR …` gives exit 4 |
| `./kvdb serve <db> [--max-bytes N]` | stdin protocol: `[<writer>] <id> put|del|get|batch <n>|compact` |
| `./kvdb xray <db> [--json] [--verbose]` | read-only diagnostics |

Exit codes: 0 ok · 1 failed (nothing changed) · 2 usage · 3 outcome unknown · 4 needs repair.

## Evidence commands (deductions A–C)

```bash
go build -o . ./src/cmd/kvdb-perf && ./kvdb-perf --only recovery   # A: 100k/1M/2M recovery timing
printf 'w1 c1 compact\n' | ./kvdb serve big.db                        # B: explicit compaction, then ls -l
./kvdb put cap.db k v --max-bytes 200000                              # C: ErrFull at the cap → "ERROR (full)", exit 1
```

Full procedures: ONE_MILLION_KEY_RECOVERY.md, COMPACTION_SHRINK_TEST.md, DISK_FULL_VALIDATION.md.

## Test suite

```bash
go vet ./...
go test ./...
```

## Dashboard (optional, needs the already-installed `frontend/node_modules`)

```bash
go build -o bin/ ./src/cmd/...
bin/nexus-api --db demo-run/nexus.kv
NEXT_TELEMETRY_DISABLED=1 npm --prefix frontend run dev     # http://localhost:3000
```

A clean clone has no `node_modules`, and `npm install` needs the network. The dashboard is not part of the judging schedule; demo it from the development laptop.
