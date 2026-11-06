// The dashboard's only way to talk to NEXUS. `liveService` calls the real
// nexus-api adapter (src/cmd/nexus-api); `demoService` (./demo.ts) is an
// explicitly labelled stand-in used only when the user turns demo mode on.
// Both implement this interface so pages never know which one they use, and
// query keys are prefixed with the mode so the two never share a cache.
import { API_BASE_URL, request, keyPath } from "./client";
import type { z } from "zod";
import * as S from "./schemas";
import * as X from "./xray-schemas";

export type Encoding = "utf8" | "base64";

export interface ListParams {
  prefix: string;
  after: S.EncodedBytes | null;
  limit: number;
}

export interface NexusService {
  readonly mode: "live" | "demo";
  health(): Promise<S.Health>;
  overview(): Promise<S.Overview>;
  activity(limit?: number): Promise<S.Activity>;
  metrics(windowSeconds: number, stepSeconds: number): Promise<S.Metrics>;
  listKeys(p: ListParams): Promise<S.KeyList>;
  getKey(key: string): Promise<S.GetResult>;
  putKey(key: string, value: string, encoding: Encoding): Promise<S.WriteResult>;
  deleteKey(key: string): Promise<S.WriteResult>;
  compact(): Promise<S.CompactResult>;
  verify(): Promise<S.VerifyResult>;
  storage(): Promise<S.Storage>;
  recovery(): Promise<S.Recovery>;
  demoReset(keys: number): Promise<S.DemoState>;
  demoCrash(writes: number, killAfterMs: number): Promise<S.DemoState>;
  demoInspect(): Promise<S.DemoState>;
  demoTear(bytes: number): Promise<S.DemoState>;
  demoRecover(): Promise<S.DemoState>;
  tortureModes(): Promise<S.TortureModes>;
  scenarioCatalog(): Promise<S.ScenarioCatalog>;
  listRuns(): Promise<S.RunList>;
  startRun(mode: string, seed: string, iterations: number): Promise<S.RunStatus>;
  startScenarios(only: string[]): Promise<S.RunStatus>;
  runStatus(id: string): Promise<S.RunStatus>;
  runLog(id: string, lines: number): Promise<S.RunLog>;
  cancelRun(id: string): Promise<S.RunStatus>;
  evidence(): Promise<S.EvidenceList>;
  evidenceFileUrl(name: string): string | null;
  benchmark(): Promise<S.Benchmark>;
  databases(): Promise<S.DbList>;
  openDatabase(name: string, create: boolean): Promise<S.OpenDbResult>;
  batch(ops: BatchOp[]): Promise<S.WriteResult>;
  dump(q: string, limit: number): Promise<S.Dump>;
  frames(limit: number): Promise<S.LogMap>;
  demoFrames(limit: number): Promise<S.LogMap>;
  mutantCatalog(): Promise<S.MutantCatalog>;
  startMutants(only: string[], iterations: number): Promise<S.RunStatus>;
  replay(id: string, iteration: number): Promise<S.RunStatus>;
  startBench(cfg: { n: number; runs: number; value_size: number; batch_size: number }): Promise<S.RunStatus>;
  /** A recorded evidence file parsed with a schema (labelled RECORDED in the UI). */
  evidenceJson<T extends z.ZodType>(name: string, schema: T): Promise<z.infer<T>>;
  // ---- X-RAY (targets are IDs: "main", "demo", "lab:<case>"; never paths) ----
  xrayTargets(): Promise<X.XTargets>;
  xrayScan(target: string, noHash?: boolean): Promise<X.XScanResult>;
  xrayRepair(target: string): Promise<X.XScanResult>;
  xrayReports(kind?: string): Promise<X.XReportList>;
  xrayReport(id: string): Promise<X.XScanResult>;
  xrayExportUrl(id: string): string | null;
  xraySummary(): Promise<X.XSummary>;
  xrayLab(): Promise<X.XLab>;
  xrayLabReset(): Promise<X.XLab>;
  xrayLabScanAll(): Promise<X.XLabScanAll>;
  // ---- Power-Cut Explorer and performance runs ----
  powercutInfo(): Promise<X.PowercutInfo>;
  startPowercut(cfg: X.PowercutConfig): Promise<S.RunStatus>;
  powercutImageXRay(runId: string, image: string): Promise<X.XReport>;
  perfHistory(): Promise<X.PerfHistory>;
  startPerf(cfg: { only: string[]; quick: boolean }): Promise<S.RunStatus>;
}

