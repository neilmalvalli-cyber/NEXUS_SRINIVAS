// Schemas for NEXUS X-RAY (src/internal/xray, docs/XRAY_API.md), the
// Power-Cut Explorer (src/internal/powercut, docs/POWER_CUT_EXPLORER.md) and
// the recorded optimization history (docs/results/optimization/history.json).
// Every field mirrors a Go JSON tag; optional fields are Go `omitempty`.
import { z } from "zod";
import { RunState } from "./schemas";

export const Severity = z.enum(["INFO", "WARNING", "ERROR", "CRITICAL"]);
export type Severity = z.infer<typeof Severity>;
export const SEVERITIES: Severity[] = ["CRITICAL", "ERROR", "WARNING", "INFO"];

export const XRecovery = z.enum(["not_applicable", "detected", "recoverable", "repaired", "recovered", "refused", "unrecoverable"]);
export type XRecovery = z.infer<typeof XRecovery>;

export const XFinding = z.object({
  id: z.string(),
  code: z.string(),
  severity: Severity,
  category: z.string(),
  title: z.string(),
  simple: z.string(),
  technical: z.string(),
  file: z.string().optional(),
  offset: z.number().optional(),
  frame: z.number().optional(),
  seq: z.number().optional(),
  expected: z.string().optional(),
  actual: z.string().optional(),
  rule: z.string().optional(),
  timestamp: z.string(),
  component: z.string(),
  data_impact: z.string(),
  recovery_status: XRecovery,
  recommended_action: z.string(),
  original_error: z.string().optional(),
});
export type XFinding = z.infer<typeof XFinding>;

const Fingerprint = z.object({ exists: z.boolean(), size: z.number(), mod_time: z.string(), sha256: z.string().optional() });
export const Counts = z.object({ INFO: z.number(), WARNING: z.number(), ERROR: z.number(), CRITICAL: z.number() });
export type Counts = z.infer<typeof Counts>;
const strs = z.array(z.string()).nullable().optional();

export const XReport = z.object({
  tool: z.string(),
  version: z.number(),
  path: z.string(),
  mode: z.string(),
  live: z.boolean(),
  started_at: z.string(),
  finished_at: z.string(),
  duration_ms: z.number(),
  health: z.string(),
  counts: Counts,
  fingerprint_before: Fingerprint,
  fingerprint_after: Fingerprint,
  modified: z.boolean(),
  engine: z.object({
    outcome: z.string(),
    rules: strs,
    target: z.string().optional(),
    repairs: strs,
    stale: strs,
    tail_rule: z.string().optional(),
    tail_offset: z.number().optional(),
    tail_bytes: z.number().optional(),
    frames: z.number().optional(),
    keys: z.number().optional(),
    next_seq: z.number().optional(),
    live_bytes: z.number().optional(),
    dead_bytes: z.number().optional(),
    error: z.string().optional(),
    error_class: z.string().optional(),
    refused_file: z.string().optional(),
    refused_rule: z.string().optional(),
    scan_rule: z.string().optional(),
    scan_offset: z.number().optional(),
  }),
  walk: z
    .object({
      file: z.string(),
      size: z.number(),
      superblock: z.object({ version: z.number(), first_seq: z.number(), snapshot_end: z.number() }).optional(),
      frames_valid: z.number(),
      entries_valid: z.number(),
      puts: z.number(),
      dels: z.number(),
      valid_end: z.number(),
      stopped_at: z.number().optional(),
      stop_rule: z.string().optional(),
      bytes_after_valid_end: z.number(),
      next_frame_intact: z.boolean().optional(),
      later_frame_offset: z.number().optional(),
      later_frame_seq: z.number().optional(),
    })
    .optional(),
  recovery: z.object({
    status: XRecovery,
    scan_stopped_at: z.number().optional(),
    failed_frame: z.number().optional(),
    valid_frames_replayed: z.number(),
    valid_records_replayed: z.number(),
    file_modified: z.boolean(),
    truncated: z.boolean(),
    would_truncate_bytes: z.number().optional(),
    index_rebuilt_in_memory: z.boolean(),
    open_would_succeed: z.boolean(),
    possible_data_loss: z.string(),
  }),
  findings: z.array(XFinding),
  repair: z
    .object({
      backups: strs,
      planned: strs,
      open_error: z.string().optional(),
      applied: strs,
      verify_error: z.string().optional(),
      keys: z.number().optional(),
      after: z.object({ health: z.string(), engine_outcome: z.string(), counts: Counts, finding_ids: strs }).optional(),
      verified: z.boolean(),
      at: z.string(),
    })
    .optional(),
});
export type XReport = z.infer<typeof XReport>;

