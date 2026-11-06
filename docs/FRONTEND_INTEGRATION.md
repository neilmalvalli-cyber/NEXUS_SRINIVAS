# Frontend integration — readiness and API contract

Status: **assessment and specification only.** Nothing in this document is
implemented yet. No HTTP server exists in the repository today (there is no
`net/http` import anywhere), and there are no frontend files. The engine
(`src/kvdb`, `src/internal/...`) is not changed by anything proposed here.

Verdict: **READY WITH A SMALL ADAPTER.** Every number and action the five
planned UI sections need is already available from code that exists and is
tested: the `kvdb` Go library, the `kvdb` CLI (`open`, `check --no-repair`,
`dump`, `serve`), `nexus-torture` and `kvdb-bench`, plus the JSON files those
tools already write. What is missing is only a transport: a small HTTP process
that calls them. It is about 500–700 lines of standard-library Go and needs no
new dependency.

## 1. What exists today (inspected, not assumed)

| Capability | Where | How a UI can reach it |
|---|---|---|
| Open / Put / Get / Delete / Batch / Compact / Close | `src/kvdb/kvdb.go` | in-process library call |
| `Stats()` → `{Keys, LiveBytes, Size, MaxBytes}` | `src/kvdb/inspect.go` | in-process |
| `Recovery()` → `{Rules, Created, Tail, DiscardedBytes, Frames, Warnings}` (what the last open did; **no duration field**) | `src/kvdb/inspect.go` | in-process |
| `Range(fn)` — every live pair in key order (sorts all keys; holds the lock) | `src/kvdb/inspect.go` | in-process |
| `Verify()` → `{Keys, Frames, Size, LiveBytes}` — re-reads and checks the whole file | `src/kvdb/inspect.go` | in-process |
| `CheckReadOnly(path)` → keys, frames, size, end, rules, tail, `Repairs[]`, `NeedsRepair()` — never modifies the file | `src/kvdb/inspect.go` | in-process, or `kvdb check --no-repair` (exit 4 = needs repair) |
| Recovery timing: `kvdb open` prints `keys=N recovery_ms=X size=B` | `src/internal/cli/cli.go` (`cmdOpen`) | child process |
| `kvdb serve`: line protocol on stdin/stdout — `put`, `del`/`delete`, `get`, `batch n`, `compact`; replies `ACK`, `VALUE`, `NOTFOUND`, `ERR id class msg` | `src/internal/cli/serve.go` | child process (used for crash demos) |
| Error classes: `invalid`, `too-large`, `full`, `locked`, `closed`, `failed`, `corruption`, `bad-format`, `notfound`, `unknown`, `io` | `cli.ErrorClass` | mapped to HTTP (section 4) |
| Torture runs: `nexus-torture run --mode M --seed S --iterations N --dir D` writes `results.jsonl` (one line per finished iteration), `events.jsonl`, `summary.json`; failures add `history.json`, `minimized.json`, `failure.txt` and a repro command | `torture2/engine` | child process + reading files |
| `nexus-torture modes`, `scenarios [--only/--group/--full]` (writes `scenarios.json`, `SCENARIOS.md`), `mutants` | `torture2/cmd/nexus-torture` | child process + files |
| Benchmark: `kvdb-bench --json F --md F` (machine info, median of runs, p50/p99/max) | `src/cmd/kvdb-bench` | read the last JSON, or run as a child |
| Recorded evidence | `docs/results/final/` (logs, `bench.json`, torture summaries, scenarios) | read-only file serving |

**Not available today** (do not show these in the UI unless the adapter
creates them): live operations-per-second of the engine, latency histograms of
live traffic, cache or memory statistics of the engine, a history of past
recoveries, a key count per prefix without a full scan, replication or
cluster status (none exists), user accounts (none exist).

## 2. Architecture of the adapter (proposed)

```
browser UI ──HTTP/JSON──> nexus-api (new, src/cmd/nexus-api, net/http only)
                              │
                              ├── in-process *kvdb.DB  (main database: Database + Overview)
                              ├── child: kvdb serve / kvdb open / kvdb check --no-repair
                              │          on a separate demo file (Recovery section)
                              ├── child: nexus-torture run|scenarios|mutants (Torture Lab)
                              └── read-only: docs/results/final/*, bench.json (Architecture / evidence)
```

Rules the adapter must follow:

1. **One owner per database file.** The adapter holds the main database open
   (process lock `<path>.lock`). While it runs, `kvdb` CLI commands on the same
   file fail with `locked` — that is correct behaviour, and the UI should say
   so rather than hide it.
