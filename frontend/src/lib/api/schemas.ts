// Zod schemas for every nexus-api response the dashboard reads. They mirror
// the JSON written by src/cmd/nexus-api (see docs/FRONTEND_INTEGRATION.md);
// a response that does not match is reported as "malformed", never guessed.
import { z } from "zod";

export const ErrorEnvelope = z.object({
  error: z.object({
    class: z.string(),
    message: z.string(),
    outcome_unknown: z.boolean().optional().default(false),
  }),
});

export const EncodedBytes = z.object({
  data: z.string(),
  encoding: z.enum(["utf8", "base64"]),
});
export type EncodedBytes = z.infer<typeof EncodedBytes>;

const ApiErr = z.object({ class: z.string(), message: z.string() }).nullable();

export const Health = z.object({
  status: z.string(),
  db_path: z.string(),
  db_open: z.boolean(),
  started_at: z.string(),
  tools: z.object({
    kvdb: z.string().nullable(),
    nexus_torture: z.string().nullable(),
    kvdb_mutant: z.string().nullable().optional(),
    kvdb_bench: z.string().nullable().optional(),
    kvdb_perf: z.string().nullable().optional(),
    nexus_powercut: z.string().nullable().optional(),
  }),
  torture_running: z.boolean(),
  measured_at: z.string(),
});
export type Health = z.infer<typeof Health>;

export const Stats = z.object({
  keys: z.number(),
  live_bytes: z.number(),
  size: z.number(),
  max_bytes: z.number(),
});
export type Stats = z.infer<typeof Stats>;

export const LastOpen = z.object({
  at: z.string(),
  duration_ms: z.number(),
  created: z.boolean(),
  rules: z.array(z.string()).nullable(),
  tail_rule: z.string(),
  discarded_bytes: z.number(),
  frames: z.number(),
  warnings: z.array(z.string()).nullable(),
});
export type LastOpen = z.infer<typeof LastOpen>;

export const CompactResult = z.object({
  at: z.string(),
  ok: z.boolean(),
  size_before: z.number(),
  size_after: z.number(),
  keys: z.number(),
  elapsed_ms: z.number(),
  error: ApiErr,
});
export type CompactResult = z.infer<typeof CompactResult>;

export const VerifyResult = z.object({
  at: z.string(),
  ok: z.boolean(),
  keys: z.number(),
  frames: z.number(),
  size: z.number(),
  live_bytes: z.number(),
  elapsed_ms: z.number(),
  error: ApiErr,
  method: z.string(),
});
export type VerifyResult = z.infer<typeof VerifyResult>;

export const OpTotals = z.object({
  count: z.number(),
  errors: z.number(),
  total_ms: z.number(),
  max_ms: z.number(),
});
export type OpTotals = z.infer<typeof OpTotals>;

export const Overview = z.object({
  status: z.string(),
  db_path: z.string(),
  stats: Stats,
  format_version: z.number().optional(),
  database: z.string().optional(),
  last_open: LastOpen,
  last_compaction: CompactResult.nullable(),
  last_verify: VerifyResult.nullable(),
  limits: z.object({
    max_key_bytes: z.number(),
    max_value_bytes: z.number(),
    max_batch_ops: z.number(),
  }),
  adapter: z.object({
    started_at: z.string(),
    uptime_seconds: z.number(),
    requests: z.number(),
    by_op: z.record(z.string(), OpTotals),
  }),
  source: z.string(),
  measured_at: z.string(),
});
export type Overview = z.infer<typeof Overview>;

export const ActivityEntry = z.object({
  id: z.number(),
  at: z.string(),
  op: z.string(),
  key: z.string().optional(),
  result: z.string(),
  elapsed_ms: z.number(),
  detail: z.string().optional(),
});
export type ActivityEntry = z.infer<typeof ActivityEntry>;

