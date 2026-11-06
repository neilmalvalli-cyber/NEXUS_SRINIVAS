"use client";

// Views of the recorded optimization history (docs/results/optimization/
// history.json, served by GET /api/perf/history). Every number shown here is
// RECORDED: measured while the optimizations were made, on the machine named
// in the file. Live kvdb-perf runs are shown separately and labelled LIVE.
import { Fragment, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Loader2, Play } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useCanAct, useHealth } from "@/lib/queries";
import { toastError } from "@/lib/mutations";
import Link from "next/link";
import { useNexus } from "@/components/providers";
import { ErrorState, LoadingBlock, Panel, RecordedBadge, StatusPill } from "@/components/nexus";
import { PerfReport, type PerfHistory, type PerfMetric } from "@/lib/api/xray-schemas";
import { formatDateTime, formatDuration, formatNumber } from "@/lib/format";
import { cn } from "@/lib/utils";

export function usePerfHistory() {
  const { api, mode } = useNexus();
  return useQuery({ queryKey: [api.mode, "perf-history"], queryFn: api.perfHistory, enabled: mode === "live", staleTime: 60_000, retry: false });
}

export function fmtValue(v: number | undefined, unit: string) {
  if (v === undefined) return "—";
  const n = Math.abs(v) >= 100 ? formatNumber(Math.round(v)) : v.toFixed(v < 10 ? 2 : 1);
  return `${n} ${unit}`;
}

/** Change from baseline to final, signed so that positive always means "better". */
export function change(m: PerfMetric, from: string, to: string): { pct: number; better: boolean } | null {
  const a = m.values[from]?.value;
  const b = m.values[to]?.value;
  if (a === undefined || b === undefined || a === 0) return null;
  const pct = ((b - a) / a) * 100;
  return { pct, better: m.better === "higher" ? b > a : b < a };
}

function Delta({ m, from, to }: { m: PerfMetric; from: string; to: string }) {
  const c = change(m, from, to);
  if (!c) return <span className="text-faint">—</span>;
  const small = Math.abs(c.pct) < 5;
  return (
    <span className={cn("font-mono", small ? "text-faint" : c.better ? "text-ok" : "text-bad")} title={small ? "within 5%: treat as run-to-run variance" : undefined}>
      {c.pct > 0 ? "+" : ""}
      {c.pct.toFixed(0)}%
    </span>
  );
}

const HIGHLIGHT = ["put_16w_ops", "get_8t_ops", "mixed_get_max", "compact_reader_stall"];

/** Small panel for the overview: four headline before/after numbers. */
export function OptimizationHighlight() {
  const q = usePerfHistory();
  const { mode } = useNexus();
  if (mode !== "live") return null;
  const h = q.data?.history;
  return (
    <Panel
      title={<span className="flex items-center gap-2">Engine optimization <RecordedBadge when={q.data?.modified} /></span>}
      description={h ? `${h.title}. Baseline vs. final, same machine and workload (${h.machine_note.split(";")[0]}). Not a live measurement.` : "Recorded before/after measurements."}
      actions={<Link href="/benchmark" className="text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">History →</Link>}
    >
      {q.isPending ? (
        <LoadingBlock rows={3} />
      ) : q.isError ? (
        <ErrorState error={q.error} compact />
      ) : (
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          {HIGHLIGHT.map((k) => {
            const m = h!.metrics.find((x) => x.key === k);
            if (!m) return null;
            return (
              <div key={k} className="rounded-md border border-border p-3">
                <div className="text-[11px] text-muted-foreground">{m.label}</div>
                <div className="mt-1 font-mono text-sm text-foreground">{fmtValue(m.values["A+B+C"]?.value, m.unit)}</div>
                <div className="mt-0.5 text-[11px] text-faint">
                  was {fmtValue(m.values.baseline?.value, m.unit)} · <Delta m={m} from="baseline" to="A+B+C" />
                </div>
              </div>
            );
          })}
        </div>
      )}
    </Panel>
  );
}