2. **Crash demos never touch the main database.** They use their own file
   (for example `demo/recovery.kv`) driven by real child processes, so a kill is
   a real `TerminateProcess`, not a simulation.
3. **One torture run at a time** (they are CPU- and disk-heavy). A second
   request returns 409.
4. **Every number shown comes from a call or a file.** Each response carries
   `source` (which call or file) and `measured_at`. If a value is unavailable
   the field is `null`, never a guess.
5. Listen on `127.0.0.1` only by default. There is no authentication; this is a
   local demo tool, and the docs must say so.

## 3. Endpoints

All bodies are JSON (`Content-Type: application/json`). Keys and values are
byte strings in the engine; the API carries them as UTF-8 strings by default,
or base64 when `"encoding":"base64"` is given (a key or value that is not valid
UTF-8 is returned with `"encoding":"base64"`).

### 3.1 Health and overview

`GET /api/health` → 200

```json
{"status":"ok","db_path":"data/main.kv","db_open":true,"version":"phase1-format@8058c2d+wip",
 "torture_running":false}
```

If the database could not be opened: 503 with the error envelope; the UI shows
the disconnected state.

`GET /api/overview` → 200 — every field from `Stats()`, `Recovery()` and the
adapter's own timing of `Open`:

```json
{
  "stats": {"keys": 1204, "live_bytes": 88231, "size": 131072, "max_bytes": 0},
  "last_open": {
    "duration_ms": 3.41,
    "created": false,
    "rules": ["A1"],
    "tail_rule": "",
    "discarded_bytes": 0,
    "frames": 1530,
    "warnings": []
  },
  "limits": {"max_key_bytes": 1024, "max_value_bytes": 1048576, "max_batch_ops": 10000},
  "adapter": {"started_at": "2026-11-07T09:00:00Z", "requests": 52, "write_errors": {"full": 0}},
  "source": "kvdb.Stats, kvdb.Recovery, adapter timer around kvdb.Open",
  "measured_at": "2026-11-07T09:05:12Z"
}
```

`duration_ms` is measured by the adapter around `kvdb.Open`, exactly as
`kvdb open` does; it is a real measurement of that one open, not a benchmark.
`adapter.requests` counts requests to this adapter only — label it that way.

### 3.2 Database

| Method | Path | Engine call | Success |
|---|---|---|---|
| GET | `/api/keys?prefix=&after=&limit=100` | `Range` (filter + page in the adapter) | 200 `{"items":[{"key","value_size","value_preview"}],"next_after":"k123"\|null,"scanned":1204}` |
| GET | `/api/keys/{key}` | `Get` | 200 `{"key","value","encoding","size"}`; 404 if absent |
| PUT | `/api/keys/{key}` body `{"value":"...","encoding":"utf8"}` | `Put` | 200 `{"ok":true,"durable":true,"elapsed_ms":0.41}` |
| DELETE | `/api/keys/{key}` | `Delete` | 200 `{"ok":true,"durable":true,"elapsed_ms":0.39}` (deleting a missing key succeeds, as in the engine) |
| POST | `/api/batch` body `{"ops":[{"op":"put","key":"a","value":"1"},{"op":"del","key":"b"}]}` | `Batch` | 200 `{"ok":true,"ops":2,"elapsed_ms":1.1}` — all or nothing |
| POST | `/api/compact` | `Stats` → `Compact` → `Stats` | 200 `{"size_before":131072,"size_after":40960,"keys":1204,"elapsed_ms":11.2}` |
| POST | `/api/verify` | `Verify` | 200 `{"keys","frames","size","live_bytes","elapsed_ms"}`; 500 class `corruption` if the check fails |

Notes:
- `durable: true` is honest: the engine returns only after `FlushFileBuffers`
  / `fsync` of the frame.
- `Range` sorts every key and holds the database lock. At 1,000,000 keys one
  page costs a full scan (hundreds of ms). Keep `limit` ≤ 500 and show
  "scanning…" while waiting. A faster ordered index is an engine change and is
  out of scope (section 6).
- `elapsed_ms` is measured by the adapter around the single call.

### 3.3 Recovery (real processes, demo database only)