export const Activity = z.object({
  scope: z.string(),
  description: z.string(),
  items: z.array(ActivityEntry),
  measured_at: z.string(),
});
export type Activity = z.infer<typeof Activity>;

export const Metrics = z.object({
  scope: z.string(),
  description: z.string(),
  window_seconds: z.number(),
  step_seconds: z.number(),
  buckets: z.array(z.object({ start: z.string(), ops: z.record(z.string(), OpTotals) })),
  totals: z.record(z.string(), OpTotals),
  total_requests: z.number(),
  since: z.string(),
  measured_at: z.string(),
});
export type Metrics = z.infer<typeof Metrics>;

export const KeyItem = z.object({
  key: EncodedBytes,
  value_size: z.number(),
  value_preview: EncodedBytes,
  value_truncated: z.boolean(),
});
export type KeyItem = z.infer<typeof KeyItem>;

export const KeyList = z.object({
  items: z.array(KeyItem),
  next_after: EncodedBytes.nullable(),
  elapsed_ms: z.number(),
  note: z.string().optional(),
});
export type KeyList = z.infer<typeof KeyList>;

export const GetResult = z.object({
  key: EncodedBytes,
  value: EncodedBytes,
  size: z.number(),
  elapsed_ms: z.number(),
});
export type GetResult = z.infer<typeof GetResult>;

export const WriteResult = z.object({
  ok: z.literal(true),
  durable: z.boolean(),
  /** PUT: inserted | updated | written; DELETE: deleted | absent (missing key, nothing written). */
  action: z.string().optional(),
  size: z.number().optional(),
  ops: z.number().optional(),
  elapsed_ms: z.number(),
});
export type WriteResult = z.infer<typeof WriteResult>;

export const FileInfo = z.object({
  role: z.string(),
  name: z.string(),
  exists: z.boolean(),
  size: z.number(),
  modified: z.string().optional(),
  meaning: z.string(),
});
export type FileInfo = z.infer<typeof FileInfo>;

export const Storage = z.object({
  directory: z.string(),
  files: z.array(FileInfo),
  stats: Stats,
  last_compaction: CompactResult.nullable(),
  layout: z.object({
    kind: z.string(),
    wal: z.string(),
    levels: z.string(),
    auto_compaction: z.string(),
  }),
  measured_at: z.string(),
});
export type Storage = z.infer<typeof Storage>;

export const DemoCrash = z.object({
  at: z.string(),
  requested_writes: z.number(),
  kill_after_ms: z.number(),
  sent: z.number(),
  acked: z.number(),
  rejected: z.number(),
  unknown: z.number(),
  killed_at_ms: z.number(),
  exited_before_kill: z.boolean(),
  serve_stderr: z.string().optional(),
  acked_keys_sample: z.array(z.string()).nullable(),
  torn_by_hand: z.boolean(),
});
export type DemoCrash = z.infer<typeof DemoCrash>;

export const DemoInspect = z.object({
  at: z.string(),
  ok: z.boolean(),
  needs_repair: z.boolean(),
  missing: z.boolean(),
  keys: z.number(),
  frames: z.number(),
  size: z.number(),
  end: z.number(),
  live_bytes: z.number(),
  rules: z.array(z.string()).nullable(),
  tail_rule: z.string(),
  repairs: z.array(z.string()).nullable(),
  elapsed_ms: z.number(),
  error: ApiErr,
  method: z.string(),
});
export type DemoInspect = z.infer<typeof DemoInspect>;

export const DemoRecovery = z.object({
  at: z.string(),
  ok: z.boolean(),
  open: LastOpen,
  keys: z.number(),
  size: z.number(),
  file_verified: z.boolean(),
  acked_checked: z.number(),
  acked_missing: z.number(),
  acked_wrong_value: z.number(),
  missing_keys_sample: z.array(z.string()).nullable(),
  expectation: z.string(),
  error: ApiErr,
});
export type DemoRecovery = z.infer<typeof DemoRecovery>;

