"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Gauge, Loader2, Play } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useNexus } from "@/components/providers";
import { useCanAct, useHealth } from "@/lib/queries";
import { toastError } from "@/lib/mutations";
import { EmptyState, ErrorState, LoadingBlock, PageHeader, Panel, RecordedBadge, StatusPill } from "@/components/nexus";
import { BenchReport } from "@/lib/api/schemas";
import { formatDateTime, formatNumber } from "@/lib/format";
import { Headline, LiveRun, PhaseTable, RECORDED_BENCH } from "@/components/bench-view";
import { LivePerfPanel, OptimizationHistory, usePerfHistory } from "@/components/perf-view";

export default function BenchmarkPage() {
  const { api, mode } = useNexus();
  const qc = useQueryClient();
  const health = useHealth();
  const { ready, reason } = useCanAct();
  const recorded = useQuery({ queryKey: [api.mode, "evidence-json", RECORDED_BENCH], queryFn: () => api.evidenceJson(RECORDED_BENCH, BenchReport), staleTime: Infinity, retry: false });
  const runs = useQuery({ queryKey: [api.mode, "runs"], queryFn: api.listRuns, enabled: mode === "live", refetchInterval: health.data?.torture_running ? 3_000 : false });
  const benchRuns = runs.data?.runs.filter((r) => r.run.kind === "bench") ?? [];
  const [selected, setSelected] = useState<string | null>(null);
  const current = selected ?? benchRuns[0]?.run.id ?? null;
  const [n, setN] = useState("10000");
  const [nRuns, setNRuns] = useState("1");
  const [valueSize, setValueSize] = useState("100");
  const history = usePerfHistory();
  const toolMissing = health.data ? !health.data.tools.kvdb_bench : false;
  const busy = !!health.data?.torture_running || !!runs.data?.runs.some((r) => r.run.state === "running");
  const nv = Number(n), rv = Number(nRuns), vv = Number(valueSize);
  const invalid = !Number.isInteger(nv) || nv < 2000 || nv > 100000 || !Number.isInteger(rv) || rv < 1 || rv > 3 || !Number.isInteger(vv) || vv < 0 || vv > 65536;
  const start = useMutation({
    mutationFn: () => api.startBench({ n: nv, runs: rv, value_size: vv, batch_size: 1000 }),
    onSuccess: (st) => {
      toast.success("Benchmark started", { description: st.run.args.join(" ") });
      qc.invalidateQueries({ queryKey: [api.mode, "runs"] });
      qc.invalidateQueries({ queryKey: [api.mode, "health"] });
      setSelected(st.run.id);
    },
    onError: (e) => toastError("Start benchmark", e),
  });
  const disabled = mode !== "live" || !ready || toolMissing || busy || invalid;
  const disabledReason = mode !== "live" ? "Live API only" : !ready ? reason : toolMissing ? "kvdb-bench executable not found" : busy ? "Another run is in progress" : invalid ? "Check the inputs" : undefined;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Benchmark"
        description="Throughput and timing of the real engine, measured by src/cmd/kvdb-bench. Every PUT and DELETE is synced to disk before it returns, so their rate is bounded by the disk's fsync latency. Numbers depend on the machine and disk."
        actions={health.data && (toolMissing ? <StatusPill tone="bad">kvdb-bench not found</StatusPill> : <StatusPill tone="ok">kvdb-bench ready</StatusPill>)}
      />

      <Panel
        title={<span className="flex items-center gap-2">Recorded result <RecordedBadge /></span>}
        description={recorded.data ? `docs/results/final/${RECORDED_BENCH} · ${formatNumber(Number(recorded.data.config.n))} keys × ${recorded.data.config.runs} runs (median) · ${recorded.data.machine.cpu}, ${recorded.data.machine.os} · recorded ${formatDateTime(recorded.data.started)}. Historical and machine-dependent.` : "Historical result from the final validation."}
      >
        {recorded.isPending ? (
          <LoadingBlock rows={4} />
        ) : recorded.isError ? (
          <ErrorState error={recorded.error} compact />
        ) : (
          <div className="space-y-4">
            <Headline r={recorded.data} />
            <PhaseTable r={recorded.data} />
            {recorded.data.notes?.length ? (
              <ul className="list-disc space-y-1 pl-5 text-[11px] text-faint">
                {recorded.data.notes.map((t) => <li key={t}>{t}</li>)}
              </ul>
            ) : null}
          </div>
        )}
      </Panel>

      {mode === "live" && (
        <Panel
          title={<span className="flex items-center gap-2">Optimization history <RecordedBadge when={history.data?.modified} /></span>}
          description="Measured with src/cmd/kvdb-perf while the engine was optimized (Prompt 19): one change at a time, same machine, same workload, every write fsynced before it returns. Historical; never mixed with live runs."
        >
          {history.isPending ? <LoadingBlock rows={6} /> : history.isError ? <ErrorState error={history.error} compact /> : <OptimizationHistory data={history.data} />}
        </Panel>
      )}

      <LivePerfPanel />

      {mode !== "live" ? (
        <EmptyState icon={Gauge} title="Live benchmarks need the Live API">Switch the data source to Live API to run kvdb-bench.</EmptyState>
      ) : (
        <>
          <Panel title="Run a benchmark now" description="nexus-api runs kvdb-bench in its own scratch directory; your open database is not used. 10,000 keys take roughly 10–20 s on an SSD; 100,000 keys take minutes.">
            <div className="flex flex-wrap items-end gap-3">
              <div className="space-y-1">
                <Label htmlFor="bn" className="text-xs text-muted-foreground">Keys (n, 2,000–100,000)</Label>
                <Input id="bn" className="h-8 w-36 font-mono text-xs" inputMode="numeric" value={n} onChange={(e) => setN(e.target.value)} />
              </div>
              <div className="space-y-1">
                <Label htmlFor="br" className="text-xs text-muted-foreground">Runs (1–3, median)</Label>
                <Input id="br" className="h-8 w-24 font-mono text-xs" inputMode="numeric" value={nRuns} onChange={(e) => setNRuns(e.target.value)} />
              </div>
              <div className="space-y-1">
                <Label htmlFor="bv" className="text-xs text-muted-foreground">Value size (bytes)</Label>
                <Input id="bv" className="h-8 w-28 font-mono text-xs" inputMode="numeric" value={valueSize} onChange={(e) => setValueSize(e.target.value)} />
              </div>
              <Button disabled={disabled || start.isPending} title={disabledReason} onClick={() => start.mutate()}>
                {start.isPending ? <Loader2 className="animate-spin" /> : <Play />} Run kvdb-bench
              </Button>
              {invalid && <span className="text-xs text-bad">n 2,000–100,000 · runs 1–3 · value 0–65,536</span>}
            </div>
          </Panel>

          {benchRuns.length > 0 && (
            <Panel
              title="Live result"
              description="Measured just now on this machine by the run below."
              actions={
                benchRuns.length > 1 ? (
                  <select className="h-7 rounded-md border border-input bg-background px-2 font-mono text-xs" value={current ?? ""} onChange={(e) => setSelected(e.target.value)}>
                    {benchRuns.map((r) => <option key={r.run.id} value={r.run.id}>{r.run.id}</option>)}
                  </select>
                ) : undefined
              }
            >
              {current && <LiveRun key={current} id={current} recorded={recorded.data} />}
            </Panel>
          )}
        </>
      )}
    </div>
  );
}
