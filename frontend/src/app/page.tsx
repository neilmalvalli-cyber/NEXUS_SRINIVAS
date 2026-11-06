"use client";

import { useState } from "react";
import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { Archive, Clock, Database, Download, FileStack, HardDrive, Layers, Loader2, ScanSearch, Server, Trash2, Upload } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useNexus } from "@/components/providers";
import { useCanAct, useHealth, useOverview } from "@/lib/queries";
import { useCompact, useVerify } from "@/lib/actions";
import { ConfirmDialog, DemoBadge, EmptyState, ErrorState, KV, LoadingBlock, PageHeader, Panel, RecordedBadge, StatCard, StatusPill } from "@/components/nexus";
import { ActivityPanel } from "@/components/activity-table";
import { BenchChart, ChartLegend, LatencyChart, OpsChart } from "@/components/charts";
import { KeyOpsPanel, type KeyOp } from "@/components/key-ops";
import { formatBytes, formatDateTime, formatDuration, formatMs, formatNumber, relativeTime } from "@/lib/format";
import type { Overview } from "@/lib/api/schemas";
import { XRayOverview } from "@/components/xray-view";
import { OptimizationHighlight } from "@/components/perf-view";

function QuickActions() {
  const { ready, reason } = useCanAct();
  const [op, setOp] = useState<KeyOp | null>(null);
  const [confirmCompact, setConfirmCompact] = useState(false);
  const compact = useCompact();
  const verify = useVerify();
  const dis = !ready;
  const t = dis ? reason : undefined;
  return (
    <>
      <Button size="sm" variant="outline" disabled={dis} title={t} onClick={() => setOp("put")}><Upload /> PUT</Button>
      <Button size="sm" variant="outline" disabled={dis} title={t} onClick={() => setOp("get")}><Download /> GET</Button>
      <Button size="sm" variant="outline" disabled={dis} title={t} onClick={() => setOp("delete")}><Trash2 /> DELETE</Button>
      <Button size="sm" variant="outline" disabled={dis || compact.isPending} title={t} onClick={() => setConfirmCompact(true)}>
        {compact.isPending ? <Loader2 className="animate-spin" /> : <Archive />} COMPACT
      </Button>
      <Button size="sm" variant="outline" disabled={dis || verify.isPending} title={t} onClick={() => verify.mutate()}>
        {verify.isPending ? <Loader2 className="animate-spin" /> : <ScanSearch />} INSPECT
      </Button>

      <Dialog open={op !== null} onOpenChange={(o) => !o && setOp(null)}>
        <DialogContent className="bg-elevated ring-border sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>Key operation</DialogTitle>
            <DialogDescription>Runs against the database through nexus-api. Results are the backend&apos;s answers.</DialogDescription>
          </DialogHeader>
          {op && <KeyOpsPanel initialOp={op} />}
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={confirmCompact}
        onOpenChange={setConfirmCompact}
        title="Compact the database?"
        confirmLabel="Compact"
        description="The engine rewrites the live keys into a new file, syncs it and swaps it in. Writes wait while it runs; reads keep working."
        onConfirm={() => compact.mutate()}
      />
    </>
  );
}