export const DemoState = z.object({
  db_path: z.string(),
  file: FileInfo,
  kvdb: z.string().nullable(),
  last_crash: DemoCrash.nullable(),
  last_inspect: DemoInspect.nullable(),
  last_recovery: DemoRecovery.nullable(),
  events: z
    .array(z.object({ at: z.string(), action: z.string(), summary: z.string(), ok: z.boolean() }))
    .nullable(),
});
export type DemoState = z.infer<typeof DemoState>;

export const Recovery = z.object({
  last_open: LastOpen,
  verify_history: z.array(VerifyResult).nullable(),
  demo: DemoState,
  read_only_check: z.string(),
});
export type Recovery = z.infer<typeof Recovery>;

export const TortureModes = z.object({
  modes: z.array(z.object({ name: z.string(), description: z.string() })),
  source: z.string(),
  max_iterations: z.number(),
});
export type TortureModes = z.infer<typeof TortureModes>;

export const ScenarioCatalog = z.object({
  scenarios: z.array(
    z.object({ id: z.string(), group: z.string(), title: z.string(), invariant: z.string(), method: z.string() }),
  ),
  source: z.string(),
});
export type ScenarioCatalog = z.infer<typeof ScenarioCatalog>;

export const RunState = z.enum(["running", "passed", "failed", "inconclusive", "interrupted", "error"]);
export type RunState = z.infer<typeof RunState>;

export const IterLine = z.object({
  iter: z.number(),
  verdict: z.string(),
  kill_after_ms: z.number(),
  kill_point: z.string(),
  sent: z.number(),
  acked_writes: z.number(),
  unknown: z.number(),
  gets: z.number(),
  keys: z.number(),
  oracle_ms: z.number(),
  fail_kind: z.string().optional(),
  failure: z.string().optional(),
});
export type IterLine = z.infer<typeof IterLine>;

export const RunStatus = z.object({
  run: z.object({
    id: z.string(),
    kind: z.enum(["run", "scenarios", "mutants", "bench", "powercut", "perf"]),
    mode: z.string().optional(),
    seed: z.string().optional(),
    iterations: z.number().optional(),
    only: z.array(z.string()).optional(),
    iteration: z.number().optional(),
    replay_of: z.string().optional(),
    bench: z.object({ n: z.number(), runs: z.number(), value_size: z.number(), batch_size: z.number() }).optional(),
    powercut: z.object({ seed: z.number(), writers: z.number(), ops_per_writer: z.number(), max_crash_points: z.number(), max_variants_per_point: z.number(), no_controls: z.boolean().optional() }).optional(),
    perf: z.object({ only: z.array(z.string()).nullable(), quick: z.boolean() }).optional(),
    args: z.array(z.string()),
    started_at: z.string(),
    ended_at: z.string().optional(),
    state: RunState,
    exit_code: z.number().nullable(),
    error: z.string().optional(),
  }),
  elapsed_seconds: z.number(),
  progress: z
    .object({
      completed: z.number().optional(),
      passed: z.number().optional(),
      failed: z.number().optional(),
      inconclusive: z.number().optional(),
      total: z.number().optional(),
      last: IterLine.optional(),
      iterations: z.array(IterLine).nullable().optional(),
      scenarios: z.array(z.object({ id: z.string(), status: z.string() })).nullable().optional(),
      mutations: z.array(z.object({ mutation: z.string(), verdict: z.string(), iterations: z.string() })).nullable().optional(),
      runs_started: z.number().optional(),
      median_printed: z.boolean().optional(),
      crash_points_done: z.number().optional(),
      crash_points_total: z.number().optional(),
      results_printed: z.number().optional(),
    })
    .optional(),
  // summary.json / scenarios.json written by nexus-torture at the end; shown as is.
  summary: z.unknown().nullable().optional(),
  log_tail: z.array(z.string()).nullable().optional(),
});
export type RunStatus = z.infer<typeof RunStatus>;