| Method | Path | What it runs | Response |
|---|---|---|---|
| POST | `/api/recovery/demo/reset` | delete and recreate `demo/recovery.kv` with N keys (`{"keys":1000}`) through `kvdb serve` | `{"keys":1000,"size":...}` |
| POST | `/api/recovery/demo/crash` body `{"writes":500,"kill_after_ms":40}` | start `kvdb serve demo.kv`, stream puts, kill the process after the delay | `{"sent":500,"acked":212,"unknown":3,"killed_at_ms":40,"acked_keys_sample":[...]}` |
| POST | `/api/recovery/demo/inspect` | `kvdb check --no-repair demo.kv` (or `CheckReadOnly`) | `{"needs_repair":true,"tail_rule":"C1","repairs":["truncate a torn 37-byte tail..."],"keys":1212,"frames":...}` |
| POST | `/api/recovery/demo/tear` body `{"bytes":7}` | truncate the last N bytes of the file (scenario F4 style) | `{"size_before","size_after"}` |
| POST | `/api/recovery/demo/recover` | `kvdb open demo.kv`, parse `keys= recovery_ms= size=` and stderr `recovery:` lines; then check every acked key from the last crash with `kvdb get` (or one `dump`) | `{"keys":1212,"recovery_ms":4.2,"size":...,"actions":["discarded a torn 37-byte tail (rule C1)"],"acked_checked":212,"acked_missing":0}` |

The UI's verdict line on this page ("212 acknowledged writes, 0 missing") is
computed from real `ACK` lines and a real read-back after a real kill, so it is
not a mock. If `acked_missing > 0` the page must show a failure in red; never
hide it.

### 3.4 Torture Lab

| Method | Path | What it runs | Response |
|---|---|---|---|
| GET | `/api/torture/modes` | `nexus-torture modes` | `{"modes":[{"name":"default","description":"..."}]}` |
| POST | `/api/torture/runs` body `{"mode":"default","seed":"12345","iterations":50}` | `nexus-torture run ... --dir torture-runs/api/<id> --quiet` | 202 `{"id":"r-20261107-0901","state":"running"}`; 409 if one is running |
| GET | `/api/torture/runs/{id}` | count lines of `results.jsonl`; read `summary.json` when it exists | see below |
| GET | `/api/torture/runs/{id}/iterations?from=0` | lines of `results.jsonl` | `{"items":[IterResult...]}` |
| POST | `/api/torture/runs/{id}/cancel` | kill the child process tree | `{"state":"cancelled"}` |
| GET | `/api/torture/scenarios/latest` | read the newest `scenarios.json` | the file, unchanged |
| POST | `/api/torture/scenarios` body `{"only":["A1","F4"]}` | `nexus-torture scenarios --only ...` | 202 like runs |
| GET | `/api/torture/mutants/latest` | newest mutation report | the file |

Progress response while running:

```json
{
  "id": "r-20261107-0901", "state": "running",
  "mode": "default", "seed": "12345", "iterations": 50,
  "completed": 17, "pass": 17, "fail": 0, "inconclusive": 0,
  "last": {"iter": 17, "verdict": "PASS", "acked_writes": 281, "unknown": 2, "kill_point": "batch in flight"},
  "elapsed_s": 6.8,
  "summary": null
}
```

`state` is one of `running`, `passed`, `failed`, `inconclusive`, `cancelled`,
`error`. When the child exits, `summary` is `summary.json` verbatim (it
contains the `Verification` counters: attempted, acknowledged, cleanly
rejected, unknown, acknowledged writes verified after restart, batch atomicity
violations, data integrity violations, recovery failures, and a repro command
per failure). **INCONCLUSIVE must be shown as its own colour, never as PASS.**

### 3.5 Architecture and evidence (read-only)

| Method | Path | Source |
|---|---|---|
| GET | `/api/benchmarks/latest` | `docs/results/final/bench.json` (includes machine info) |
| POST | `/api/benchmarks/run` body `{"n":10000,"runs":1}` | `kvdb-bench --json` child, on a scratch directory; 202 + polling like torture |
| GET | `/api/evidence` | list of `docs/results/final/*` with size and modification time; each file downloadable at `/api/evidence/{name}` |

The Architecture page itself is static text and diagrams taken from
`ARCHITECTURE.md` and `docs/FAILURE_GUARANTEES.md`; it needs no API beyond
links to evidence.

## 4. Errors

One envelope for every error:

```json
{"error": {"class": "full", "message": "write would exceed max-bytes 262144", "outcome_unknown": false}}
```

| Engine class | HTTP | UI meaning |
|---|---|---|
| `invalid` | 400 | empty key, key > 1024 B, malformed batch (show the message) |
| `too-large` | 413 | value > 1 MiB or batch > 10,000 ops |
| `notfound` | 404 | key absent (GET only) |
| `locked` | 409 | another process holds the database |
| `full` | 507 | the size cap rejected the write; nothing was written |
| `closed` | 503 | handle closed (adapter shutting down) |
| `failed` | 500 | write failed and was rolled back; nothing was written |
| `corruption`, `bad-format` | 500 | file is not a valid NEXUS file or a mid-file frame is corrupt; the engine refuses to guess |
| `unknown`, `io` | 500 with `"outcome_unknown": true` | the write may or may not be durable; the UI must say "outcome unknown — re-read the key", never "failed" |
| adapter: torture already running | 409 class `busy` | |
| adapter: child process failed to start | 500 class `adapter` | |

