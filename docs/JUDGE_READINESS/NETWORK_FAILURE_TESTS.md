# Network failure tests (judge attacks N2, N3, N4)

Status: N2, N3, N4, N7 and N8 done (2026-11-07). The network was toggled by the user (Airplane mode), and the script verified the offline state itself.

## The bug found (N2/N3)

`frontend/src/lib/api/client.ts` only marked a write as *outcome unknown* when nexus-api itself said so (`outcome_unknown: true` in its error body). Several cases came back as plain **"PUT failed"**, even though the engine may already have made the write durable:
* a client timeout;
* a dropped connection;
* a response cut off mid-body;
* an unreadable response;
* a bare 5xx.

That is a false statement to the user. nexus-api answers only after the fsync, so a lost answer can hide a committed write.

## The fix

* `request()` takes `write: true` for the three data-changing calls: PUT key, DELETE key and BATCH (`service.ts`).
* For those calls, each of the following ends as `outcomeUnknown = true`:
  * a timeout;
  * a network error;
  * a connection closed while reading the response;
  * a non-JSON 2xx or 5xx response;
  * a bare 5xx;
  * a 2xx whose body fails the schema.
* "Connection refused" is included. A browser's `fetch` cannot tell it apart from a reset after the request was received, so it is classified conservatively.
* Definite answers stay plain failures: an engine error envelope such as `invalid`, `full` or `locked`, and a bare 4xx.
* Reads are never marked unknown.
* The UI already had an outcome-unknown presentation: the warning toast in `lib/mutations.ts` and the card in `key-ops.tsx`. The guided-demo write-path panel (`write-path.tsx`) used to say "Not acknowledged… may be absent". It now says the outcome is unknown, that the write was not retried, and that the user should read the key back.

## N4: retry after an uncertain write

* **No automatic retry of writes.** TanStack mutations use `retry: false` (`providers.tsx`), and `request()` itself never retries. The harness confirms that the server saw exactly **one** request in every case, and the browser test confirms exactly one PUT.
* **Manual retry by the user is safe to repeat.** The engine's operations are absolute (PUT key=value, DELETE key, and BATCH of those), with no increment or append operation.
  * Repeating a PUT of the same value, or a DELETE, leaves the same final state. A repeated DELETE may report NOTFOUND, which the UI shows as "key not found".
  * Repeating a BATCH re-applies the same absolute operations, so the result is the same, unless another writer changed one of those keys in between. In that case the repeat overwrites that change, as any later write would.
* No retry logic was added, as Prompt 20 requires for non-idempotent operations.

## Evidence

### 1. Client harness (simulated faults, real client code)

```bash
cd frontend && node --no-warnings scripts/write-outcome-test.mjs --json ../docs/JUDGE_READINESS/write-outcome-results.json
```

* It runs the unmodified `src/lib/api/client.ts` under Node 24's type stripping, against a local fault server (not nexus-api).
* Result: **13 passed, 0 failed**, plus the envelope check. Machine-readable results are in [write-outcome-results.json](write-outcome-results.json).

| Case | Expected | Result |
|---|---|---|
| PUT / DELETE / POST committed, then connection reset | network, unknown | PASS |
| PUT committed, response cut mid-body | unknown | PASS |
| PUT committed, server never answers (N3 timeout) | timeout, unknown | PASS |
| bare 502 | unknown | PASS |
| HTTP 200 with a non-JSON body | unknown | PASS |
| connection refused | unknown (conservative) | PASS |
| 400 `invalid` envelope | plain failure | PASS |
| bare 404 | plain failure | PASS |
| 500 envelope with `outcome_unknown: true` | unknown | PASS |
| GET reset or timeout | read failure, not unknown | PASS |

**Negative control.** The same harness run against the committed pre-fix `client.ts` gave **8 failed, 5 passed**: every uncertain-write case was reported as a plain failure. So the harness detects the bug.

### 2. Real dashboard and real nexus-api (answer loss simulated in the browser)