export const RunList = z.object({ runs: z.array(RunStatus), scope: z.string() });
export type RunList = z.infer<typeof RunList>;

export const RunLog = z.object({ id: z.string(), lines: z.array(z.string()).nullable() });
export type RunLog = z.infer<typeof RunLog>;

export const EvidenceList = z.object({
  directory: z.string(),
  files: z.array(z.object({ name: z.string(), size: z.number(), modified: z.string() })).nullable(),
});
export type EvidenceList = z.infer<typeof EvidenceList>;

const BenchPhase = z.object({
  phase: z.string(),
  ops: z.number(),
  seconds: z.number(),
  ops_per_sec: z.number(),
  latency_per_call: z.object({ p50_us: z.number(), p99_us: z.number(), max_us: z.number() }).optional(),
  file_bytes_after: z.number().optional(),
  keys_after: z.number().optional(),
});

export const Benchmark = z.object({
  kind: z.literal("recorded"),
  file: z.string(),
  modified: z.string(),
  result: z.object({
    tool: z.string(),
    started: z.string(),
    machine: z.record(z.string(), z.string()),
    config: z.record(z.string(), z.union([z.string(), z.number()])),
    median_of_runs: z.array(BenchPhase),
    notes: z.array(z.string()).optional(),
  }),
});
export type Benchmark = z.infer<typeof Benchmark>;

export const DbList = z.object({
  directory: z.string(),
  current: z.string(),
  db_path: z.string(),
  files: z.array(z.object({ name: z.string(), file: z.string(), size: z.number(), modified: z.string(), current: z.boolean() })),
  rule: z.string(),
});
export type DbList = z.infer<typeof DbList>;

export const OpenDbResult = z.object({
  ok: z.literal(true),
  action: z.enum(["opened", "created"]),
  name: z.string(),
  db_path: z.string(),
  last_open: LastOpen,
  stats: Stats,
});
export type OpenDbResult = z.infer<typeof OpenDbResult>;

export const Dump = z.object({
  items: z.array(z.object({ key: EncodedBytes, value: EncodedBytes, size: z.number(), value_truncated: z.boolean() })),
  total_keys: z.number(),
  matched: z.number(),
  returned: z.number(),
  truncated: z.boolean(),
  elapsed_ms: z.number(),
  method: z.string(),
});
export type Dump = z.infer<typeof Dump>;

export const LogEntry = z.object({
  op: z.string(),
  offset: z.number(),
  size: z.number(),
  key_len: z.number(),
  value_len: z.number(),
  key: EncodedBytes,
  value_preview: EncodedBytes,
  value_truncated: z.boolean(),
  entry_crc: z.number(),
});
export type LogEntry = z.infer<typeof LogEntry>;

export const LogFrame = z.object({
  offset: z.number(),
  size: z.number(),
  seq: z.number(),
  op_count: z.number(),
  payload_len: z.number(),
  payload_crc: z.number(),
  in_snapshot: z.boolean(),
  entries: z.array(LogEntry),
  more_entries: z.number(),
});
export type LogFrame = z.infer<typeof LogFrame>;

export const LogMap = z.object({
  path: z.string(),
  file_size: z.number(),
  superblock: z.object({
    magic: z.string(),
    version: z.number(),
    first_seq: z.number(),
    snapshot_end: z.number(),
    valid: z.boolean(),
    error: z.string().optional(),
  }),
  frames: z.array(LogFrame),
  first_listed_frame: z.number(),
  total_frames: z.number(),
  total_entries: z.number(),
  last_seq: z.number(),
  valid_end: z.number(),
  stop: z.object({ kind: z.string(), offset: z.number(), bytes_after: z.number(), message: z.string().optional() }),
  index_sample: z.array(z.object({ key: EncodedBytes, entry_offset: z.number(), entry_size: z.number(), frame_seq: z.number() })).nullable(),
  index_keys: z.number(),
  elapsed_ms: z.number(),
  method: z.string(),
});
export type LogMap = z.infer<typeof LogMap>;