/** Full optimization history for the benchmark page. */
export function OptimizationHistory({ data }: { data: PerfHistory }) {
  const h = data.history;
  const steps = h.steps.map((s) => s.id);
  const groups = Array.from(new Set(h.metrics.map((m) => m.group)));
  return (
    <div className="space-y-5">
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        {h.steps.map((s, i) => (
          <div key={s.id} className="rounded-md border border-border p-3 text-xs">
            <div className="font-mono text-[11px] text-faint">step {i}</div>
            <div className="mt-0.5 text-foreground">{s.label}</div>
            {s.changes.length > 0 && <ul className="mt-1 list-disc space-y-0.5 pl-4 text-muted-foreground">{s.changes.map((c) => <li key={c}>{c}</li>)}</ul>}
            <div className="mt-1 font-mono text-[10px] text-faint">{s.files.join(", ")}</div>
          </div>
        ))}
      </div>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[720px] text-xs">
          <thead className="text-left text-faint">
            <tr>
              <th className="py-1 pr-3 font-normal">Metric</th>
              {steps.map((s) => <th key={s} className="pr-3 text-right font-normal">{s}</th>)}
              <th className="text-right font-normal">baseline → final</th>
            </tr>
          </thead>
          <tbody>
            {groups.map((g) => (
              <Fragment key={g}>
                <tr>
                  <td colSpan={steps.length + 2} className="pt-3 pb-1 text-[10px] uppercase tracking-widest text-faint">{g}</td>
                </tr>
                {h.metrics.filter((m) => m.group === g).map((m) => (
                  <tr key={m.key} className="border-t border-border align-top">
                    <td className="py-1.5 pr-3 text-muted-foreground">
                      {m.label} <span className="text-faint">({m.better} is better)</span>
                      {m.note && <div className="text-[11px] text-faint">{m.note}</div>}
                    </td>
                    {steps.map((s) => (
                      <td key={s} className="py-1.5 pr-3 text-right font-mono text-foreground" title={m.values[s]?.file}>
                        {fmtValue(m.values[s]?.value, m.unit)}
                      </td>
                    ))}
                    <td className="py-1.5 text-right"><Delta m={m} from="baseline" to="A+B+C" /></td>
                  </tr>
                ))}
              </Fragment>
            ))}
          </tbody>
        </table>
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <div>
          <div className="mb-1 text-[11px] uppercase tracking-wider text-faint">Trade-offs</div>
          <ul className="list-disc space-y-1 pl-5 text-xs text-muted-foreground">{h.tradeoffs.map((t) => <li key={t}>{t}</li>)}</ul>
        </div>
        <div>
          <div className="mb-1 text-[11px] uppercase tracking-wider text-faint">Evaluated and rejected</div>
          <ul className="space-y-1 text-xs text-muted-foreground">{h.rejected.map((r) => <li key={r.name}><span className="text-foreground">{r.name}</span>: {r.reason}</li>)}</ul>
        </div>
      </div>
      <p className="text-[11px] text-faint">
        Validation after the final step: {h.validation.go_test}; torture: {h.validation.torture.join(", ")}; power-cut: {h.validation.powercut}. Machine: {h.machine_note}. Tool: {h.tool}. Source {data.source}, file modified {formatDateTime(data.modified)}. Values within ±5% are shown grey and should be read as run-to-run variance.
      </p>
    </div>
  );
}

const PERF_PHASES = ["writers", "batch", "delete", "get", "mixed", "memory", "recovery", "compaction"] as const;

