#!/usr/bin/env node
// API integration test for nexus-api, the backend of the NEXUS dashboard.
// No dependencies: Node 18+ (global fetch). Runs against a live adapter:
//
//   node scripts/api-integration.mjs [base-url]    (default http://127.0.0.1:8080)
//
// It writes only keys under the prefix "itest:" in the open database and
// deletes them again; it never switches databases, starts torture runs or
// touches the recovery demo. X-RAY checks scan the open database read-only
// and rebuild/repair only the disposable X-RAY lab in the adapter's work
// directory. Exit code 0 when every check passes.

const BASE = (process.argv[2] ?? process.env.NEXUS_API_URL ?? "http://127.0.0.1:8080").replace(/\/$/, "");
const P = `itest:${Date.now()}:`;
let passed = 0;
let failed = 0;

async function call(method, path, body) {
  const res = await fetch(BASE + path, {
    method,
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  let json = null;
  try {
    json = await res.json();
  } catch {
    /* not JSON */
  }
  return { status: res.status, json };
}

async function check(name, fn) {
  try {
    await fn();
    passed++;
    console.log(`  ok    ${name}`);
  } catch (e) {
    failed++;
    console.log(`  FAIL  ${name}\n        ${e.message}`);
  }
}

function expect(cond, msg) {
  if (!cond) throw new Error(msg);
}
const k = (s) => encodeURIComponent(P + s);

console.log(`nexus-api integration test against ${BASE}`);

await check("health: database open, JSON shape", async () => {
  const r = await call("GET", "/api/health");
  expect(r.status === 200, `status ${r.status}`);
  expect(r.json.db_open === true, "db_open is not true");
  expect(typeof r.json.tools === "object", "no tools object");
});

await check("overview: stats and format version", async () => {
  const r = await call("GET", "/api/overview");
  expect(r.status === 200, `status ${r.status}`);
  expect(typeof r.json.stats.keys === "number", "stats.keys missing");
  expect(r.json.format_version === 1, `format_version ${r.json.format_version}`);
});

await check("PUT new key reports inserted", async () => {
  const r = await call("PUT", `/api/keys/${k("a")}`, { value: "one", encoding: "utf8" });
  expect(r.status === 200 && r.json.action === "inserted", JSON.stringify(r.json));
  expect(r.json.durable === true, "not durable");
});

await check("PUT same key reports updated", async () => {
  const r = await call("PUT", `/api/keys/${k("a")}`, { value: "two", encoding: "utf8" });
  expect(r.status === 200 && r.json.action === "updated", JSON.stringify(r.json));
});

await check("GET returns the latest value", async () => {
  const r = await call("GET", `/api/keys/${k("a")}`);
  expect(r.status === 200 && r.json.value.data === "two", JSON.stringify(r.json));
});

await check("GET missing key is 404 notfound", async () => {
  const r = await call("GET", `/api/keys/${k("missing")}`);
  expect(r.status === 404 && r.json.error.class === "notfound", `${r.status} ${JSON.stringify(r.json)}`);
});

await check("DELETE missing key reports absent (nothing written)", async () => {
  const r = await call("DELETE", `/api/keys/${k("missing")}`);
  expect(r.status === 200 && r.json.action === "absent", JSON.stringify(r.json));
});

await check("BATCH applies all operations atomically", async () => {
  const r = await call("POST", "/api/batch", {
    ops: [
      { op: "put", key: P + "b1", value: "x" },
      { op: "put", key: P + "b2", value: "y" },
      { op: "del", key: P + "a" },
    ],
  });
  expect(r.status === 200 && r.json.ops === 3, JSON.stringify(r.json));
  const a = await call("GET", `/api/keys/${k("a")}`);
  const b = await call("GET", `/api/keys/${k("b2")}`);
  expect(a.status === 404 && b.status === 200, `a ${a.status}, b2 ${b.status}`);
});

await check("BATCH with an invalid op is rejected and writes nothing", async () => {
  const r = await call("POST", "/api/batch", { ops: [{ op: "put", key: P + "c", value: "z" }, { op: "bogus", key: P + "d" }] });
  expect(r.status === 400, `status ${r.status}`);
  const c = await call("GET", `/api/keys/${k("c")}`);
  expect(c.status === 404, "first op of a rejected batch was written");
});

await check("DUMP with filter finds the batch keys", async () => {
  const r = await call("GET", `/api/dump?limit=100&q=${encodeURIComponent(P)}`);
  expect(r.status === 200, `status ${r.status}`);
  const keys = r.json.items.map((i) => i.key.data).sort();
  expect(JSON.stringify(keys) === JSON.stringify([P + "b1", P + "b2"]), JSON.stringify(keys));
});

await check("log map: valid superblock, frames end cleanly", async () => {
  const r = await call("GET", "/api/storage/frames?limit=5");
  expect(r.status === 200, `status ${r.status}`);
  expect(r.json.superblock.valid && r.json.superblock.magic === "KIPDBLOG", "bad superblock");
  expect(r.json.stop.kind === "clean", `stop ${r.json.stop.kind}`);
  expect(r.json.last_seq >= 1 && r.json.valid_end === r.json.file_size, "valid_end != file_size");
});

await check("verify (CHECK) passes", async () => {
  const r = await call("POST", "/api/verify");
  expect(r.status === 200 && r.json.ok === true, JSON.stringify(r.json));
});

await check("databases: list is confined to the data directory", async () => {
  const r = await call("GET", "/api/databases");
  expect(r.status === 200 && Array.isArray(r.json.files), `status ${r.status}`);
  const bad = await call("POST", "/api/databases/open", { name: "../escape", create: true });
  expect(bad.status === 400, `path name accepted: ${bad.status}`);
});

await check("evidence: path traversal refused", async () => {
  const r = await call("GET", "/api/evidence/..%2F..%2Fgo.mod");
  expect(r.status === 404 || r.status === 400, `status ${r.status}`);
});

await check("evidence: recorded bench.json is served", async () => {
  const r = await call("GET", "/api/evidence/bench.json");
  expect(r.status === 200 && Array.isArray(r.json.median_of_runs), `status ${r.status}`);
});

await check("torture catalogs: 32 scenarios, 7 mutations", async () => {
  const s = await call("GET", "/api/torture/scenarios");
  const m = await call("GET", "/api/torture/mutants");
  expect(s.status === 200 && s.json.scenarios.length === 32, `scenarios ${s.json?.scenarios?.length}`);
  expect(m.status === 200 && m.json.mutations.length === 7 && m.json.mutations[0].mutation === "none", "mutation catalog");
});

await check("bench: out-of-range n rejected", async () => {
  const r = await call("POST", "/api/bench/runs", { n: 10, runs: 1, value_size: 100, batch_size: 1000 });
  expect(r.status === 400 || r.status === 503, `status ${r.status}`);
});

// ---- X-RAY (read-only scans; the lab is disposable and lives in the adapter's work dir) ----

await check("xray: main scan is read-only and healthy", async () => {
  const r = await call("POST", "/api/xray/scan", { target: "main" });
  expect(r.status === 200, `status ${r.status}`);
  const rep = r.json.report;
  expect(rep.mode === "read-only" && rep.live === true, "mode/live");
  expect(rep.modified === false, "scan modified the main database");
  expect(rep.fingerprint_before.sha256 === rep.fingerprint_after.sha256, "fingerprint changed");
  expect(["healthy", "attention"].includes(rep.health), `health ${rep.health}`);
  const s = await call("GET", "/api/xray/summary");
  expect(s.status === 200 && s.json.live && s.json.live.id === r.json.id, "summary does not reflect the scan");
});

await check("xray: paths and unknown fields refused; main never repaired", async () => {
  for (const t of ["../x", "C:\\Windows\\win.ini", "/etc/passwd", "lab:../../x", ""]) {
    const r = await call("POST", "/api/xray/scan", { target: t });
    expect(r.status === 400, `target ${JSON.stringify(t)} → ${r.status}`);
  }
  const u = await call("POST", "/api/xray/scan", { target: "main", path: "x" });
  expect(u.status === 400, `unknown field → ${u.status}`);
  const m = await call("POST", "/api/xray/repair", { target: "main", confirm: true });
  expect(m.status === 403, `repair main → ${m.status}`);
});

await check("xray: report list, fetch and export", async () => {
  const l = await call("GET", "/api/xray/reports?kind=live");
  expect(l.status === 200 && l.json.reports.length > 0, "no live reports");
  const id = l.json.reports[0].id;
  const g = await call("GET", `/api/xray/reports/${id}`);
  expect(g.status === 200 && g.json.id === id, "fetch by id");
  const e = await fetch(`${BASE}/api/xray/reports/${id}/export`);
  expect(e.status === 200 && (e.headers.get("content-disposition") ?? "").includes("attachment"), "export header");
  const n = await call("GET", "/api/xray/reports/x-nope");
  expect(n.status === 404 || n.status === 400, `unknown id → ${n.status}`);
});

await check("xray lab: every damaged case diagnosed as designed", async () => {
  const b = await call("POST", "/api/xray/lab/reset");
  expect(b.status === 200 && b.json.cases.length >= 10, `lab build ${b.status}`);
  const a = await call("POST", "/api/xray/lab/scan-all");
  expect(a.status === 200, `scan-all ${a.status}`);
  const miss = a.json.results.filter((r) => r.matched !== true).map((r) => r.target);
  expect(miss.length === 0, `not matched: ${miss.join(", ")}`);
});

await check("xray lab: repair needs confirm; torn tail repaired and verified; corruption refused", async () => {
  const n = await call("POST", "/api/xray/repair", { target: "lab:torn-tail" });
  expect(n.status === 400, `without confirm → ${n.status}`);
  const r = await call("POST", "/api/xray/repair", { target: "lab:torn-tail", confirm: true });
  expect(r.status === 200 && r.json.report.repair.verified === true && r.json.report.repair.backups.length > 0, "torn tail not verified");
  const c = await call("POST", "/api/xray/repair", { target: "lab:committed-frame-corrupted", confirm: true });
  expect(c.json.report.repair.verified === false && c.json.report.modified === false, "corruption was modified");
});

// ---- Power-Cut Explorer and performance (validation only; no runs started) ----

await check("powercut: info has model, limits and disclaimer", async () => {
  const r = await call("GET", "/api/powercut");
  expect(r.status === 200, `status ${r.status}`);
  expect(Array.isArray(r.json.model) && r.json.model.length >= 5, "model");
  expect(/not an exhaustive proof/.test(r.json.disclaimer), "disclaimer");
  expect(r.json.sector_size === 512, "sector size");
});

await check("powercut: invalid configurations rejected", async () => {
  for (const b of [{ writers: 0 }, { writers: 99 }, { ops_per_writer: 100000 }, { seed: -1 }, { max_crash_points: 0 }, { dir: "x" }]) {
    const r = await call("POST", "/api/powercut/runs", b);
    expect(r.status === 400 || r.status === 503, `${JSON.stringify(b)} → ${r.status}`);
  }
  const t = await call("GET", "/api/powercut/runs/none/images/..%2f..%2fx/xray");
  expect(t.status === 404, `image traversal → ${t.status}`);
});

await check("perf: history is recorded; full run without phases refused", async () => {
  const h = await call("GET", "/api/perf/history");
  expect(h.status === 200 && h.json.kind === "recorded" && h.json.history.metrics.length > 10, `history ${h.status}`);
  const f = await call("POST", "/api/perf/runs", {});
  expect(f.status === 400 || f.status === 503, `empty perf → ${f.status}`);
  const u = await call("POST", "/api/perf/runs", { only: ["nope"] });
  expect(u.status === 400 || u.status === 503, `unknown phase → ${u.status}`);
});

// cleanup
await call("POST", "/api/batch", { ops: [{ op: "del", key: P + "b1" }, { op: "del", key: P + "b2" }] });

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed ? 1 : 0);
