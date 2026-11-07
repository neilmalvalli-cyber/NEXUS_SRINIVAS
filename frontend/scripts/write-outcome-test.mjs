// Judge attacks N2/N3/N4: how the real dashboard HTTP client (src/lib/api/client.ts,
// run unmodified through Node's type stripping) classifies write requests that
// end without a definite answer. A local fault server (not nexus-api) simulates
// each network failure deterministically:
//   * the body is received, then the connection is reset (N2: committed, answer lost)
//   * the body is received and the server never answers (N3: client timeout)
//   * a bare 502, an unreadable 200, a definite engine error, an engine I/O error
//   * connection refused (nothing listening)
// Every write in those states must be reported as outcomeUnknown (never "failed"),
// definite answers must not be, reads must not be, and request() must send each
// request exactly once (no hidden retry that could duplicate a write).
//
// Usage: node scripts/write-outcome-test.mjs [--json out.json]
import http from "node:http";
import { registerHooks } from "node:module";
import { writeFileSync } from "node:fs";

// Resolve the frontend's extensionless TS imports ("./schemas") to .ts files.
registerHooks({
  resolve(spec, ctx, next) {
    try {
      return next(spec, ctx);
    } catch (e) {
      if (spec.startsWith(".") && !/\.[cm]?[jt]s$/.test(spec)) return next(spec + ".ts", ctx);
      throw e;
    }
  },
});

