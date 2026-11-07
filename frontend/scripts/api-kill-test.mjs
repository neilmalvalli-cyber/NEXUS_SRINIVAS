// Judge attack O8: kill nexus-api while HTTP writes are in flight.
//
// Real processes, real TerminateProcess, real Windows filesystem. Each
// iteration starts nexus-api on a disposable database, runs concurrent HTTP
// writers (PUT, DELETE, BATCH), kills the process at a random moment, restarts
// it on the same database and reads every key back through HTTP.
//
// Oracle (per key; each key is owned by one writer, which has at most one
// request in flight, so the order of operations on a key is known):
//   * HTTP 2xx (acknowledged)       -> the value must be present after restart
//   * definite error (envelope 4xx) -> the operation must not have happened
//   * no answer (reset / refused / timeout: "outcome unknown")
//                                   -> either the old or the new value, nothing else
//   * BATCH with unknown outcome    -> all of its operations or none
//   * no value that was never written may appear
//
// Usage:
//   node scripts/api-kill-test.mjs --api PATH/nexus-api.exe --dir NEW_DIR [--iterations 30] [--seed 1] [--writers 12] [--json out.json]
//   node scripts/api-kill-test.mjs --control --dir NEW_DIR   # negative control: a server that ACKs without durability
import { spawn } from "node:child_process";
import { mkdirSync, existsSync, readdirSync, writeFileSync } from "node:fs";
import net from "node:net";
import path from "node:path";

const arg = (name, def) => {
  const i = process.argv.indexOf(`--${name}`);
  return i > 0 ? process.argv[i + 1] : def;
};
const CONTROL = process.argv.includes("--control");
const API = arg("api");
const DIR = arg("dir");
const ITER = Number(arg("iterations", CONTROL ? 5 : 30));
const SEED = Number(arg("seed", 1));
const WRITERS = Number(arg("writers", 12));
const KEYS_PER_WRITER = 4;
if (!DIR || (!CONTROL && !API)) {
  console.error("usage: --api nexus-api.exe --dir NEW_DIR [--iterations N] [--seed N] [--writers N] [--json out]  |  --control --dir NEW_DIR");
  process.exit(2);
}
if (existsSync(DIR) && readdirSync(DIR).length > 0) {
  console.error(`refusing: ${DIR} exists and is not empty (this test only uses a new, disposable directory)`);
  process.exit(2);
}
mkdirSync(DIR, { recursive: true });

// Deterministic PRNG (workload choices and kill times; thread timing is not replayable).
let rs = SEED >>> 0 || 1;
const rnd = () => ((rs = (rs * 1664525 + 1013904223) >>> 0) / 2 ** 32);
const pick = (n) => Math.floor(rnd() * n);

const freePort = () =>
  new Promise((r) => {
    const s = net.createServer().listen(0, "127.0.0.1", () => {
      const p = s.address().port;
      s.close(() => r(p));
    });
  });

// A deliberately broken server for the negative control: ACKs writes from memory only.
const LYING_SERVER = `
const http=require("http");const m=new Map();
http.createServer((q,s)=>{let b="";q.on("data",c=>b+=c);q.on("end",()=>{
 const k=decodeURIComponent(q.url.replace(/^\\/api\\/keys\\//,""));
 const j=(c,o)=>{s.writeHead(c,{"Content-Type":"application/json"});s.end(JSON.stringify(o));};
 if(q.url==="/api/health")return j(200,{db_open:true});
 if(q.url==="/api/batch"){for(const o of JSON.parse(b).ops){o.op==="put"?m.set(o.key,o.value):m.delete(o.key);}return j(200,{ok:true});}
 if(q.method==="PUT"){m.set(k,JSON.parse(b).value);return j(200,{ok:true});}
 if(q.method==="DELETE"){if(!m.has(k))return j(404,{error:{class:"notfound",message:"x"}});m.delete(k);return j(200,{ok:true});}
 if(!m.has(k))return j(404,{error:{class:"notfound",message:"x"}});
 return j(200,{value:{data:m.get(k),encoding:"utf8"}});});}).listen(Number(process.argv[1]),"127.0.0.1");`;