/** Start and show live kvdb-perf runs (labelled LIVE; never merged with recorded numbers). */
export function LivePerfPanel() {
  const { api, mode } = useNexus();
  const qc = useQueryClient();
  const health = useHealth();
  const { ready, reason } = useCanAct();
  const runs = useQuery({ queryKey: [api.mode, "runs"], queryFn: api.listRuns, enabled: mode === "live", refetchInterval: health.data?.torture_running ? 3_000 : false });
  const perfRuns = runs.data?.runs.filter((r) => r.run.kind === "perf") ?? [];
  const [sel, setSel] = useState<string | null>(null);
  const current = sel ?? perfRuns[0]?.run.id ?? null;
  const [phases, setPhases] = useState<Set<string>>(new Set(["writers", "get", "mixed"]));
  const missing = health.data ? !health.data.tools.kvdb_perf : false;
  const busy = !!health.data?.torture_running;
  const start = useMutation({
    mutationFn: () => api.startPerf({ only: Array.from(phases), quick: true }),
    onSuccess: (st) => {
      toast.success("kvdb-perf started", { description: st.run.args.join(" ") });
      setSel(st.run.id);
      qc.invalidateQueries({ queryKey: [api.mode, "runs"] });
      qc.invalidateQueries({ queryKey: [api.mode, "health"] });
    },
    onError: (e) => toastError("Start kvdb-perf", e),
  });
  if (mode !== "live") return null;
  return (
    <Panel
      title={
        <span className="flex items-center gap-2">
          Run kvdb-perf now <span className="inline-flex h-5 items-center rounded-sm border border-foreground/40 px-1.5 font-mono text-[10px] tracking-widest text-foreground">LIVE</span>
        </span>
      }
      description="Quick-size workloads (smaller than the recorded history, so the numbers are not directly comparable with it) in the adapter's scratch directory. Your database is not used."
    >
      <div className="flex flex-wrap items-center gap-2">
        {PERF_PHASES.map((p) => (
          <label key={p} className="flex items-center gap-1.5 rounded-md border border-border px-2 py-1 font-mono text-[11px] text-muted-foreground">
            <input
              type="checkbox"
              checked={phases.has(p)}
              onChange={() =>
                setPhases((s) => {
                  const n = new Set(s);
                  if (n.has(p)) n.delete(p);
                  else n.add(p);
                  return n;
                })
              }
            />{" "}
            {p}
          </label>
        ))}
        <Button
          size="sm"
          disabled={!ready || missing || busy || phases.size === 0 || start.isPending}
          title={!ready ? reason : missing ? "kvdb-perf executable not found" : busy ? "Another run is in progress" : undefined}
          onClick={() => start.mutate()}
        >
          {start.isPending ? <Loader2 className="animate-spin" /> : <Play />} Run kvdb-perf (quick)
        </Button>
        {perfRuns.length > 1 && (
          <select aria-label="Run" className="h-7 rounded-md border border-input bg-background px-2 font-mono text-xs" value={current ?? ""} onChange={(e) => setSel(e.target.value)}>
            {perfRuns.map((r) => (
              <option key={r.run.id} value={r.run.id}>
                {r.run.id} · {r.run.state}
              </option>
            ))}
          </select>
        )}
      </div>
      {current && (
        <div className="mt-4">
          <LivePerfRun key={current} id={current} />
        </div>
      )}
    </Panel>
  );
}

function LivePerfRun({ id }: { id: string }) {
  const { api } = useNexus();
  const q = useQuery({ queryKey: [api.mode, "run", id], queryFn: () => api.runStatus(id), refetchInterval: (query) => (query.state.data?.run.state === "running" ? 2_000 : false) });
  if (q.isPending) return <LoadingBlock rows={3} />;
  if (q.isError) return <ErrorState error={q.error} compact />;
  const { run, progress, summary } = q.data;
  const parsed = summary ? PerfReport.safeParse(summary) : null;
  const results = parsed?.success ? (parsed.data.results ?? []) : [];
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3 text-xs">
        <StatusPill tone={run.state === "running" ? "running" : run.state === "passed" ? "ok" : "bad"}>{run.state === "passed" ? "finished" : run.state}</StatusPill>
        <span className="font-mono text-muted-foreground">{run.id}</span>
        <span className="text-faint">{formatDuration(q.data.elapsed_seconds)}</span>
        {run.state === "running" && <span className="font-mono text-faint">{progress?.results_printed ?? 0} result line(s) printed</span>}
        <span className="font-mono text-faint">phases: {run.perf?.only?.join(", ")}</span>
      </div>
      {run.error && <ErrorState error={new Error(run.error)} compact />}
      {results.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[560px] text-xs">
            <tbody>
              {results.map((r) => (
                <tr key={r.name} className="border-t border-border align-top">
                  <td className="py-1.5 pr-3 text-foreground">{r.name}</td>
                  <td className="py-1.5 font-mono text-muted-foreground">
                    {Object.entries(r.metrics)
                      .map(([k, v]) => `${k} ${Math.abs(v) >= 100 ? formatNumber(Math.round(v)) : v.toFixed(2)}`)
                      .join(" · ")}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