const hits = new Map();
const committed = new Set();
const server = http.createServer((req, res) => {
  const name = decodeURIComponent(req.url.replace(/^\/api\/keys\//, ""));
  hits.set(name, (hits.get(name) ?? 0) + 1);
  const chunks = [];
  req.on("data", (c) => chunks.push(c));
  req.on("end", () => {
    if (req.method !== "GET") committed.add(name); // the "server" applied the write
    switch (name) {
      case "reset-after-commit":
        req.socket.destroy(); // N2: no response bytes at all
        return;
      case "hang-after-commit":
        return; // N3: never answer
      case "half-response":
        res.writeHead(200, { "Content-Type": "application/json", "Content-Length": "500" });
        res.write('{"action":"sto');
        setTimeout(() => req.socket.destroy(), 20);
        return;
      case "bare-502":
        res.writeHead(502, { "Content-Type": "text/plain" }).end("Bad Gateway");
        return;
      case "bad-json-200":
        res.writeHead(200, { "Content-Type": "application/json" }).end("not json");
        return;
      case "definite-invalid":
        committed.delete(name);
        res.writeHead(400, { "Content-Type": "application/json" }).end(JSON.stringify({ error: { class: "invalid", message: "key too long" } }));
        return;
      case "engine-io":
        res.writeHead(500, { "Content-Type": "application/json" }).end(JSON.stringify({ error: { class: "io", message: "write failed", outcome_unknown: true } }));
        return;
      case "bare-404":
        committed.delete(name);
        res.writeHead(404, { "Content-Type": "text/plain" }).end("no route");
        return;
      default:
        res.writeHead(500).end();
    }
  });
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const port = server.address().port;

// A port with nothing listening (bind, read the port, close).
const closedPort = await new Promise((r) => {
  const s = http.createServer().listen(0, "127.0.0.1", () => {
    const p = s.address().port;
    s.close(() => r(p));
  });
});

process.env.NEXT_PUBLIC_NEXUS_API_URL = `http://127.0.0.1:${port}`;
const client = await import("../src/lib/api/client.ts");
const { ErrorEnvelope } = await import("../src/lib/api/schemas.ts");
const { z } = await import("zod");
const WriteResult = z.object({ action: z.string() });

async function attempt(name, { method = "PUT", write = true, base, timeoutMs = 400 } = {}) {
  const path = `/api/keys/${encodeURIComponent(name)}`;
  const opts = { method, schema: WriteResult, timeoutMs, write };
  if (method !== "GET" && method !== "DELETE") opts.body = { value: "v", encoding: "utf8" };
  try {
    if (base) {
      // Same client code against another origin: re-import with a different env
      // (a separate module instance, so its own describeError is used).
      process.env.NEXT_PUBLIC_NEXUS_API_URL = base;
      const c2 = await import(`../src/lib/api/client.ts?base=${encodeURIComponent(base)}`);
      process.env.NEXT_PUBLIC_NEXUS_API_URL = `http://127.0.0.1:${port}`;
      try {
        await c2.request(path, opts);
      } catch (e) {
        return { ok: false, kind: e.kind, outcomeUnknown: e.outcomeUnknown, status: e.status, text: c2.describeError(e) };
      }
    } else {
      await client.request(path, opts);
    }
    return { ok: true };
  } catch (e) {
    return { ok: false, kind: e.kind, outcomeUnknown: e.outcomeUnknown, status: e.status, text: client.describeError(e) };
  }
}

const cases = [
  ["N2", "PUT: body received and committed, connection reset before any response", "reset-after-commit", {}, { kind: "network", unknown: true }],
  ["N2", "DELETE: committed, connection reset", "reset-after-commit", { method: "DELETE" }, { kind: "network", unknown: true }],
  ["N2", "BATCH-style POST: committed, connection reset", "reset-after-commit", { method: "POST" }, { kind: "network", unknown: true }],
  ["N2", "PUT: committed, response cut off mid-body", "half-response", {}, { unknown: true }],
  ["N3", "PUT: committed, server never answers, client timeout", "hang-after-commit", {}, { kind: "timeout", unknown: true }],
  ["N2", "PUT: bare 502 from something in between", "bare-502", {}, { kind: "malformed", unknown: true }],
  ["N2", "PUT: HTTP 200 with an unreadable body", "bad-json-200", {}, { kind: "malformed", unknown: true }],
  ["N2", "PUT: connection refused (browser cannot tell it from a reset)", "x", { base: `http://127.0.0.1:${closedPort}` }, { kind: "network", unknown: true }],
  ["ctl", "PUT: definite engine rejection (400 invalid) is a plain failure", "definite-invalid", {}, { kind: "http", unknown: false }],
  ["ctl", "PUT: bare 404 is a plain failure", "bare-404", {}, { kind: "malformed", unknown: false }],
  ["ctl", "PUT: engine I/O error with outcome_unknown stays unknown", "engine-io", {}, { kind: "http", unknown: true }],
  ["ctl", "GET: connection reset is a read failure, not an unknown write", "reset-after-commit", { method: "GET", write: false }, { kind: "network", unknown: false }],
  ["ctl", "GET: timeout is not an unknown write", "hang-after-commit", { method: "GET", write: false }, { kind: "timeout", unknown: false }],
];

const results = [];
for (const [id, title, name, opts, want] of cases) {
  const before = hits.get(name) ?? 0;
  const got = await attempt(name, opts);
  const sent = (hits.get(name) ?? 0) - before;
  const problems = [];
  if (got.ok) problems.push("reported success");
  if (want.kind && got.kind !== want.kind) problems.push(`kind ${got.kind}, want ${want.kind}`);
  if (got.outcomeUnknown !== want.unknown) problems.push(`outcomeUnknown ${got.outcomeUnknown}, want ${want.unknown}`);
  if (want.unknown && !/outcome unknown/.test(got.text ?? "")) problems.push("message does not say outcome unknown");
  if (!opts.base && sent !== 1) problems.push(`server saw ${sent} requests, want exactly 1 (no retry)`);
  const pass = problems.length === 0;
  results.push({ id, title, pass, sent: opts.base ? 0 : sent, got, problems });
  console.log(`  ${pass ? "ok  " : "FAIL"}  [${id}] ${title}${pass ? "" : `: ${problems.join("; ")}`}`);
}

// ErrorEnvelope sanity: the adapter's default for outcome_unknown is false.
const env = ErrorEnvelope.parse({ error: { class: "invalid", message: "m" } });
console.log(`  ${env.error.outcome_unknown === false ? "ok  " : "FAIL"}  [ctl] error envelope defaults outcome_unknown to false`);

server.close();
const failed = results.filter((r) => !r.pass).length;
console.log(`\n${results.length - failed} passed, ${failed} failed (simulated network faults against a local fault server; client code is the real dashboard client)`);
const j = process.argv.indexOf("--json");
if (j > 0) writeFileSync(process.argv[j + 1], JSON.stringify({ when: new Date().toISOString(), node: process.version, simulated: true, results }, null, 2));
process.exit(failed ? 1 : 0);
