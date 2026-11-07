// Judge attacks N7 (network adapter disabled) and N8 (internet off).
// Run it once with the network on and again after you disable the adapter or
// unplug the internet. NEXUS runs entirely on 127.0.0.1, so every check must
// pass in both states. The script never changes any system setting.
//
// Checks: which non-loopback interfaces are up (evidence of the adapter state),
// optional internet reachability, nexus-api health, a PUT/GET/DELETE round
// trip on a throwaway key, X-RAY summary, every dashboard page served by
// localhost:3000, and no external URL in the served HTML.
//
// Usage:
//   node scripts/offline-check.mjs --label network-on  [--probe-internet] [--json out.json]
//   node scripts/offline-check.mjs --label adapter-off [--virtual "Ethernet 2"] [--json out.json]
//   options: --api http://127.0.0.1:8080  --web http://localhost:3000
//
// The labels "internet-off" and "adapter-off" are claims about the network,
// so the script verifies them and FAILS if the claim is not true (a run taken
// with the network still on must never be recorded as offline evidence):
//   internet-off: the internet probe must fail.
//   adapter-off:  the internet probe must fail AND no non-loopback interface
//                 may be up except those named with --virtual (host-only
//                 adapters such as VirtualBox's, which have no internet and
//                 which Airplane mode does not switch off).
// The probe is a TCP connect to 1.1.1.1:443 (no DNS, so a cached DNS answer
// cannot make an offline machine look online); nothing is sent over it.
import os from "node:os";
import net from "node:net";
import { writeFileSync } from "node:fs";

const arg = (n, d) => { const i = process.argv.indexOf(`--${n}`); return i > 0 ? process.argv[i + 1] : d; };
const API = arg("api", "http://127.0.0.1:8080").replace(/\/+$/, "");
const WEB = arg("web", "http://localhost:3000").replace(/\/+$/, "");
const LABEL = arg("label", "unlabelled");
const CLAIMS_OFFLINE = LABEL === "internet-off" || LABEL === "adapter-off";
const VIRTUAL = process.argv.flatMap((a, i) => (a === "--virtual" ? [process.argv[i + 1]] : []));
const PAGES = ["/", "/database", "/storage", "/recovery", "/torture", "/xray", "/reliability", "/benchmark", "/architecture", "/comparison", "/demo"];

const results = [];
const check = (id, title, pass, detail) => {
  results.push({ id, title, pass, detail });
  console.log(`  ${pass ? "ok  " : "FAIL"}  [${id}] ${title}${detail ? `  (${detail})` : ""}`);
};
const req = (url, init = {}) => fetch(url, { ...init, signal: AbortSignal.timeout(init.timeout ?? 60_000) });

// Environment evidence (not pass/fail).
const ifaces = Object.entries(os.networkInterfaces())
  .flatMap(([name, addrs]) => (addrs ?? []).filter((a) => !a.internal && a.family === "IPv4").map(() => name)); // names only: no addresses in saved evidence
console.log(`NEXUS offline check  label=${LABEL}  ${new Date().toISOString()}`);
console.log(`  non-loopback IPv4 interfaces up: ${ifaces.length ? ifaces.join(", ") : "none (only loopback)"}`);
let internet = "not probed (pass --probe-internet)";
let online = null;
if (process.argv.includes("--probe-internet") || CLAIMS_OFFLINE) {
  online = await new Promise((resolve) => {
    const s = net.connect({ host: "1.1.1.1", port: 443, timeout: 4000 });
    const done = (v, why) => { s.destroy(); internet = v ? "reachable (TCP connect to 1.1.1.1:443 succeeded)" : `unreachable (${why})`; resolve(v); };
    s.once("connect", () => done(true));
    s.once("timeout", () => done(false, "timeout"));
    s.once("error", (e) => done(false, e.code || e.message));
  });
}
console.log(`  internet: ${internet}`);
if (CLAIMS_OFFLINE) {
  check("state-internet", `label "${LABEL}" claims no internet: probe must fail`, online === false, internet);
}
if (LABEL === "adapter-off") {
  const unexpected = ifaces.filter((n) => !VIRTUAL.includes(n));
  check("state-adapters", `label "adapter-off": no interface up except --virtual [${VIRTUAL.join(", ")}]`, unexpected.length === 0,
    unexpected.length ? `still up: ${unexpected.join(", ")}` : `up: ${ifaces.join(", ") || "none"}`);
}

// nexus-api on loopback.
try {
  const h = await (await req(`${API}/api/health`, { timeout: 4000 })).json();
  check("api-health", "nexus-api answers on loopback with the database open", h.db_open === true, API);
} catch (e) {
  check("api-health", "nexus-api answers on loopback with the database open", false, e.cause?.code || e.message);
}
const key = `offline-check:${LABEL}:${Date.now()}`;
const kp = `${API}/api/keys/${encodeURIComponent(key)}`;
try {
  const p = await req(kp, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ value: "durable-offline", encoding: "utf8" }) });
  const pj = await p.json();
  const g = await (await req(kp)).json();
  const d = await req(kp, { method: "DELETE" });
  const gone = (await req(kp)).status === 404;
  check("api-roundtrip", "PUT (durable) / GET / DELETE round trip", p.ok && pj.durable === true && g.value?.data === "durable-offline" && d.ok && gone, key);
} catch (e) {
  check("api-roundtrip", "PUT (durable) / GET / DELETE round trip", false, e.cause?.code || e.message);
}
try {
  const r = await req(`${API}/api/xray/summary`);
  check("api-xray", "X-RAY summary served", r.ok, `HTTP ${r.status}`);
} catch (e) {
  check("api-xray", "X-RAY summary served", false, e.cause?.code || e.message);
}

// Dashboard pages and their HTML.
for (const p of PAGES) {
  try {
    const r = await req(WEB + p);
    const html = await r.text();
    const ext = [...html.matchAll(/(?:src|href)="(https?:\/\/[^"]+)"/g)].map((m) => m[1]).filter((u) => !/^https?:\/\/(localhost|127\.0\.0\.1)(:\d+)?\//.test(u));
    check(`web${p}`, `dashboard ${p} served without external resources`, r.ok && ext.length === 0, ext.length ? `external: ${ext.join(" ")}` : `HTTP ${r.status}, ${html.length} bytes`);
  } catch (e) {
    check(`web${p}`, `dashboard ${p} served without external resources`, false, e.cause?.code || e.message);
  }
}

const failed = results.filter((r) => !r.pass).length;
console.log(`\n${failed ? "FAIL" : "PASS"} [${LABEL}]: ${results.length - failed}/${results.length} checks passed`);
const out = arg("json");
if (out) writeFileSync(out, JSON.stringify({ label: LABEL, when: new Date().toISOString(), interfacesUp: ifaces, internet, results }, null, 2));
process.exit(failed ? 1 : 0);