export const XScanResult = z.object({ id: z.string(), target: z.string(), report: XReport });
export type XScanResult = z.infer<typeof XScanResult>;

export const XReportSummary = z.object({
  id: z.string(),
  target: z.string(),
  kind: z.string(),
  started_at: z.string(),
  duration_ms: z.number(),
  mode: z.string(),
  health: z.string(),
  engine_outcome: z.string(),
  recovery_status: XRecovery,
  counts: Counts,
  modified: z.boolean(),
});
export type XReportSummary = z.infer<typeof XReportSummary>;

export const XReportList = z.object({ reports: z.array(XReportSummary).nullable(), scope: z.string() });
export type XReportList = z.infer<typeof XReportList>;

export const XTarget = z.object({
  id: z.string(),
  kind: z.string(),
  label: z.string(),
  description: z.string().optional(),
  note: z.string().optional(),
  exists: z.boolean().optional(),
  repair_allowed: z.boolean(),
  expect: z.object({ code: z.string(), severity: z.string(), rule: z.string().optional(), recovery: z.string() }).optional(),
});
export type XTarget = z.infer<typeof XTarget>;

export const XTargets = z.object({ targets: z.array(XTarget), lab_built: z.boolean(), lab_built_at: z.string().optional() });
export type XTargets = z.infer<typeof XTargets>;

export const XLab = z.object({
  cases: z
    .array(
      z.object({
        name: z.string(),
        target: z.string(),
        title: z.string(),
        description: z.string(),
        expect_code: z.string(),
        expect_severity: z.string(),
        expect_rule: z.string().optional(),
        expect_recovery: z.string(),
        expect_offset: z.number().nullable().optional(),
        hold_lock: z.boolean().optional(),
      }),
    )
    .nullable(),
  available: z.array(z.string()).optional(),
  note: z.string().optional(),
});
export type XLab = z.infer<typeof XLab>;

export const XLabScanAll = z.object({
  results: z.array(
    z.object({ target: z.string(), report: XReportSummary.optional(), expected: z.string().optional(), matched: z.boolean().optional(), error: z.string().optional() }),
  ),
});
export type XLabScanAll = z.infer<typeof XLabScanAll>;

export const XRecentFinding = XFinding.extend({ report_id: z.string(), target: z.string(), kind: z.string() });
export type XRecentFinding = z.infer<typeof XRecentFinding>;

export const XSummary = z.object({
  live: XReportSummary.nullable().optional(),
  live_note: z.string().optional(),
  recent_findings: z.array(XRecentFinding).nullable(),
  operational_findings: z.array(XFinding).nullable(),
  scans_by_kind: z.record(z.string(), z.number()),
});
export type XSummary = z.infer<typeof XSummary>;

// ---- Power-Cut Explorer ----

export const PowercutConfig = z.object({
  seed: z.number(),
  writers: z.number(),
  ops_per_writer: z.number(),
  max_crash_points: z.number(),
  max_variants_per_point: z.number(),
  no_controls: z.boolean().optional(),
});
export type PowercutConfig = z.infer<typeof PowercutConfig>;

const XRayLink = z.object({ health: z.string(), engine_outcome: z.string(), findings: strs, report: z.string() }).nullable().optional();