/** Engine state facts: each value comes from an endpoint, or shows why it is unknown. */
function EngineState({ d }: { d: Overview | undefined }) {
  const { api, mode } = useNexus();
  const frames = useQuery({ queryKey: [api.mode, "frames", 1], queryFn: () => api.frames(1), enabled: mode === "live", refetchInterval: 15_000 });
  const act = useQuery({ queryKey: [api.mode, "activity", 12], queryFn: () => api.activity(12), refetchInterval: 10_000 });
  const lastOk = act.data?.items.find((a) => a.result === "ok");
  const tail = d?.last_open.tail_rule;
  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
      <StatCard label="Database" value={d?.database ?? "—"} hint={d ? `format version ${d.format_version ?? "unknown"}` : undefined} />
      <StatCard
        label="Last sequence"
        value={mode !== "live" ? "N/A" : frames.data ? (frames.data.last_seq ? formatNumber(frames.data.last_seq) : "none yet") : frames.isError ? "unavailable" : "…"}
        hint={frames.data ? `${formatNumber(frames.data.total_frames)} frames in the log` : mode !== "live" ? "live API only" : undefined}
      />
      <StatCard label="Last successful op" value={lastOk ? lastOk.op.toUpperCase() : "—"} hint={lastOk ? `${lastOk.key ? `${lastOk.key.slice(0, 24)} · ` : ""}${relativeTime(lastOk.at)}` : "none through this adapter yet"} />
      <StatCard
        label="Integrity"
        value={d?.last_verify ? <StatusPill tone={d.last_verify.ok ? "ok" : "bad"} className="text-xs">{d.last_verify.ok ? "verified" : "failed"}</StatusPill> : <StatusPill tone="unknown" className="text-xs">not checked</StatusPill>}
        hint={d?.last_verify ? `kvdb.Verify ${relativeTime(d.last_verify.at)}` : "run INSPECT to check"}
      />
      <StatCard
        label="Recovery status"
        value={d ? <StatusPill tone={tail ? "warn" : "ok"} className="text-xs">{d.last_open.created ? "new file" : tail ? "tail repaired" : "clean open"}</StatusPill> : "—"}
        hint={d ? (tail ? `${formatBytes(d.last_open.discarded_bytes)} discarded (${tail})` : `${formatNumber(d.last_open.frames)} frames replayed`) : undefined}
      />
      <StatCard
        label="Compaction status"
        value={d?.last_compaction ? <StatusPill tone={d.last_compaction.ok ? "ok" : "bad"} className="text-xs">{d.last_compaction.ok ? "completed" : "failed"}</StatusPill> : <StatusPill tone="unknown" className="text-xs">none requested</StatusPill>}
        hint={d?.last_compaction ? relativeTime(d.last_compaction.at) : "automatic compactions are not reported individually"}
      />
    </div>
  );
}

function MetricsPanels() {
  const { api } = useNexus();
  const q = useQuery({ queryKey: [api.mode, "metrics", 300, 5], queryFn: () => api.metrics(300, 5), refetchInterval: 10_000 });
  const scopeNote = "Measured by nexus-api around each engine call it made in the last 5 minutes (5 s buckets). Not engine-wide; no history before the process started.";
  if (q.isPending) return <Panel title="Operations"><LoadingBlock rows={5} /></Panel>;
  if (q.isError) return <Panel title="Operations"><ErrorState error={q.error} onRetry={() => q.refetch()} /></Panel>;
  const empty = q.data.buckets.every((b) => Object.keys(b.ops).length === 0);
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Panel title="Operations per 5 s" description={scopeNote} actions={<ChartLegend />}>
        {empty ? (
          <EmptyState title="No operations in the last 5 minutes">Run a PUT, GET or DELETE and it is counted here. Nothing is drawn that was not measured.</EmptyState>
        ) : (
          <OpsChart metrics={q.data} />
        )}
      </Panel>
      <Panel title="Mean latency per 5 s" description="Wall time of each engine call as timed by nexus-api, including the fsync for writes. Gaps are intervals without requests.">
        {empty ? <EmptyState title="No latency samples yet" /> : <LatencyChart metrics={q.data} />}
      </Panel>
    </div>
  );
}

function RecordedBenchmark() {
  const { api, mode } = useNexus();
  const q = useQuery({ queryKey: [api.mode, "benchmark"], queryFn: api.benchmark, staleTime: 60_000 });
  if (mode === "demo") return null;
  return (
    <Panel
      title={<span className="flex items-center gap-2">Recorded benchmark <RecordedBadge /></span>}
      description={q.data ? `kvdb-bench, median of ${String(q.data.result.config.runs ?? "?")} runs, ${String(q.data.result.config.n ?? "?")} keys, recorded ${formatDateTime(q.data.result.started)} on ${q.data.result.machine.cpu ?? "?"}. Read from ${q.data.file}; not a live measurement.` : "Read from the evidence directory; not a live measurement."}
    >
      {q.isPending ? <LoadingBlock rows={4} /> : q.isError ? <ErrorState error={q.error} compact /> : <BenchChart phases={q.data.result.median_of_runs.filter((p) => !p.phase.startsWith("compact"))} />}
    </Panel>
  );
}