## 5. UI states (service layer)

The UI talks only to a `NexusService` interface with two implementations:

```ts
interface NexusService {
  health(): Promise<Health>;
  overview(): Promise<Overview>;
  listKeys(q: {prefix?: string; after?: string; limit?: number}): Promise<KeyPage>;
  getKey(k: string): Promise<KeyValue>;
  putKey(k: string, v: string): Promise<WriteResult>;
  deleteKey(k: string): Promise<WriteResult>;
  batch(ops: BatchOp[]): Promise<WriteResult>;
  compact(): Promise<CompactResult>;
  verify(): Promise<VerifyResult>;
  recoveryDemo: {reset; crash; inspect; tear; recover};
  torture: {modes; start; status; iterations; cancel; latestScenarios; latestMutants};
  benchmarks: {latest; run};
  evidence(): Promise<EvidenceFile[]>;
}
```

- `MockNexusService` returns fixtures that are copies of **real recorded
  files** (`bench.json`, a `summary.json`, `scenarios.json`), each marked
  `"mock": true`. The UI shows a visible "DEMO DATA" badge whenever any
  response has `mock: true`.
- `HttpNexusService` calls the endpoints above. A build flag or `?backend=`
  switches between them.

Required states for every panel:

| State | Trigger | Display |
|---|---|---|
| loading | request in flight | skeleton or spinner; keep the last data greyed |
| disconnected | network error or `/api/health` fails | banner "backend not reachable"; retry every 5 s with backoff; disable write buttons |
| locked | 409 `locked` | "database is held by another process" |
| in progress | compaction, verify, torture, crash demo, benchmark | disable the button that started it; show elapsed time; torture shows `completed / iterations` |
| outcome unknown | `outcome_unknown: true` | amber, with a "re-read" button |
| empty | 0 keys | "empty database" plus a link to add a key |
| stale | data older than the refresh period | timestamp from `measured_at` |

Refresh: overview every 2 s while the tab is visible; torture status every
1 s while running; nothing else polls. Writes refresh the overview once.

## 6. Classification of what the UI wants

| UI item | Exists | Needs adapter only | Needs new engine capability |
|---|---|---|---|
| key count, live bytes, file size, size cap | ✓ (`Stats`) | HTTP | — |
| last recovery actions (rules, torn tail, discarded bytes) | ✓ (`Recovery`) | HTTP | — |
| recovery time | ✓ (`kvdb open` prints it) | timer around `Open`, or parse the CLI line | a duration field in `RecoveryInfo` (optional; not needed) |
| browse keys | ✓ (`Range`) | prefix filter and paging | ordered index for fast paging at 1M keys (out of scope) |
| get / put / delete / batch / compact / verify | ✓ | HTTP | — |
| crash and recover demo | ✓ (`serve`, `open`, `check --no-repair`) | process control | — |
| torture run with live progress | ✓ (files written per iteration) | process control + file tail | — |
| scenarios and mutation results | ✓ (`scenarios.json`, mutants report) | read files | — |
| benchmark numbers | ✓ (`bench.json`) | read file / run child | — |
| live throughput chart of real traffic | — | adapter can count its own requests and time them (label "through this adapter") | engine-wide counters (not needed) |
| size cap persisted across restarts | — | adapter passes the same `max_bytes` on every open | persisting maxBytes in the file (format change — do not do) |
| keys with spaces or newlines through `serve` | — | use the library in-process for the Database page | — |

## 7. Estimated effort

| Piece | Lines (estimate) | Time |
|---|---|---|
| `nexus-api`: router, JSON envelope, Database + Overview handlers | ~250 | 1.5 h |
| Recovery demo handlers (child processes) | ~150 | 1 h |
| Torture Lab handlers (child, progress, cancel) | ~150 | 1 h |
| Evidence / benchmark readers | ~60 | 20 min |
| Handler tests with `httptest` (one per endpoint, real temp DB) | ~200 | 1 h |

None of it modifies `src/kvdb` or `src/internal`. If time is short, build the
Database + Overview + Torture Lab endpoints first; the Recovery page can call
the same `nexus-torture scenarios --only F4,A1` that already exists.
