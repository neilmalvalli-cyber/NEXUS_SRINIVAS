# X-RAY, Power-Cut and performance HTTP API (nexus-api)

Targets are IDs, never paths: `main`, `demo`, `lab:<case>` (`^lab:[a-z][a-z-]{1,40}$`).
Report IDs: `^x-[0-9]{8}-[0-9]{6}-[0-9]{3}$`. Unknown JSON fields → 400. Errors use
`{"error":{"class","message","outcome_unknown"}}`.

| Method & path | Does |
|---|---|
| GET `/api/xray/targets` | targets with `kind` (live/demo/lab), `repair_allowed`, lab expectations |
| POST `/api/xray/scan {target, no_hash}` | read-only scan; `main` is scanned under the database read lock with adapter writes paused. Returns `{id, target, report}` |
| POST `/api/xray/repair {target, confirm:true}` | demo/lab only (main → 403; without confirm → 400): backup, open, verify, rescan |
| GET `/api/xray/reports?kind=` | summaries of scans run by this process (also saved under `<work>/xray/reports`, last 50 kept) |
| GET `/api/xray/reports/{id}`, `/{id}/export` | full report; export as an attachment |
| GET `/api/xray/summary` | latest live scan (null until one is run), recent findings with source kind, operational findings |
| GET `/api/xray/lab`, POST `/api/xray/lab/reset`, POST `/api/xray/lab/scan-all` | disposable lab under `<work>/xray/lab`; scan-all reports `matched` per case (409 before the lab is built) |
| GET `/api/powercut` | model, not-modeled list, disclaimer, limits, runs, recorded default run |
| POST `/api/powercut/runs {seed 0-999999999999, writers 1-8, ops_per_writer 1-500, max_crash_points 1-2000, max_variants_per_point 1-16, no_controls}` | starts nexus-powercut (503 if missing, 409 if a run is active); status via `/api/torture/runs/{id}` |
| GET `/api/powercut/runs/{id}/images/{image}/xray` | X-RAY report of a kept failed image |
| POST `/api/perf/runs {only[], quick}` | kvdb-perf; `only` or `quick` is required |
| GET `/api/perf/history` | recorded optimization history (`kind: recorded`) |

Long tasks reuse the torture run manager: argument vectors only (no shell),
one child at a time, cancellable. Tests: `src/cmd/nexus-api/xray_test.go`,
`frontend/scripts/api-integration.mjs` (25 checks).
