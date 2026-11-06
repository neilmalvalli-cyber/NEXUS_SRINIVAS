"use client";

import { useQuery } from "@tanstack/react-query";
import { useNexus } from "@/components/providers";
import { EmptyState, ErrorState, KV, LoadingBlock, StatCard, StatusPill } from "@/components/nexus";
import { BenchReport, type BenchPhase } from "@/lib/api/schemas";
import { formatBytes, formatDateTime, formatDuration, formatMs, formatNumber } from "@/lib/format";

const PHASES_PER_RUN = 7;
export const RECORDED_BENCH = "bench.json";

function phase(r: BenchReport | undefined, name: string): BenchPhase | undefined {
  return r?.median_of_runs.find((p) => p.phase === name || p.phase.startsWith(name + " "));
}
const ops = (p?: BenchPhase) => (p && p.ops_per_sec > 0 ? `${formatNumber(Math.round(p.ops_per_sec))}/s` : "—");

export function Headline({ r }: { r: BenchReport }) {
  const put = phase(r, "put");
  const get = phase(r, "get");
  const del = phase(r, "delete");
  const reopen = phase(r, "reopen");
  const compact = phase(r, "compact");
  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
      <StatCard label="PUT throughput" value={ops(put)} hint={put?.latency_per_call ? `p50 ${put.latency_per_call.p50_us} µs · synced each` : "synced each"} />
      <StatCard label="GET throughput" value={ops(get)} hint="index lookup + one read" />
      <StatCard label="DELETE throughput" value={ops(del)} hint="synced each" />
      <StatCard label="Reopen (recovery)" value={reopen ? formatMs(reopen.seconds * 1000) : "—"} hint={reopen ? `${formatNumber(reopen.keys_after ?? 0)} keys rebuilt` : undefined} />
      <StatCard label="Compaction" value={compact ? formatMs(compact.seconds * 1000) : "—"} hint={compact ? `${formatNumber(compact.ops)} live keys rewritten` : undefined} />
      <StatCard
        label="File before → after"
        value={compact?.file_bytes_before !== undefined ? `${formatBytes(compact.file_bytes_before)} → ${formatBytes(compact.file_bytes_after)}` : "—"}
        hint="compaction of n live keys"
      />
    </div>
  );
}

