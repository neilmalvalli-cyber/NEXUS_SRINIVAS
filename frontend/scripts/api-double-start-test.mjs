// Judge attack O7: two nexus-api instances started on the same database.
// Real processes on a disposable directory. Expected: the second instance
// fails with a clear error and exits; the first keeps serving, its data and
// writes unaffected; nothing is left locked once an instance exits.
//
// Usage: node scripts/api-double-start-test.mjs --api PATH/nexus-api.exe --dir NEW_DIR [--races 10] [--json out.json]
import { spawn } from "node:child_process";
import { existsSync, mkdirSync, readdirSync, writeFileSync } from "node:fs";
import net from "node:net";
import path from "node:path";

const arg = (n, d) => { const i = process.argv.indexOf(`--${n}`); return i > 0 ? process.argv[i + 1] : d; };
const API = arg("api"), DIR = arg("dir"), RACES = Number(arg("races", 10));
if (!API || !DIR) { console.error("usage: --api nexus-api.exe --dir NEW_DIR [--races N] [--json out]"); process.exit(2); }
if (existsSync(DIR) && readdirSync(DIR).length) { console.error(`refusing: ${DIR} is not empty`); process.exit(2); }
mkdirSync(DIR, { recursive: true });

const freePort = () => new Promise((r) => { const s = net.createServer().listen(0, "127.0.0.1", () => { const p = s.address().port; s.close(() => r(p)); }); });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const dbPath = (n) => path.join(DIR, n, "db.kv");

function start(db, port) {
  const p = { port, stderr: "", exit: null, t0: Date.now(), exitMs: null };
  p.child = spawn(API, ["--addr", `127.0.0.1:${port}`, "--db", db, "--work", path.join(DIR, `work-${port}`),
    "--evidence", path.join(DIR, "none"), "--perf-history", path.join(DIR, "none.json"), "--allow-origin", ""], { stdio: ["ignore", "ignore", "pipe"] });
  p.child.stderr.on("data", (c) => (p.stderr += c));
  p.done = new Promise((r) => p.child.on("exit", (code, sig) => { p.exit = code ?? sig; p.exitMs = Date.now() - p.t0; r(); }));
  return p;
}
async function healthy(p, ms = 15000) {
  const t0 = Date.now();
  while (Date.now() - t0 < ms && p.exit === null) {
    try { const r = await fetch(`http://127.0.0.1:${p.port}/api/health`, { signal: AbortSignal.timeout(800) }); if (r.ok && (await r.json()).db_open) return true; } catch {}
    await sleep(30);
  }
  return false;
}
const settle = (p, ms = 20000) => Promise.race([p.done, sleep(ms)]);
async function put(p, k, v) {
  const r = await fetch(`http://127.0.0.1:${p.port}/api/keys/${encodeURIComponent(k)}`, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ value: v, encoding: "utf8" }), signal: AbortSignal.timeout(5000) });
  return r.ok;
}
async function get(p, k) {
  const r = await fetch(`http://127.0.0.1:${p.port}/api/keys/${encodeURIComponent(k)}`, { signal: AbortSignal.timeout(5000) });
  return r.ok ? (await r.json()).value.data : null;
}
async function stop(p) { if (p.exit === null) { p.child.kill(); await p.done; } }

const results = [];
function check(id, title, ok, detail) {
  results.push({ id, title, pass: !!ok, detail });
  console.log(`  ${ok ? "ok  " : "FAIL"}  [${id}] ${title}${detail ? `  (${detail})` : ""}`);
}
const lastLine = (s) => s.trim().split(/\r?\n/).pop() ?? "";

// 1. Same database, different port.
{
  const a = start(dbPath("one"), await freePort());
  check("O7.1a", "first instance starts", await healthy(a));
  const okW = await put(a, "before", "1");
  const b = start(dbPath("one"), await freePort());
  await settle(b);
  const bErr = lastLine(b.stderr);
  check("O7.1b", "second instance on the same database exits with an error", b.exit !== null && b.exit !== 0, `exit ${b.exit} after ${b.exitMs} ms`);
  check("O7.1c", "its error says the database is locked", /locked/i.test(b.stderr), bErr);
  check("O7.1d", "first instance still healthy and accepts writes", okW && (await healthy(a, 2000)) && (await put(a, "after", "2")));
  check("O7.1e", "first instance's data intact", (await get(a, "before")) === "1" && (await get(a, "after")) === "2");
  await stop(a);
  // 5. After the first exits, the lock is free (O5 with a killed nexus-api).
  const c = start(dbPath("one"), await freePort());
  check("O7.5", "after the first is killed, a new instance opens the database with all data", (await healthy(c)) && (await get(c, "after")) === "2");
  await stop(c);
}

// 2. Same port, different database.
{
  const port = await freePort();
  const a = start(dbPath("two-a"), port);
  check("O7.2a", "first instance starts", await healthy(a));
  await put(a, "k", "a");
  const b = start(dbPath("two-b"), port);
  await settle(b);
  check("O7.2b", "second instance on the same port exits with an error", b.exit !== null && b.exit !== 0, `exit ${b.exit}: ${lastLine(b.stderr)}`);
  check("O7.2c", "first instance still answers on that port with its own data", (await get(a, "k")) === "a");
  const c = start(dbPath("two-b"), await freePort());
  check("O7.2d", "the second instance released its database when it exited", await healthy(c));
  await stop(c);
  await stop(a);
}

// 3. Both started at the same instant on one database, repeated.
{
  let exactlyOne = 0;
  const details = [];
  for (let i = 0; i < RACES; i++) {
    const db = dbPath(`race-${i}`);
    const [a, b] = [start(db, await freePort()), start(db, await freePort())];
    const [ha, hb] = await Promise.all([healthy(a, 20000), healthy(b, 20000)]);
    const loser = ha && !hb ? b : hb && !ha ? a : null;
    let ok = false;
    if (loser) {
      await settle(loser);
      ok = loser.exit !== null && loser.exit !== 0 && /locked/i.test(loser.stderr);
    }
    if (ok) exactlyOne++;
    details.push({ i, aHealthy: ha, bHealthy: hb, loserExit: loser?.exit ?? null, loserMs: loser?.exitMs ?? null });
    await stop(a); await stop(b);
  }
  const ms = details.map((d) => d.loserMs).filter((x) => x !== null);
  check("O7.3", `simultaneous start: exactly one instance serves, the other exits "locked" (${RACES} races)`, exactlyOne === RACES,
    `${exactlyOne}/${RACES}; loser exit after ${Math.min(...ms)}–${Math.max(...ms)} ms`);
  results.at(-1).races = details;
}

const failed = results.filter((r) => !r.pass).length;
console.log(`\n${results.length - failed} passed, ${failed} failed (real nexus-api processes, disposable databases in ${DIR})`);
const out = arg("json");
if (out) writeFileSync(out, JSON.stringify({ when: new Date().toISOString(), real_processes: true, results }, null, 2));
process.exit(failed ? 1 : 0);