export default function OverviewPage() {
  const { mode } = useNexus();
  const health = useHealth();
  const ov = useOverview();
  const d = ov.data;
  const disconnected = mode === "live" && health.isError;

  const statusTone = disconnected ? "unknown" : !health.data ? "unknown" : health.data.db_open ? (d?.last_verify && !d.last_verify.ok ? "bad" : "ok") : "bad";
  const statusText = disconnected ? "Unknown" : !health.data ? "…" : health.data.db_open ? (d?.last_verify && !d.last_verify.ok ? "Verify failed" : "Online") : "Closed";

  return (
    <div className="space-y-6">
      <PageHeader
        title="Overview"
        description={
          <span className="flex flex-wrap items-center gap-2">
            {mode === "demo" && <DemoBadge />}
            <span className="break-all font-mono text-xs">{d?.db_path ?? health.data?.db_path ?? "—"}</span>
          </span>
        }
        actions={<QuickActions />}
      />

      {ov.isError && !d ? (
        <ErrorState error={ov.error} onRetry={() => ov.refetch()} />
      ) : (
        <div className="grid grid-cols-2 gap-3 md:grid-cols-4 xl:grid-cols-8">
          <StatCard label="Status" icon={Server} value={<StatusPill tone={statusTone} className="text-xs">{statusText}</StatusPill>} hint={health.data ? `checked ${relativeTime(health.data.measured_at)}` : undefined} />
          <StatCard label="Keys" icon={Database} value={d ? formatNumber(d.stats.keys) : "—"} hint="live keys in the index" />
          <StatCard label="File size" icon={HardDrive} value={d ? formatBytes(d.stats.size) : "—"} hint={d ? `${formatBytes(d.stats.live_bytes)} live${d.stats.max_bytes ? ` · cap ${formatBytes(d.stats.max_bytes)}` : ""}` : undefined} />
          <StatCard label="WAL" icon={FileStack} value="N/A" hint="the data file is the log" />
          <StatCard label="Levels" icon={Layers} value="N/A" hint="no LSM levels" />
          <StatCard label="Requests" icon={Clock} value={d ? formatNumber(d.adapter.requests) : "—"} hint={d ? `this adapter, ${formatDuration(d.adapter.uptime_seconds)} up` : undefined} />
          <StatCard
            label="Last compaction"
            icon={Archive}
            value={d?.last_compaction ? relativeTime(d.last_compaction.at) : "None"}
            tone={d?.last_compaction && !d.last_compaction.ok ? "bad" : undefined}
            hint={d?.last_compaction ? `${formatBytes(d.last_compaction.size_before)} → ${formatBytes(d.last_compaction.size_after)}` : "none requested via this adapter"}
          />
          <StatCard
            label="Recovery"
            icon={ScanSearch}
            value={d ? formatMs(d.last_open.duration_ms) : "—"}
            hint={d ? (d.last_open.created ? "new file created at open" : `open replayed ${formatNumber(d.last_open.frames)} frames`) : undefined}
          />
        </div>
      )}

      {!(ov.isError && !d) && <EngineState d={d} />}

      <XRayOverview />

      <MetricsPanels />

      <div className="grid gap-4 xl:grid-cols-[1fr_24rem]">
        <ActivityPanel />
        <div className="space-y-4">
          <Panel title="Last open (recovery)" description="What kvdb.Open reported when nexus-api opened the file.">
            {d ? (
              <KV
                rows={[
                  ["Opened", formatDateTime(d.last_open.at)],
                  ["Duration", formatMs(d.last_open.duration_ms)],
                  ["Frames replayed", formatNumber(d.last_open.frames)],
                  ["Tail", d.last_open.tail_rule || "clean"],
                  ["Discarded", formatBytes(d.last_open.discarded_bytes)],
                  ["Warnings", d.last_open.warnings?.length ? d.last_open.warnings.join("; ") : "none"],
                ]}
              />
            ) : ov.isPending ? <LoadingBlock /> : <span className="text-sm text-faint">Unavailable</span>}
            <div className="mt-3 text-xs"><Link href="/recovery" className="text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">Recovery details →</Link></div>
          </Panel>
          <Panel title="Last inspection" description="kvdb.Verify on the open database, requested through this adapter.">
            {d?.last_verify ? (
              <KV
                rows={[
                  ["Result", <StatusPill key="r" tone={d.last_verify.ok ? "ok" : "bad"}>{d.last_verify.ok ? "pass" : "fail"}</StatusPill>],
                  ["When", formatDateTime(d.last_verify.at)],
                  ["Frames", formatNumber(d.last_verify.frames)],
                  ["Took", formatMs(d.last_verify.elapsed_ms)],
                ]}
              />
            ) : (
              <div className="flex items-center gap-2 text-sm text-faint"><StatusPill tone="unknown">not run</StatusPill> Use INSPECT to check the file.</div>
            )}
          </Panel>
        </div>
      </div>

      <OptimizationHighlight />

      <RecordedBenchmark />
    </div>
  );
}