export function PhaseTable({ r }: { r: BenchReport }) {
  return (
    <div className="overflow-x-auto rounded-sm border border-border">
      <table className="w-full text-xs">
        <thead>
          <tr className="border-b border-border text-left text-faint">
            <th className="px-3 py-1.5 font-normal">Phase</th>
            <th className="px-3 py-1.5 text-right font-normal">Ops</th>
            <th className="px-3 py-1.5 text-right font-normal">Seconds</th>
            <th className="px-3 py-1.5 text-right font-normal">Ops/s</th>
            <th className="px-3 py-1.5 text-right font-normal">p50 / p99 per call</th>
            <th className="px-3 py-1.5 text-right font-normal">File after</th>
            <th className="px-3 py-1.5 text-right font-normal">Keys after</th>
          </tr>
        </thead>
        <tbody>
          {r.median_of_runs.map((p) => (
            <tr key={p.phase} className="border-b border-border font-mono last:border-0">
              <td className="px-3 py-1.5 font-sans">{p.phase}</td>
              <td className="px-3 py-1.5 text-right">{formatNumber(p.ops)}</td>
              <td className="px-3 py-1.5 text-right">{p.seconds.toFixed(3)}</td>
              <td className="px-3 py-1.5 text-right">{p.ops_per_sec > 0 ? formatNumber(Math.round(p.ops_per_sec)) : "—"}</td>
              <td className="px-3 py-1.5 text-right">{p.latency_per_call ? `${p.latency_per_call.p50_us} / ${p.latency_per_call.p99_us} µs` : "—"}</td>
              <td className="px-3 py-1.5 text-right">{p.file_bytes_after !== undefined ? formatBytes(p.file_bytes_after) : "—"}</td>
              <td className="px-3 py-1.5 text-right">{p.keys_after !== undefined ? formatNumber(p.keys_after) : "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** Horizontal bars on a log scale: synced writes and in-memory reads differ by ~60x. */
export function ThroughputBars({ live, recorded }: { live?: BenchReport; recorded?: BenchReport }) {
  const names = ["put", "get", "batch", "delete"];
  const vals = names.flatMap((n) => [phase(live, n)?.ops_per_sec ?? 0, phase(recorded, n)?.ops_per_sec ?? 0]).filter((v) => v > 0);
  if (vals.length === 0) return null;
  const max = Math.log10(Math.max(...vals));
  const min = Math.max(0, Math.floor(Math.log10(Math.min(...vals))) - 0.3);
  const w = (v: number) => (v > 0 ? `${Math.max(2, ((Math.log10(v) - min) / (max - min || 1)) * 100)}%` : "0");
  return (
    <div className="space-y-3">
      {names.map((n) => {
        const l = phase(live, n)?.ops_per_sec ?? 0;
        const rv = phase(recorded, n)?.ops_per_sec ?? 0;
        if (!l && !rv) return null;
        return (
          <div key={n} className="grid grid-cols-[4rem_1fr] items-center gap-2 text-xs">
            <span className="font-mono uppercase text-muted-foreground">{n}</span>
            <div className="space-y-1">
              {live && (
                <div className="flex items-center gap-2">
                  <div className="h-3 rounded-sm bg-foreground/75 transition-[width] duration-700" style={{ width: w(l) }} />
                  <span className="shrink-0 font-mono text-[11px]">{l ? formatNumber(Math.round(l)) : "—"} live</span>
                </div>
              )}
              {recorded && (
                <div className="flex items-center gap-2">
                  <div className="h-3 rounded-sm border border-faint bg-elevated" style={{ width: w(rv) }} />
                  <span className="shrink-0 font-mono text-[11px] text-faint">{rv ? formatNumber(Math.round(rv)) : "—"} recorded</span>
                </div>
              )}
            </div>
          </div>
        );
      })}
      <p className="text-[11px] text-faint">Ops per second, logarithmic scale. PUT and DELETE are bounded by one fsync per call; GET and BATCH are not.</p>
    </div>
  );
}

export function LiveRun({ id, recorded }: { id: string; recorded?: BenchReport }) {
  const { api } = useNexus();
  const q = useQuery({
    queryKey: [api.mode, "run", id],
    queryFn: () => api.runStatus(id),
    refetchInterval: (query) => (query.state.data?.run.state === "running" ? 1_500 : false),
  });
  const running = q.data?.run.state === "running";
  const log = useQuery({ queryKey: [api.mode, "run-log", id], queryFn: () => api.runLog(id, 200), refetchInterval: running ? 1_500 : false });
  if (q.isPending) return <LoadingBlock rows={4} />;
  if (q.isError) return <ErrorState error={q.error} onRetry={() => q.refetch()} />;
  const { run, progress, summary } = q.data;
  const parsed = summary ? BenchReport.safeParse(summary) : null;
  const total = (run.bench?.runs ?? 1) * PHASES_PER_RUN;
  const done = Math.min(progress?.completed ?? 0, total);
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3 text-xs">
        <StatusPill tone={run.state === "running" ? "running" : run.state === "passed" ? "ok" : "bad"}>{run.state === "passed" ? "finished" : run.state}</StatusPill>
        <span className="font-mono text-muted-foreground">{run.id}</span>
        <span className="text-faint">{formatDuration(q.data.elapsed_seconds)}</span>
        {run.bench && <span className="font-mono text-faint">n={formatNumber(run.bench.n)} · runs={run.bench.runs} · value={run.bench.value_size} B · batch={run.bench.batch_size}</span>}
      </div>
      {run.error && <ErrorState error={new Error(run.error)} compact />}
      {running && (
        <div className="space-y-1">
          <div className="h-1.5 overflow-hidden rounded-full bg-elevated">
            <div className="h-full bg-foreground/70 transition-[width]" style={{ width: `${(done / total) * 100}%` }} />
          </div>
          <div className="font-mono text-[11px] text-faint">{done} / {total} phases printed by kvdb-bench</div>
        </div>
      )}
      {parsed?.success ? (
        <>
          <Headline r={parsed.data} />
          <div className="grid gap-4 lg:grid-cols-2">
            <ThroughputBars live={parsed.data} recorded={recorded} />
            <KV
              className="text-xs"
              rows={[
                ["Machine", `${parsed.data.machine.cpu ?? "?"} · ${parsed.data.machine.os ?? ""}`],
                ["Started", formatDateTime(parsed.data.started)],
                ["Durability", String(parsed.data.config.durability ?? "—")],
                ["Comparable to recorded?", recorded && recorded.config.n === parsed.data.config.n ? "Same n; still a different moment and possibly a different machine." : "No: different n. Throughput depends on n, disk and machine."],
              ]}
            />
          </div>
          <PhaseTable r={parsed.data} />
        </>
      ) : !running ? (
        <EmptyState title="No result file">kvdb-bench did not write bench.json; see the log.</EmptyState>
      ) : null}
      <pre className="h-48 overflow-auto rounded-sm border border-border bg-background p-2.5 font-mono text-[11px] leading-relaxed text-muted-foreground">
        {log.data?.lines?.length ? log.data.lines.join("\n") : running ? "waiting for output…" : "(no output)"}
      </pre>
    </div>
  );
}
