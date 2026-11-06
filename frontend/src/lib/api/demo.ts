// DEMO DATA. An in-browser stand-in for nexus-api, used only when the user
// switches demo mode on (the UI then shows a "DEMO DATA" badge everywhere).
// It keeps keys in a Map inside this tab and nothing else: it is not the
// engine, it is not durable, and its sizes are a rough model of the log
// (header + key + value per write). Recovery drills, torture runs, recorded
// evidence and benchmarks are not simulated: they answer "unavailable in
// demo mode", so no invented test result can appear on screen.
import { ApiError } from "./client";
import type { ListParams, NexusService } from "./service";
import type * as S from "./schemas";

const SUPERBLOCK = 32;
const FRAME_OVERHEAD = 24;
const started = new Date();

const store = new Map<string, string>();
let fileSize = SUPERBLOCK;
let lastCompaction: S.CompactResult | null = null;
let lastVerify: S.VerifyResult | null = null;
const verifies: S.VerifyResult[] = [];
const entries: S.ActivityEntry[] = [];
const totals: Record<string, S.OpTotals> = {};
const perSecond = new Map<number, Record<string, S.OpTotals>>();
let nextId = 0;

function seed() {
  if (store.size > 0) return;
  const samples: [string, string][] = [
    ["user:1001", JSON.stringify({ name: "Asha", plan: "pro", seats: 4 })],
    ["user:1002", JSON.stringify({ name: "Ravi", plan: "free", seats: 1 })],
    ["config/feature-flags", JSON.stringify({ compaction: "auto", sync: "always" })],
    ["session:7f3a", "expires=2026-12-01T00:00:00Z"],
    ["greeting", "hello from demo mode"],
  ];
  for (const [k, v] of samples) {
    store.set(k, v);
    fileSize += FRAME_OVERHEAD + k.length + v.length;
  }
}
seed();

const bytes = (s: string) => new TextEncoder().encode(s).length;
const liveBytes = () => [...store].reduce((n, [k, v]) => n + bytes(k) + bytes(v), 0);
const enc = (s: string): S.EncodedBytes => ({ data: s, encoding: "utf8" });
const now = () => new Date().toISOString();

function record(op: string, key: string, result: string, ms: number, detail = "") {
  const isErr = result !== "ok" && result !== "notfound";
  const sec = Math.floor(Date.now() / 1000);
  const bucket = perSecond.get(sec) ?? {};
  perSecond.set(sec, bucket);
  for (const t of [bucket, totals]) {
    const cur = (t[op] ??= { count: 0, errors: 0, total_ms: 0, max_ms: 0 });
    cur.count++;
    if (isErr) cur.errors++;
    cur.total_ms += ms;
    cur.max_ms = Math.max(cur.max_ms, ms);
  }
  entries.push({ id: ++nextId, at: now(), op, key, result, elapsed_ms: ms, detail });
  if (entries.length > 200) entries.shift();
}

const delay = <T,>(v: T) => new Promise<T>((r) => setTimeout(() => r(v), 60 + Math.random() * 80));
const stats = (): S.Stats => ({ keys: store.size, live_bytes: liveBytes(), size: fileSize, max_bytes: 0 });
const unavailable = (what: string) =>
  Promise.reject(new ApiError("unavailable-in-demo", `${what} needs the real nexus-api; demo mode does not simulate it.`));

function keyError(key: string): Promise<never> | null {
  if (key.length === 0) return Promise.reject(new ApiError("http", "empty key", { status: 400, errorClass: "invalid" }));
  if (bytes(key) > 1024) return Promise.reject(new ApiError("http", "key longer than 1024 bytes", { status: 400, errorClass: "invalid" }));
  return null;
}

const lastOpen: S.LastOpen = {
  at: started.toISOString(),
  duration_ms: 0,
  created: true,
  rules: ["demo: no file was opened"],
  tail_rule: "none",
  discarded_bytes: 0,
  frames: 0,
  warnings: [],
};

