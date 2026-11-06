"use client";

// Shared X-RAY views: severity and recovery pills, an expandable finding
// card, the byte-offset map of a scanned file and a full report summary.
// Everything shown comes from a report produced by src/internal/xray; no
// value is derived from error-message text.
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNexus } from "@/components/providers";
import Link from "next/link";
import { ChevronDown, ChevronRight, ExternalLink, FileSearch, LifeBuoy, Map as MapIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import { KV, Mono, Panel, StatusPill, type Tone } from "@/components/nexus";
import type { Counts, Severity, XFinding, XRecovery, XReport } from "@/lib/api/xray-schemas";
import { SEVERITIES } from "@/lib/api/xray-schemas";
import { formatBytes, formatDateTime, formatNumber } from "@/lib/format";

export const severityTone: Record<Severity, Tone> = { CRITICAL: "bad", ERROR: "bad", WARNING: "warn", INFO: "neutral" };

export const recoveryLabel: Record<XRecovery, string> = {
  not_applicable: "not applicable",
  detected: "detected",
  recoverable: "recoverable on open",
  repaired: "repaired (verified)",
  recovered: "recovered (verified)",
  refused: "refused",
  unrecoverable: "unrecoverable",
};

export const recoveryTone: Record<XRecovery, Tone> = {
  not_applicable: "neutral",
  detected: "warn",
  recoverable: "warn",
  repaired: "ok",
  recovered: "ok",
  refused: "bad",
  unrecoverable: "bad",
};

export function healthTone(h: string): Tone {
  return h === "healthy" ? "ok" : h === "attention" ? "warn" : h === "degraded" || h === "critical" ? "bad" : "unknown";
}

/** Where a report's database came from: live data and test copies are never mixed. */
export function KindBadge({ kind }: { kind: string }) {
  const label = kind === "live" ? "LIVE DATABASE" : kind === "demo" ? "CRASH-DEMO COPY" : kind === "lab" ? "TEST LAB" : kind === "powercut" ? "POWER-CUT IMAGE" : kind.toUpperCase();
  return (
    <span
      className={cn(
        "inline-flex h-5 items-center rounded-sm border px-1.5 font-mono text-[10px] tracking-widest",
        kind === "live" ? "border-foreground/40 text-foreground" : "border-border text-muted-foreground",
      )}
      title={kind === "live" ? "Findings about the database nexus-api serves" : "A disposable test database; findings here say nothing about the live database"}
    >
      {label}
    </span>
  );
}

export function SeverityPill({ s }: { s: Severity }) {
  return <StatusPill tone={severityTone[s]}>{s}</StatusPill>;
}

export function CountsRow({ counts, className }: { counts: Counts; className?: string }) {
  return (
    <div className={cn("flex flex-wrap gap-2", className)}>
      {SEVERITIES.map((s) => (
        <span key={s} className={cn("inline-flex items-center gap-1.5 rounded-sm border border-border px-2 py-0.5 font-mono text-[11px]", counts[s] ? "text-foreground" : "text-faint")}>
          {s} <span className={cn(counts[s] && (s === "CRITICAL" || s === "ERROR") ? "text-bad" : counts[s] && s === "WARNING" ? "text-warn" : "")}>{counts[s]}</span>
        </span>
      ))}
    </div>
  );
}

const hex = (n: number) => "0x" + n.toString(16);

export function FindingCard({ f, live, defaultOpen = false }: { f: XFinding; live?: boolean; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen);
  const loc: [React.ReactNode, React.ReactNode][] = [];
  if (f.file) loc.push(["File", f.file]);
  if (f.offset !== undefined) loc.push(["Offset", `${formatNumber(f.offset)} (${hex(f.offset)})`]);
  if (f.frame !== undefined) loc.push(["Frame", `#${formatNumber(f.frame)}`]);
  if (f.seq !== undefined) loc.push(["Sequence", formatNumber(f.seq)]);
  if (f.rule) loc.push(["Rule", f.rule]);
  if (f.expected) loc.push(["Expected", f.expected]);
  if (f.actual) loc.push(["Actual", f.actual]);
  return (
    <div className={cn("rounded-md border bg-background/40", f.severity === "CRITICAL" ? "border-bad/40" : "border-border")}>
      <button type="button" className="flex w-full items-start gap-3 px-3 py-2.5 text-left" onClick={() => setOpen((o) => !o)} aria-expanded={open}>
        {open ? <ChevronDown className="mt-0.5 size-4 shrink-0 text-faint" /> : <ChevronRight className="mt-0.5 size-4 shrink-0 text-faint" />}
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <SeverityPill s={f.severity} />
            <span className="text-sm text-foreground">{f.title}</span>
          </div>
          <div className="flex flex-wrap gap-x-3 gap-y-0.5 font-mono text-[11px] text-faint">
            <span>{f.code}</span>
            <span>{f.category}</span>
            <span>{f.component}</span>
            {f.offset !== undefined && <span>@{formatNumber(f.offset)}</span>}
            <span className={cn(recoveryTone[f.recovery_status] === "bad" ? "text-bad" : recoveryTone[f.recovery_status] === "ok" ? "text-ok" : "")}>
              {recoveryLabel[f.recovery_status]}
            </span>
          </div>
        </div>
      </button>
      {open && (
        <div className="space-y-3 border-t border-border px-3 py-3 text-sm">
          <p className="text-foreground">{f.simple}</p>
          <div>
            <div className="mb-1 text-[11px] uppercase tracking-wider text-faint">Technical</div>
            <p className="font-mono text-[12px] leading-relaxed text-muted-foreground">{f.technical}</p>
          </div>
          {loc.length > 0 && <KV className="text-xs" rows={loc} />}
          <KV
            className="text-xs"
            rows={[
              ["Data impact", <span key="d" className="font-sans">{f.data_impact}</span>],
              ["Recovery", recoveryLabel[f.recovery_status]],
              ["Recommended action", <span key="a" className="font-sans">{f.recommended_action}</span>],
              ["Detected", formatDateTime(f.timestamp)],
              ["Finding ID", f.id],
              ...(f.original_error ? ([["Original error", f.original_error]] as [string, string][]) : []),
            ]}
          />
          <div className="flex flex-wrap gap-2 text-xs">
            {live && f.offset !== undefined && (
              <Link className="inline-flex items-center gap-1 rounded-sm border border-border px-2 py-1 text-muted-foreground hover:text-foreground" href="/storage">
                <MapIcon className="size-3.5" /> Frame in the log map
              </Link>
            )}
            {(f.category === "recovery" || f.category === "frame" || f.category === "compaction") && (
              <Link className="inline-flex items-center gap-1 rounded-sm border border-border px-2 py-1 text-muted-foreground hover:text-foreground" href="/recovery">
                <LifeBuoy className="size-3.5" /> Recovery rules
              </Link>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

/** Byte map of the scanned file: valid frames, the stop point, and every finding with an offset. */
export function OffsetMap({ report }: { report: XReport }) {
  const w = report.walk;
  const size = w?.size ?? report.fingerprint_before.size;
  const marks = report.findings.filter((f) => f.offset !== undefined && (!f.file || !w || f.file === w.file));
  if (!w || size <= 0) return null;
  const pct = (o: number) => `${Math.min(100, Math.max(0, (o / size) * 100))}%`;
  return (
    <div className="space-y-2">
      <div className="relative h-6 overflow-hidden rounded-sm border border-border bg-elevated" role="img" aria-label="File layout by byte offset">
        <div className="absolute inset-y-0 left-0 bg-foreground/15" style={{ width: pct(w.valid_end) }} title={`valid frames: 0 – ${formatNumber(w.valid_end)}`} />
        {w.bytes_after_valid_end > 0 && (
          <div className="absolute inset-y-0 bg-bad/25" style={{ left: pct(w.valid_end), right: 0 }} title={`${formatNumber(w.bytes_after_valid_end)} bytes after the last valid frame`} />
        )}
        {marks.map((f) => (
          <div
            key={f.id}
            className={cn("absolute inset-y-0 w-0.5", f.severity === "CRITICAL" || f.severity === "ERROR" ? "bg-bad" : f.severity === "WARNING" ? "bg-warn" : "bg-muted-foreground")}
            style={{ left: pct(f.offset!) }}
            title={`${f.code} @ ${formatNumber(f.offset!)}`}
          />
        ))}
      </div>
      <div className="flex flex-wrap justify-between gap-2 font-mono text-[11px] text-faint">
        <span>0</span>
        <span>
          {formatNumber(w.frames_valid)} valid frames · {formatNumber(w.entries_valid)} records · valid to {formatNumber(w.valid_end)}
          {w.bytes_after_valid_end > 0 && ` · ${formatNumber(w.bytes_after_valid_end)} B after`}
        </span>
        <span>{formatBytes(size)}</span>
      </div>
    </div>
  );
}

export function ReportFacts({ report }: { report: XReport }) {
  const e = report.engine;
  const r = report.recovery;
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <KV
        className="text-xs"
        rows={[
          ["Engine verdict", e.outcome],
          ...(e.rules?.length ? ([["Authority rules", e.rules.join(", ")]] as [string, string][]) : []),
          ...(e.tail_rule ? ([["Tail rule", `${e.tail_rule}${e.tail_offset !== undefined ? ` @ ${formatNumber(e.tail_offset)}` : ""}${e.tail_bytes ? ` (${formatNumber(e.tail_bytes)} B)` : ""}`]] as [string, string][]) : []),
          ...(e.keys !== undefined ? ([["Keys", formatNumber(e.keys)]] as [string, string][]) : []),
          ...(e.frames !== undefined ? ([["Frames", formatNumber(e.frames)]] as [string, string][]) : []),
          ...(e.live_bytes !== undefined ? ([["Live / dead bytes", `${formatBytes(e.live_bytes)} / ${formatBytes(e.dead_bytes ?? 0)}`]] as [string, string][]) : []),
          ...(e.error ? ([["Engine error", e.error]] as [string, string][]) : []),
          ["Duration", `${report.duration_ms.toFixed(1)} ms`],
        ]}
      />
      <KV
        className="text-xs"
        rows={[
          ["Recovery status", recoveryLabel[r.status]],
          ["Normal open would succeed", r.open_would_succeed ? "yes" : "no"],
          ["Valid frames / records", `${formatNumber(r.valid_frames_replayed)} / ${formatNumber(r.valid_records_replayed)}`],
          ...(r.would_truncate_bytes ? ([["Open would truncate", `${formatNumber(r.would_truncate_bytes)} bytes`]] as [string, string][]) : []),
          ["Possible data loss", <span key="l" className="font-sans">{r.possible_data_loss}</span>],
          ["File modified by this scan", report.modified ? "YES" : "no (fingerprint unchanged)"],
          ...(report.fingerprint_before.sha256 ? ([["SHA-256", report.fingerprint_before.sha256.slice(0, 16) + "…"]] as [string, string][]) : []),
        ]}
      />
    </div>
  );
}

export function ExportLink({ href }: { href: string | null }) {
  if (!href) return null;
  return (
    <a className="inline-flex h-7 items-center gap-1 rounded-md border border-border px-2 text-xs text-muted-foreground hover:text-foreground" href={href} download>
      <ExternalLink className="size-3.5" /> Export JSON
    </a>
  );
}

export function NoFindings() {
  return (
    <div className="flex items-center gap-2 rounded-md border border-border px-3 py-3 text-sm text-muted-foreground">
      <FileSearch className="size-4" /> No findings match.
    </div>
  );
}

export { Mono };

/** Overview panel: live health from the latest real scan, recent findings (labelled by source). */
export function XRayOverview() {
  const { api, mode } = useNexus();
  const q = useQuery({ queryKey: [api.mode, "xray-summary"], queryFn: api.xraySummary, enabled: mode === "live", refetchInterval: 30_000 });
  if (mode !== "live") return null;
  const live = q.data?.live;
  const recent = (q.data?.recent_findings ?? []).filter((f) => f.code !== "SCAN_CLEAN").slice(0, 5);
  const ops = q.data?.operational_findings ?? [];
  return (
    <Panel
      title="X-RAY diagnostics"
      description="Counts come from the latest real scan of the live database; test-lab and crash-demo scans are listed separately and never change live health."
      actions={<Link href="/xray" className="text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">Open X-RAY →</Link>}
    >
      {q.isPending ? (
        <div className="h-16" />
      ) : q.isError ? (
        <p className="text-xs text-bad">X-RAY summary unavailable.</p>
      ) : (
        <div className="space-y-3">
          {live ? (
            <div className="flex flex-wrap items-center gap-3">
              <StatusPill tone={healthTone(live.health)}>{live.health}</StatusPill>
              <CountsRow counts={live.counts} />
              <span className="text-[11px] text-faint">scanned {formatDateTime(live.started_at)}</span>
            </div>
          ) : (
            <p className="text-xs text-muted-foreground">The live database has not been scanned by this adapter yet. Live alert counts are unknown, not zero.</p>
          )}
          {ops.length > 0 && <p className="text-xs text-warn">{ops.length} operational event(s) recorded (errors returned to callers).</p>}
          {recent.length > 0 && (
            <ul className="space-y-1 text-xs">
              {recent.map((f) => (
                <li key={f.report_id + f.id} className="flex flex-wrap items-center gap-2">
                  <SeverityPill s={f.severity} />
                  <KindBadge kind={f.kind} />
                  <Link href={`/xray?report=${encodeURIComponent(f.report_id)}`} className="text-foreground underline-offset-2 hover:underline">{f.title}</Link>
                  <span className="font-mono text-faint">{f.target}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </Panel>
  );
}
