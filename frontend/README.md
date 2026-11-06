# NEXUS dashboard

A local web interface for the NEXUS key-value database. Every operation goes
through `nexus-api` (`src/cmd/nexus-api`) to the real engine; nothing is
simulated in the browser. The full API is documented in
`docs/FRONTEND_INTEGRATION.md` section 0.

## Start it (Windows, Git Bash or PowerShell)

1. Build the Go tools into one folder (from the repository root):

   ```bash
   go build -o bin/ ./src/cmd/kvdb ./src/cmd/nexus-api ./src/cmd/kvdb-bench ./torture2/cmd/nexus-torture ./torture2/mutants/kvdb-mutant
   ```

2. Start the adapter (keeps the database in `nexus-data/`):

   ```bash
   bin/nexus-api --addr 127.0.0.1:8080 --db nexus-data/nexus.kv --work nexus-data/api-work --evidence docs/results/final --allow-origin http://localhost:3000
   ```

3. Start the dashboard:

   ```bash
   cd frontend && npm install && npm run dev
   ```

4. Open **http://localhost:3000**. The sidebar shows "Connected" when the
   adapter answers. `NEXT_PUBLIC_NEXUS_API_URL` (see `.env.example`) changes
   the adapter address.

A production build: `npm run build && npm run start`.

## Pages

| Page | What is real |
|---|---|
| Overview | status, keys, file size, database name, format version, last sequence, last successful operation, integrity, recovery and compaction status, charts of operations this adapter performed |
| Database Explorer | open/create databases, PUT (insert vs update), GET (NOTFOUND), DELETE, BATCH, DUMP with search, CHECK |
| Storage Engine | live map of the log file frame by frame, index → log pointers, write path driven by a real PUT, byte layout from the Go source |
| Recovery | the 8 recovery steps with what the last open reported; crash drill on a disposable database: crash a real writer, tear the tail, inspect, recover, check acknowledged writes |
| Torture Lab | runs of `nexus-torture` (workload, 32 scenarios, mutation testing, replay of failed iterations), live log, recorded results labelled RECORDED |
| Benchmark | recorded `bench.json` (historical, machine-dependent) and live `kvdb-bench` runs |
| Architecture | clickable components with source files; animated PUT/GET/DELETE/BATCH/recovery/compaction flows (static content) |
| Database Comparison | NEXUS vs Bitcask, Redis, LevelDB, RocksDB (descriptive; no benchmark against them was run) |
| Guided Demo | 10 steps with live widgets, Next/Previous or ← → keys |

"Offline sample" in the sidebar is for browsing the layout without a backend:
key operations there act on an in-browser sample store and every page shows a
SAMPLE DATA badge. The log map, database switching, recovery, torture,
benchmark runs and the Guided Demo's live steps are unavailable in that mode
and say so. Use "Live API" for anything you present as NEXUS behaviour.

## Mentor demo (about 10 minutes)

1. Start the adapter and dashboard as above; open **Guided Demo**.
2. Steps 1–2: introduce the two structures (log + index).
3. Step 3: PUT `demo:greeting`, GET it, GET a missing key (NOTFOUND).
4. Step 4: PUT again (updated), DELETE, DELETE again (absent: nothing written).
5. Step 5: send a PUT through the write path; the log strip grows by a frame.
6. Step 6: verify every live entry on disk.
7. Step 7: Reset the demo database, Crash the writer, optionally Tear the tail,
   then Recover: every acknowledged write is checked.
8. Step 8: run 5 crash iterations (confirm the dialog); show the recorded
   500-iteration result.
9. Step 9: recorded benchmark; optional quick live run.
10. Step 10: comparison; open the Database Comparison page for detail.

## Checks

```bash
npm run lint
```

```bash
npm run typecheck
```

```bash
npm run build
```

```bash
npm run test:api
```

`test:api` needs a running adapter; it writes only `itest:*` keys and removes
them again.

## Design notes

Monochrome palette (#111111 background, #151515 sidebar, #1B1B1B cards),
green and red only for pass/fail. Animations are CSS transitions
(tw-animate-css), not Framer Motion, to avoid an extra dependency.