export const PowercutReport = z.object({
  tool: z.string(),
  version: z.number(),
  config: z.object({ seed: z.number(), writers: z.number(), ops_per_writer: z.number(), max_crash_points: z.number(), max_variants_per_point: z.number(), controls: z.boolean() }).passthrough(),
  started_at: z.string(),
  finished_at: z.string(),
  duration_ms: z.number(),
  environment: z.object({ goos: z.string(), goarch: z.string(), go_version: z.string(), cpus: z.number() }),
  model: z.array(z.string()),
  not_modeled: z.array(z.string()),
  disclaimer: z.string(),
  trace: z.object({
    events: z.number(),
    by_op: z.record(z.string(), z.number()),
    bytes_written: z.number(),
    workload_ops: z.number(),
    acknowledged_ops: z.number(),
    by_kind: z.record(z.string(), z.number()),
    frames_written: z.number(),
  }),
  crash_points_tested: z.number(),
  crash_points_in_trace: z.number(),
  images_validated: z.number(),
  images_by_variant_family: z.record(z.string(), z.number()),
  outcomes: z.record(z.string(), z.number()),
  checks: z.record(z.string(), z.number()),
  passed: z.number(),
  failed: z.number(),
  failures: z
    .array(z.object({ image: z.string(), crash_point: z.number(), variant: z.string(), kind: z.string(), detail: z.string(), dir: z.string().optional(), xray: XRayLink }))
    .nullable(),
  negative_controls: z
    .array(z.object({ name: z.string(), crash_point: z.number(), violates: z.string(), detected: z.boolean(), how: z.string(), xray: XRayLink }))
    .nullable(),
  verdict: z.string(),
  reproduce: z.string(),
});
export type PowercutReport = z.infer<typeof PowercutReport>;

export const PowercutInfo = z.object({
  available: z.boolean(),
  defaults: PowercutConfig,
  model: z.array(z.string()),
  not_modeled: z.array(z.string()),
  disclaimer: z.string(),
  sector_size: z.number(),
  runs: z.array(z.object({ id: z.string(), state: RunState, started_at: z.string(), ended_at: z.string().optional(), config: PowercutConfig.nullable().optional() })),
  limits: z.record(z.string(), z.string()),
  recorded: z.object({ source: z.string(), modified: z.string(), report: PowercutReport }).nullable().optional(),
});
export type PowercutInfo = z.infer<typeof PowercutInfo>;

// ---- recorded optimization history ----

export const PerfMetric = z.object({
  key: z.string(),
  label: z.string(),
  unit: z.string(),
  better: z.enum(["higher", "lower"]),
  group: z.string(),
  phase: z.string(),
  metric: z.string(),
  values: z.record(z.string(), z.object({ value: z.number(), file: z.string() })),
  note: z.string().optional(),
});
export type PerfMetric = z.infer<typeof PerfMetric>;

export const PerfHistory = z.object({
  source: z.string(),
  modified: z.string(),
  kind: z.literal("recorded"),
  history: z.object({
    kind: z.literal("recorded"),
    title: z.string(),
    recorded: z.string(),
    machine: z.record(z.string(), z.unknown()),
    machine_note: z.string(),
    tool: z.string(),
    steps: z.array(z.object({ id: z.string(), label: z.string(), files: z.array(z.string()), changes: z.array(z.string()) })),
    metrics: z.array(PerfMetric),
    rejected: z.array(z.object({ name: z.string(), reason: z.string() })),
    tradeoffs: z.array(z.string()),
    validation: z.object({ go_test: z.string(), torture: z.array(z.string()), powercut: z.string() }),
  }),
});
export type PerfHistory = z.infer<typeof PerfHistory>;

// kvdb-perf JSON output (summary of a live dashboard perf run).
export const PerfReport = z
  .object({
    results: z.array(z.object({ name: z.string(), metrics: z.record(z.string(), z.number()), seconds: z.number().optional() }).passthrough()).nullable(),
  })
  .passthrough();
export type PerfReport = z.infer<typeof PerfReport>;
