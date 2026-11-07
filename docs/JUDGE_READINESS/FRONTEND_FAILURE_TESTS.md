# Frontend failure tests (Phase 5)

All tests use the live dashboard (Next.js dev server on `localhost:3000`) against the real nexus-api (`127.0.0.1:8080`), on the demo database in the scratch directory, never the user's data. "Real" means real processes and a real browser. "Simulated" means the network fault was injected.

| ID | Judge action | Expected | Result | How |
|---|---|---|---|---|
| F1 / N1 | API killed, then judge clicks PUT | no success shown; nothing written | **PASS (real, 2026-11-07)** | The adapter process was stopped and the form was already filled. Within ~1 s the UI showed the banner "Backend disconnected. Cannot reach nexus-api… actions are disabled until it answers", the PUT button greyed out, and the sidebar badge read "Disconnected". The click sent no request; afterwards `judge:n1` was NOTFOUND. |
| F1b / N1 race | click PUT in the moment before the health poll notices | outcome unknown, never "failed" or success | **PASS (simulated)** | Not reproducible by hand: the UI detected the outage first. The same path, a write to a dead port, is the `connection refused` case of `write-outcome-test.mjs`, which reports outcome unknown. |
| F2 / N2 | answer lost after the server committed | "outcome unknown", read the key back | **PASS (real server, simulated loss); bug fixed** | NETWORK_FAILURE_TESTS.md: the card read "PUT outcome unknown", with one PUT sent and the value durable. Before the fix it read "PUT failed". |
| F3 / N3 | client timeout on a write | outcome unknown | **PASS (simulated)** | harness hang case |
| F4 / N4 | retry after an uncertain write | no automatic retry | **PASS** | `retry: false` on mutations; exactly one request per write in every test |
| F5 / N5 | API restarted with the dashboard open | reconnects by itself | **PASS (real, 2026-11-07)** | After the adapter was restarted, within ~7 s and with no page reload, the banner disappeared, PUT was enabled again and the badge read "Connected". |
| F6 / N6 | dashboard dev server restarted, API up | pages load, live data | **PASS (real, 2026-11-07)** | HTTP 000 while it was stopped; after restart, `offline-check.mjs` 14/14 and Backend Connected (n6-frontend-restarted.json) |
| F7 / N7 / N8 | adapter disabled, no internet | everything works on loopback | **PASS (real, 2026-11-07)** | Airplane mode, 16/16 with the state verified; the browser step is user-reported |
| F8 | response that does not match the schema | "Unexpected response", never guessed; outcome unknown for writes | **PASS (simulated)** | `write-outcome-test.mjs` (HTTP 200 with a non-JSON body); zod schemas in `lib/api/schemas.ts` |
| F9 | no external resources | only localhost hosts | **PASS (real)** | 11 pages, 323 resources, 0 external |
| F10 | X-RAY lab and the P7 case in the UI | lab diagnoses match; mid-log header damage is refused | **PASS (real)** | `api-integration.mjs` 25/25 after the P7 fix (lab scan-all matches every expected code) |

Not covered:
* browsers other than the built-in Chromium pane;
* screen readers;
* very slow networks, with no throttling test beyond the timeout case.

Reproduce:

```bash
npm --prefix frontend run test:write-outcome
```

```bash
npm --prefix frontend run test:api
```

```bash
node frontend/scripts/offline-check.mjs --label check
```