async function startServer(port) {
  const child = CONTROL
    ? spawn(process.execPath, ["-e", LYING_SERVER, String(port)], { stdio: "ignore" })
    : spawn(API, ["--addr", `127.0.0.1:${port}`, "--db", path.join(DIR, "db.kv"), "--work", path.join(DIR, "work"),
        "--evidence", path.join(DIR, "no-evidence"), "--perf-history", path.join(DIR, "no-history.json"), "--allow-origin", ""],
        { stdio: "ignore" });
  const t0 = Date.now();
  while (Date.now() - t0 < 15_000) {
    if (child.exitCode !== null) throw new Error(`server exited with ${child.exitCode} during start`);
    try {
      const r = await fetch(`http://127.0.0.1:${port}/api/health`, { signal: AbortSignal.timeout(1000) });
      const h = await r.json();
      if (r.ok && h.db_open) return { child, startMs: Date.now() - t0 };
    } catch {}
    await new Promise((r) => setTimeout(r, 30));
  }
  child.kill();
  throw new Error("server did not become healthy in 15 s");
}

const ABSENT = "\u0000absent";
const model = new Map(); // key -> Set of allowed values (ABSENT = no key)
for (let w = 0; w < WRITERS; w++) for (let k = 0; k < KEYS_PER_WRITER; k++) model.set(`w${w}/k${k}`, new Set([ABSENT]));
const unknownBatches = []; // { iter, ops: [[key, value]] }

const totals = { iterations: 0, requests: 0, acked: 0, definite: 0, unknown: 0, unknownApplied: 0, unknownNotApplied: 0,
  batches: 0, unknownBatches: 0, keysVerified: 0, violations: [] };
const violation = (iter, what) => {
  totals.violations.push({ iter, what });
  if (totals.violations.length <= 20) console.log(`    VIOLATION (iteration ${iter}): ${what}`);
};