export const MutantCatalog = z.object({
  mutations: z.array(
    z.object({ mutation: z.string(), simulates: z.string(), mode: z.string(), expected_failure_kinds: z.array(z.string()).nullable() }),
  ),
  available: z.boolean(),
  source: z.string(),
});
export type MutantCatalog = z.infer<typeof MutantCatalog>;

export const MutationReport = z.object({
  mutant: z.string(),
  seed: z.number(),
  all_as_expected: z.boolean(),
  outcomes: z.array(
    z.object({
      mutation: z.string(),
      simulates: z.string(),
      mode: z.string(),
      expected_failure_kinds: z.array(z.string()).nullable(),
      engine_result: z.string(),
      detected: z.boolean(),
      as_expected: z.boolean(),
      detected_at_iteration: z.number().optional(),
      failure_kind: z.string().optional(),
      failure: z.string().optional(),
      iterations_run: z.number(),
      seconds: z.number(),
    }),
  ),
});
export type MutationReport = z.infer<typeof MutationReport>;

export const TortureSummary = z.object({
  mode: z.string(),
  seed: z.number(),
  iterations_planned: z.number(),
  iterations_completed: z.number(),
  passed: z.number(),
  failed: z.number(),
  inconclusive: z.number(),
  result: z.string(),
  started: z.string().optional(),
  acked_writes: z.number(),
  unknown_writes: z.number(),
  gets_verified: z.number(),
  kills_with_write_in_flight: z.number().optional(),
  kills_with_compaction_in_flight: z.number().optional(),
  recoveries_discarding_torn_tail: z.number().optional(),
  recoveries_resolving_interrupted_compaction: z.number().optional(),
  verification: z.object({
    operations_attempted: z.number(),
    acknowledged: z.number(),
    acknowledged_writes: z.number(),
    cleanly_rejected: z.number(),
    unknown_outcome: z.number(),
    acknowledged_writes_verified_after_restart: z.number(),
    reads_verified: z.number(),
    batch_atomicity_violations: z.number(),
    data_integrity_violations: z.number(),
    recovery_failures: z.number(),
    protocol_or_liveness_failures: z.number(),
    oracle_inconclusive: z.number(),
  }),
  performance: z.record(z.string(), z.unknown()).optional(),
});
export type TortureSummary = z.infer<typeof TortureSummary>;

export const ScenarioResults = z.object({
  results: z.array(
    z.object({
      id: z.string(),
      group: z.string(),
      title: z.string(),
      invariant: z.string().optional(),
      method: z.string().optional(),
      status: z.string(),
      seconds: z.number().optional(),
      detail: z.string().optional(),
      kill_points: z.array(z.object({ point: z.string(), outcome: z.string().optional(), ok: z.boolean() })).nullable().optional(),
    }),
  ),
  result: z.string(),
  seconds: z.number().optional(),
});
export type ScenarioResults = z.infer<typeof ScenarioResults>;

const BenchPhaseLive = z.object({
  phase: z.string(),
  ops: z.number(),
  seconds: z.number(),
  ops_per_sec: z.number(),
  latency_per_call: z.object({ p50_us: z.number(), p99_us: z.number(), max_us: z.number() }).optional(),
  file_bytes_before: z.number().optional(),
  file_bytes_after: z.number().optional(),
  keys_after: z.number().optional(),
});
export const BenchReport = z.object({
  tool: z.string(),
  started: z.string(),
  machine: z.record(z.string(), z.string()),
  config: z.record(z.string(), z.union([z.string(), z.number()])),
  median_of_runs: z.array(BenchPhaseLive),
  notes: z.array(z.string()).nullable().optional(),
  total_seconds: z.number().optional(),
});
export type BenchReport = z.infer<typeof BenchReport>;
export type BenchPhase = z.infer<typeof BenchPhaseLive>;