export interface BatchOp {
  op: "put" | "del";
  key: string;
  value?: string;
}

/** Base64 of the UTF-8 bytes of s (for re-encoding a text prefix next to a base64 cursor). */
export function utf8ToBase64(s: string): string {
  const bytes = new TextEncoder().encode(s);
  let bin = "";
  bytes.forEach((b) => (bin += String.fromCharCode(b)));
  return btoa(bin);
}

function listQuery(p: ListParams): string {
  const q = new URLSearchParams({ limit: String(p.limit) });
  // prefix and after share one `encoding` parameter; a binary cursor forces base64 for both.
  if (p.after && p.after.encoding === "base64") {
    q.set("encoding", "base64");
    if (p.prefix) q.set("prefix", utf8ToBase64(p.prefix));
    q.set("after", p.after.data);
  } else {
    if (p.prefix) q.set("prefix", p.prefix);
    if (p.after) q.set("after", p.after.data);
  }
  return q.toString();
}

const LONG = 120_000; // compaction / verification of a large file

export const liveService: NexusService = {
  mode: "live",
  health: () => request("/api/health", { schema: S.Health, timeoutMs: 4_000 }),
  overview: () => request("/api/overview", { schema: S.Overview }),
  activity: (limit = 50) => request(`/api/activity?limit=${limit}`, { schema: S.Activity }),
  metrics: (w, s) => request(`/api/metrics?window=${w}&step=${s}`, { schema: S.Metrics }),
  listKeys: (p) => request(`/api/keys?${listQuery(p)}`, { schema: S.KeyList, timeoutMs: 30_000 }),
  getKey: (key) => request(`/api/keys/${keyPath(key)}`, { schema: S.GetResult }),
  putKey: (key, value, encoding) =>
    request(`/api/keys/${keyPath(key)}`, { method: "PUT", body: { value, encoding }, schema: S.WriteResult, timeoutMs: 30_000 }),
  deleteKey: (key) => request(`/api/keys/${keyPath(key)}`, { method: "DELETE", schema: S.WriteResult, timeoutMs: 30_000 }),
  compact: () => request("/api/compact", { method: "POST", schema: S.CompactResult, timeoutMs: LONG }),
  verify: () => request("/api/verify", { method: "POST", schema: S.VerifyResult, timeoutMs: LONG }),
  storage: () => request("/api/storage", { schema: S.Storage }),
  recovery: () => request("/api/recovery", { schema: S.Recovery }),
  demoReset: (keys) => request("/api/recovery/demo/reset", { method: "POST", body: { keys }, schema: S.DemoState, timeoutMs: 60_000 }),
  demoCrash: (writes, kill_after_ms) =>
    request("/api/recovery/demo/crash", { method: "POST", body: { writes, kill_after_ms }, schema: S.DemoState, timeoutMs: 60_000 }),
  demoInspect: () => request("/api/recovery/demo/inspect", { method: "POST", schema: S.DemoState, timeoutMs: LONG }),
  demoTear: (bytes) => request("/api/recovery/demo/tear", { method: "POST", body: { bytes }, schema: S.DemoState }),
  demoRecover: () => request("/api/recovery/demo/recover", { method: "POST", schema: S.DemoState, timeoutMs: LONG }),
  tortureModes: () => request("/api/torture/modes", { schema: S.TortureModes, timeoutMs: 15_000 }),
  scenarioCatalog: () => request("/api/torture/scenarios", { schema: S.ScenarioCatalog }),
  listRuns: () => request("/api/torture/runs", { schema: S.RunList }),
  startRun: (mode, seed, iterations) =>
    request("/api/torture/runs", { method: "POST", body: { mode, seed, iterations }, schema: S.RunStatus, timeoutMs: 20_000 }),
  startScenarios: (only) =>
    request("/api/torture/scenarios", { method: "POST", body: { only }, schema: S.RunStatus, timeoutMs: 20_000 }),
  runStatus: (id) => request(`/api/torture/runs/${encodeURIComponent(id)}`, { schema: S.RunStatus }),
  runLog: (id, lines) => request(`/api/torture/runs/${encodeURIComponent(id)}/log?lines=${lines}`, { schema: S.RunLog }),
  cancelRun: (id) =>
    request(`/api/torture/runs/${encodeURIComponent(id)}/cancel`, { method: "POST", schema: S.RunStatus, timeoutMs: 20_000 }),
  evidence: () => request("/api/evidence", { schema: S.EvidenceList }),
  evidenceFileUrl: (name) => `${API_BASE_URL}/api/evidence/${name.split("/").map(encodeURIComponent).join("/")}`,
  benchmark: () => request("/api/benchmarks/latest", { schema: S.Benchmark }),
  databases: () => request("/api/databases", { schema: S.DbList }),
  openDatabase: (name, create) =>
    request("/api/databases/open", { method: "POST", body: { name, create }, schema: S.OpenDbResult, timeoutMs: 120_000 }),
  batch: (ops) => request("/api/batch", { method: "POST", body: { ops }, schema: S.WriteResult, timeoutMs: 60_000 }),
  dump: (q, limit) => request(`/api/dump?limit=${limit}${q ? `&q=${encodeURIComponent(q)}` : ""}`, { schema: S.Dump, timeoutMs: 60_000 }),
  frames: (limit) => request(`/api/storage/frames?limit=${limit}`, { schema: S.LogMap, timeoutMs: 60_000 }),
  demoFrames: (limit) => request(`/api/recovery/demo/frames?limit=${limit}`, { schema: S.LogMap, timeoutMs: 60_000 }),
  mutantCatalog: () => request("/api/torture/mutants", { schema: S.MutantCatalog }),
  startMutants: (only, iterations) =>
    request("/api/torture/mutants", { method: "POST", body: { only, iterations }, schema: S.RunStatus, timeoutMs: 20_000 }),
  replay: (id, iteration) =>
    request(`/api/torture/runs/${encodeURIComponent(id)}/replay`, { method: "POST", body: { iteration }, schema: S.RunStatus, timeoutMs: 20_000 }),
  startBench: (cfg) => request("/api/bench/runs", { method: "POST", body: cfg, schema: S.RunStatus, timeoutMs: 20_000 }),
  evidenceJson: (name, schema) =>
    request(`/api/evidence/${name.split("/").map(encodeURIComponent).join("/")}`, { schema, timeoutMs: 20_000 }),
  xrayTargets: () => request("/api/xray/targets", { schema: X.XTargets }),
  xrayScan: (target, noHash = false) =>
    request("/api/xray/scan", { method: "POST", body: { target, no_hash: noHash }, schema: X.XScanResult, timeoutMs: LONG }),
  xrayRepair: (target) => request("/api/xray/repair", { method: "POST", body: { target, confirm: true }, schema: X.XScanResult, timeoutMs: LONG }),
  xrayReports: (kind) => request(`/api/xray/reports${kind ? `?kind=${encodeURIComponent(kind)}` : ""}`, { schema: X.XReportList }),
  xrayReport: (id) => request(`/api/xray/reports/${encodeURIComponent(id)}`, { schema: X.XScanResult }),
  xrayExportUrl: (id) => `${API_BASE_URL}/api/xray/reports/${encodeURIComponent(id)}/export`,
  xraySummary: () => request("/api/xray/summary", { schema: X.XSummary }),
  xrayLab: () => request("/api/xray/lab", { schema: X.XLab }),
  xrayLabReset: () => request("/api/xray/lab/reset", { method: "POST", schema: X.XLab, timeoutMs: 60_000 }),
  xrayLabScanAll: () => request("/api/xray/lab/scan-all", { method: "POST", schema: X.XLabScanAll, timeoutMs: LONG }),
  powercutInfo: () => request("/api/powercut", { schema: X.PowercutInfo, timeoutMs: 20_000 }),
  startPowercut: (cfg) => request("/api/powercut/runs", { method: "POST", body: cfg, schema: S.RunStatus, timeoutMs: 20_000 }),
  powercutImageXRay: (runId, image) =>
    request(`/api/powercut/runs/${encodeURIComponent(runId)}/images/${encodeURIComponent(image)}/xray`, { schema: X.XReport }),
  perfHistory: () => request("/api/perf/history", { schema: X.PerfHistory }),
  startPerf: (cfg) => request("/api/perf/runs", { method: "POST", body: cfg, schema: S.RunStatus, timeoutMs: 20_000 }),
};