async function call(base, method, p, body) {
  try {
    const r = await fetch(base + p, {
      method, signal: AbortSignal.timeout(5000),
      headers: body ? { "Content-Type": "application/json" } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
    const text = await r.text();
    let json; try { json = JSON.parse(text); } catch {}
    if (r.ok) return { kind: "acked", json };
    if (json?.error) return json.error.outcome_unknown ? { kind: "unknown", why: `engine ${json.error.class}` } : { kind: "definite", cls: json.error.class, status: r.status };
    return r.status >= 500 ? { kind: "unknown", why: `bare ${r.status}` } : { kind: "definite", status: r.status };
  } catch (e) {
    return { kind: "unknown", why: e.cause?.code || e.name };
  }
}

async function verify(base, iter) {
  for (const [key, allowed] of model) {
    const r = await call(base, "GET", `/api/keys/${encodeURIComponent(key)}`);
    let got;
    if (r.kind === "acked") got = r.json?.value?.data;
    else if (r.kind === "definite" && r.cls === "notfound") got = ABSENT;
    else { violation(iter, `GET ${key} after restart did not answer cleanly (${JSON.stringify(r)})`); continue; }
    if (!allowed.has(got)) {
      violation(iter, `${key} = ${got === ABSENT ? "<absent>" : JSON.stringify(got.slice(0, 60))}, allowed: ${[...allowed].map((v) => (v === ABSENT ? "<absent>" : v.slice(0, 40))).join(" | ")}`);
    }
    model.set(key, new Set([got]));
    totals.keysVerified++;
  }
  for (const b of unknownBatches.splice(0)) {
    if (b.ops.length === 0) continue;
    const applied = b.ops.map(([k, v]) => model.get(k).has(v));
    if (applied.some(Boolean) && !applied.every(Boolean)) violation(iter, `batch from iteration ${b.iter} partially applied: ${applied}`);
    if (applied.every(Boolean)) totals.unknownApplied++; else totals.unknownNotApplied++;
  }
}

async function writer(base, w, iter, stop, counts) {
  let n = 0;
  while (!stop.v) {
    n++;
    const tag = `s${SEED}-i${iter}-w${w}-n${n}`;
    const pad = "x".repeat(pick(4096));
    const r = rnd();
    if (r < 0.2) {
      const ks = [0, 1, 2, 3].filter(() => rnd() < 0.7).map((k) => `w${w}/k${k}`);
      if (ks.length === 0) ks.push(`w${w}/k0`);
      const ops = ks.map((k) => (rnd() < 0.85 ? ["put", k, `${tag}|${k}|${pad}`] : ["del", k, ABSENT]));
      totals.batches++;
      const res = await call(base, "POST", "/api/batch", { ops: ops.map(([op, key, value]) => (op === "put" ? { op, key, value, encoding: "utf8" } : { op, key })) });
      account(res, counts);
      if (res.kind === "acked") for (const [, k, v] of ops) model.set(k, new Set([v]));
      else if (res.kind === "unknown") {
        // Only operations whose result differs from every value the key could
        // already hold show whether the batch was applied (a DEL of a key that
        // may already be absent proves nothing).
        const visible = ops.filter(([, k, v]) => !model.get(k).has(v)).map(([, k, v]) => [k, v]);
        for (const [, k, v] of ops) model.get(k).add(v);
        unknownBatches.push({ iter, ops: visible });
        totals.unknownBatches++;
        return;
      } else if (res.kind === "definite") continue;
    } else {
      const key = `w${w}/k${pick(KEYS_PER_WRITER)}`;
      const del = r < 0.3;
      const value = del ? ABSENT : `${tag}|${key}|${pad}`;
      const res = del
        ? await call(base, "DELETE", `/api/keys/${encodeURIComponent(key)}`)
        : await call(base, "PUT", `/api/keys/${encodeURIComponent(key)}`, { value, encoding: "utf8" });
      account(res, counts);
      if (res.kind === "acked") model.set(key, new Set([value]));
      else if (res.kind === "definite" && del && res.cls === "notfound") model.set(key, new Set([ABSENT]));
      else if (res.kind === "unknown") { model.get(key).add(value); return; }
    }
  }
}
function account(res, counts) {
  totals.requests++; counts.requests++;
  totals[res.kind]++; counts[res.kind]++;
}

const t0 = Date.now();
console.log(`O8 ${CONTROL ? "NEGATIVE CONTROL (lying in-memory server)" : `nexus-api ${API}`}  seed=${SEED} iterations=${ITER} writers=${WRITERS} dir=${DIR}`);
const perIter = [];
let prev = null;
for (let iter = 1; iter <= ITER; iter++) {
  const port = await freePort();
  const { child, startMs } = await startServer(port);
  const base = `http://127.0.0.1:${port}`;
  if (iter > 1) await verify(base, iter); // state left by the previous kill
  const counts = { requests: 0, acked: 0, definite: 0, unknown: 0 };
  const stop = { v: false };
  const killAt = 40 + pick(500);
  const workers = Array.from({ length: WRITERS }, (_, w) => writer(base, w, iter, stop, counts));
  await new Promise((r) => setTimeout(r, killAt));
  child.kill(); // TerminateProcess on Windows: no shutdown, no flush, no defer
  stop.v = true;
  await Promise.all(workers);
  await new Promise((r) => (child.exitCode !== null || child.signalCode !== null) ? r() : child.once("exit", r));
  totals.iterations++;
  perIter.push({ iter, killAtMs: killAt, startMs, ...counts });
  console.log(`  [${String(iter).padStart(3)}/${ITER}] kill@${killAt}ms sent=${counts.requests} acked=${counts.acked} definite=${counts.definite} unknown=${counts.unknown} restart=${startMs}ms violations=${totals.violations.length}`);
  prev = iter;
}
// Final restart and verification of the last kill.
{
  const port = await freePort();
  const { child } = await startServer(port);
  await verify(`http://127.0.0.1:${port}`, prev + 1);
  child.kill();
}
const pass = totals.violations.length === 0;
console.log(`\n${pass ? "PASS" : "FAIL"}: ${totals.iterations} kills, ${totals.requests} HTTP writes (${totals.acked} acknowledged, ${totals.definite} definite errors, ${totals.unknown} outcome unknown), ` +
  `${totals.keysVerified} key reads verified after restart, ${totals.violations.length} violations; unknown batches: ${totals.unknownApplied} applied whole, ${totals.unknownNotApplied} not applied (${((Date.now() - t0) / 1000).toFixed(1)} s)`);
const out = arg("json");
if (out) writeFileSync(out, JSON.stringify({ when: new Date().toISOString(), control: CONTROL, seed: SEED, writers: WRITERS, real_processes: !CONTROL, totals, perIter }, null, 2));
process.exit(pass ? (CONTROL ? 1 : 0) : (CONTROL ? 0 : 1)); // the control is expected to FAIL