* Database Explorer, `localhost:3000/database`, live API at 127.0.0.1:8080, using the demo database in the scratch directory (not the user's data).
* `window.fetch` was patched for the test. It forwarded the PUT to the real nexus-api, waited for the real answer (HTTP 200, write committed and fsynced), then threw `TypeError: Failed to fetch`, just as a dropped connection does.

Result:
* The card said **"PUT outcome unknown… The write may or may not be durable. Read the key back to find out."**, and a matching warning toast appeared.
* Exactly 1 PUT was sent.
* Reading the key back returned the committed value `committed-but-answer-lost`.

This shows the bug's real consequence: the old code would have said "PUT failed" for a write that was durable. Afterwards the test key was deleted.

## What is simulated

* The network faults are simulated, either in the fault server or by the patched `fetch`.
* The commit, the fsync and the UI rendering are real.
* A kill of nexus-api during an HTTP PUT with real processes is attack O8. It is now done; see JUDGE_TEST_RESULTS.md.

## N7 / N8: network adapter off, internet off

NEXUS uses only loopback: nexus-api on `127.0.0.1:8080` and the dashboard on `localhost:3000`. The Windows loopback interface stays up when every physical adapter is disabled, so both should keep working.

### Verified now, with the network on (2026-11-07)

**Static scan.**
* No external URL anywhere in `frontend/src` or `next.config`.
* No `next/font` (which would fetch Google Fonts while the dev server compiles).
* Fonts are system stacks (`ui-sans-serif, system-ui, "Segoe UI", …`).
* The CSS `@import`s are local npm packages.
* There are no outbound HTTP calls in the Go code (`src`, `torture2`).

**Runtime, in the browser.**
* All 11 dashboard pages were loaded in the built-in browser and every resource the page fetched was listed: **323 resources from only `localhost:3000` and `127.0.0.1:8080`, 0 external**.

**Offline check script, baseline.** [`frontend/scripts/offline-check.mjs`](../../frontend/scripts/offline-check.mjs) ran with `--label network-on --probe-internet` and passed **14/14**: nexus-api health, a durable PUT/GET/DELETE round trip, X-RAY, and all 11 pages served with no external `src`/`href`. Results: [offline-check-network-on.json](offline-check-network-on.json). Interface names only; no IP addresses are saved.

**One server-side caveat.**
* Next.js telemetry is enabled on this machine (`npx next telemetry status`). The `next dev` process sends anonymous events to nextjs.org, sent and forgotten.
* Offline these simply fail. The dashboard does not depend on them, and the browser check above shows no page requests one.
* To silence it for a demo, start the dashboard with `NEXT_TELEMETRY_DISABLED=1` (environment variable, this shell only). The persistent setting was not changed.

### Result, 2026-11-07: N7 and N8 PASS (real Airplane mode, toggled by the user)

| Run (local time) | Interfaces up | Internet probe | Checks | File |
|---|---|---|---|---|
| network-on, 09:04 | Ethernet 2 (virtual), Wi-Fi | reachable | 14/14 | [offline-check-network-on.json](offline-check-network-on.json) |
| **adapter-off, 09:19** | **Ethernet 2 (VirtualBox host-only) only; Wi-Fi gone** | **unreachable (`EHOSTUNREACH`, TCP to 1.1.1.1:443)** | **16/16**, including `state-internet` and `state-adapters` | [offline-check-adapter-off.json](offline-check-adapter-off.json) |
| network-restored, 09:21 | Ethernet 2, Wi-Fi | reachable | 14/14 | [offline-check-network-restored.json](offline-check-network-restored.json) |

With every physical adapter off and no internet:
* nexus-api answered on 127.0.0.1;
* a durable PUT/GET/DELETE round trip succeeded;
* X-RAY answered;
* all 11 dashboard pages were served without external resources.

The user also reported that, while offline, a reload of the Database Explorer with PUT and GET worked and the backend badge stayed Connected. That browser step is user-reported; the agent could not observe it (its own session was offline).

Notes:
* The 09:21 `network-restored` run was written to the original file name and replaced an earlier 09:14 run. That run was only a second online baseline (it came after the invalid attempt below), so no offline evidence was lost.
* The 09:19 adapter-off run does not separately cover "internet off with the adapter on" (optional step 5). It is a strict superset: no internet *and* no physical adapter.

### First attempt, 2026-11-07 09:13 local time: invalid, not counted

* A run labelled `adapter-off` was recorded with **Wi-Fi still up** (the MediaTek adapter had an IPv4 address) and the **internet reachable**, the same network state as the baseline. It is not N7 evidence.
* It is kept, unmodified, as [offline-check-adapter-off.INVALID-network-was-on.json](offline-check-adapter-off.INVALID-network-was-on.json).
* Likely cause: Windows 11 remembers when Wi-Fi is turned back on inside Airplane mode, so Airplane mode left Wi-Fi connected. Or the run happened outside the offline window.
* The `network-restored` run at 09:14 passed 14/14, but with nothing proven offline in between, it is only a second baseline.

**Script hardened as a result.**
* The labels `internet-off` and `adapter-off` are now *verified claims*. The run FAILS unless the internet probe fails, and, for `adapter-off`, unless no interface is up except those named with `--virtual`.
* The probe is now a TCP connect to `1.1.1.1:443`. A DNS lookup can be answered from the Windows DNS cache while offline.
* Re-running the old mislabelled state now gives `FAIL [adapter-off]: 14/16` (`state-internet` and `state-adapters` fail), so a mislabelled run can no longer look like a pass.

**This machine.** `Ethernet 2` is the *VirtualBox Host-Only Ethernet Adapter*. It is virtual, has no internet, and Airplane mode does not turn it off. Pass it as `--virtual "Ethernet 2"`. The physical adapters are the MediaTek Wi-Fi and the Realtek Ethernet (unplugged).

### Manual procedure (a person does the toggle)

Keep nexus-api and the dashboard running. Run from the project folder.

1. Turn on **Airplane mode**. In the quick-settings panel, check that the **Wi-Fi tile is off** too (if it is on, click it off).
2. Run this while still in Airplane mode:

   ```bash
   node frontend/scripts/offline-check.mjs --label adapter-off --virtual "Ethernet 2" --json docs/JUDGE_READINESS/offline-check-adapter-off.json
   ```

   It must end with `PASS [adapter-off]: 16/16`, which includes the two state checks. If `state-internet` or `state-adapters` fails, the network was not off: fix it and run again.
3. Still offline: reload `http://localhost:3000/database`, do a PUT and a GET, and check that the backend badge stays **Connected**.
4. Turn Airplane mode off and run:

   ```bash
   node frontend/scripts/offline-check.mjs --label network-restored --probe-internet --json docs/JUDGE_READINESS/offline-check-network-restored-2.json
   ```

5. *Optional, N8 only (internet off, adapter on):* disconnect from the Wi-Fi network (or unplug the router uplink) and run:

   ```bash
   node frontend/scripts/offline-check.mjs --label internet-off --json docs/JUDGE_READINESS/offline-check-internet-off.json
   ```

   It must end with `PASS … 15/15`. Airplane mode in step 2 already proves that NEXUS needs no internet.

**Pass criterion.** The offline run passes *all* checks, including its state checks.