export const demoService: NexusService = {
  mode: "demo",
  health: () =>
    delay({
      status: "ok",
      db_path: "(demo, in this browser tab)",
      db_open: true,
      started_at: started.toISOString(),
      tools: { kvdb: null, nexus_torture: null },
      torture_running: false,
      measured_at: now(),
    }),
  overview: () =>
    delay({
      status: "ok",
      db_path: "(demo, in this browser tab)",
      stats: stats(),
      last_open: lastOpen,
      last_compaction: lastCompaction,
      last_verify: lastVerify,
      limits: { max_key_bytes: 1024, max_value_bytes: 1 << 20, max_batch_ops: 10000 },
      adapter: {
        started_at: started.toISOString(),
        uptime_seconds: (Date.now() - started.getTime()) / 1000,
        requests: Object.values(totals).reduce((n, t) => n + t.count, 0),
        by_op: { ...totals },
      },
      source: "DEMO DATA (in-browser)",
      measured_at: now(),
    }),
  activity: (limit = 50) =>
    delay({ scope: "demo", description: "DEMO DATA", items: entries.slice(-limit).reverse(), measured_at: now() }),
  metrics: (window, step) => {
    const n = Math.max(1, Math.floor(window / step));
    const t = Math.floor(Date.now() / 1000);
    const last = t - (t % step);
    const buckets = [];
    for (let i = n - 1; i >= 0; i--) {
      const start = last - i * step;
      const ops: Record<string, S.OpTotals> = {};
      for (let s = start; s < start + step; s++) {
        for (const [op, v] of Object.entries(perSecond.get(s) ?? {})) {
          const c = (ops[op] ??= { count: 0, errors: 0, total_ms: 0, max_ms: 0 });
          c.count += v.count;
          c.errors += v.errors;
          c.total_ms += v.total_ms;
          c.max_ms = Math.max(c.max_ms, v.max_ms);
        }
      }
      buckets.push({ start: new Date(start * 1000).toISOString(), ops });
    }
    return delay({
      scope: "demo",
      description: "DEMO DATA",
      window_seconds: window,
      step_seconds: step,
      buckets,
      totals: { ...totals },
      total_requests: Object.values(totals).reduce((a, b) => a + b.count, 0),
      since: started.toISOString(),
      measured_at: now(),
    });
  },
  listKeys: (p: ListParams) => {
    const keys = [...store.keys()].filter((k) => k.startsWith(p.prefix)).sort();
    const from = p.after ? keys.filter((k) => k > p.after!.data) : keys;
    const page = from.slice(0, p.limit);
    record("list", p.prefix, "ok", 0.1);
    return delay({
      items: page.map((k) => {
        const v = store.get(k)!;
        return { key: enc(k), value_size: bytes(v), value_preview: enc(v.slice(0, 120)), value_truncated: v.length > 120 };
      }),
      next_after: from.length > p.limit ? enc(page[page.length - 1]) : null,
      elapsed_ms: 0.1,
    });
  },
  getKey: (key) => {
    const bad = keyError(key);
    if (bad) return bad;
    const v = store.get(key);
    if (v === undefined) {
      record("get", key, "notfound", 0.05);
      return Promise.reject(new ApiError("http", "key not found", { status: 404, errorClass: "notfound" }));
    }
    record("get", key, "ok", 0.05);
    return delay({ key: enc(key), value: enc(v), size: bytes(v), elapsed_ms: 0.05 });
  },
  putKey: (key, value, encoding) => {
    const bad = keyError(key);
    if (bad) return bad;
    const v = encoding === "base64" ? atob(value) : value;
    if (bytes(v) > 1 << 20) {
      record("put", key, "too-large", 0.1);
      return Promise.reject(new ApiError("http", "value larger than 1 MiB", { status: 413, errorClass: "too-large" }));
    }
    const action = store.has(key) ? "updated" : "inserted";
    store.set(key, v);
    fileSize += FRAME_OVERHEAD + bytes(key) + bytes(v);
    record("put", key, "ok", 0.5, `${action}, ${bytes(v)} B`);
    return delay({ ok: true as const, durable: false, action, size: bytes(v), elapsed_ms: 0.5 });
  },
  deleteKey: (key) => {
    const bad = keyError(key);
    if (bad) return bad;
    const action = store.has(key) ? "deleted" : "absent";
    if (action === "deleted") fileSize += FRAME_OVERHEAD + bytes(key);
    store.delete(key);
    record("delete", key, "ok", 0.5, action);
    return delay({ ok: true as const, durable: false, action, elapsed_ms: 0.5 });
  },
  compact: () => {
    const before = fileSize;
    fileSize = SUPERBLOCK + [...store].reduce((n, [k, v]) => n + FRAME_OVERHEAD + bytes(k) + bytes(v), 0);
    lastCompaction = { at: now(), ok: true, size_before: before, size_after: fileSize, keys: store.size, elapsed_ms: 2, error: null };
    record("compact", "", "ok", 2);
    return delay(lastCompaction);
  },
  verify: () => {
    lastVerify = {
      at: now(), ok: true, keys: store.size, frames: 0, size: fileSize, live_bytes: liveBytes(), elapsed_ms: 1, error: null,
      method: "DEMO DATA: nothing is read from disk in demo mode",
    };
    verifies.unshift(lastVerify);
    record("verify", "", "ok", 1);
    return delay(lastVerify);
  },
  storage: () =>
    delay({
      directory: "(demo, in this browser tab)",
      files: [
        { role: "data", name: "demo.kv", exists: true, size: fileSize, modified: now(), meaning: "DEMO DATA: modelled size of the append-only log" },
      ],
      stats: stats(),
      last_compaction: lastCompaction,
      layout: {
        kind: "single-file append-only log",
        wal: "not applicable: the data file is itself the log",
        levels: "not applicable: no LSM levels",
        auto_compaction: "not modelled in demo mode",
      },
      measured_at: now(),
    }),
  recovery: () => unavailable("Recovery inspection"),
  demoReset: () => unavailable("The crash drill"),
  demoCrash: () => unavailable("The crash drill"),
  demoInspect: () => unavailable("The crash drill"),
  demoTear: () => unavailable("The crash drill"),
  demoRecover: () => unavailable("The crash drill"),
  tortureModes: () => unavailable("The torture lab"),
  scenarioCatalog: () => unavailable("The torture lab"),
  listRuns: () => unavailable("The torture lab"),
  startRun: () => unavailable("The torture lab"),
  startScenarios: () => unavailable("The torture lab"),
  runStatus: () => unavailable("The torture lab"),
  runLog: () => unavailable("The torture lab"),
  cancelRun: () => unavailable("The torture lab"),
  evidence: () => unavailable("Recorded evidence"),
  evidenceFileUrl: () => null,
  benchmark: () => unavailable("The recorded benchmark"),
  databases: () => unavailable("Opening databases"),
  openDatabase: () => unavailable("Opening databases"),
  batch: () => unavailable("BATCH"),
  dump: () => unavailable("DUMP"),
  frames: () => unavailable("The log map"),
  demoFrames: () => unavailable("The log map"),
  mutantCatalog: () => unavailable("Mutation testing"),
  startMutants: () => unavailable("Mutation testing"),
  replay: () => unavailable("Replay"),
  startBench: () => unavailable("Benchmarks"),
  evidenceJson: () => unavailable("Recorded evidence"),
  xrayTargets: () => unavailable("X-RAY"),
  xrayScan: () => unavailable("X-RAY"),
  xrayRepair: () => unavailable("X-RAY"),
  xrayReports: () => unavailable("X-RAY"),
  xrayReport: () => unavailable("X-RAY"),
  xrayExportUrl: () => null,
  xraySummary: () => unavailable("X-RAY"),
  xrayLab: () => unavailable("The X-RAY lab"),
  xrayLabReset: () => unavailable("The X-RAY lab"),
  xrayLabScanAll: () => unavailable("The X-RAY lab"),
  powercutInfo: () => unavailable("The Power-Cut Explorer"),
  startPowercut: () => unavailable("The Power-Cut Explorer"),
  powercutImageXRay: () => unavailable("The Power-Cut Explorer"),
  perfHistory: () => unavailable("The optimization history"),
  startPerf: () => unavailable("Performance runs"),
};
