"use client";

import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Bug, CheckCircle2, CircleHelp, Loader2, XCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useNexus } from "@/components/providers";
import { toastError } from "@/lib/mutations";
import { ConfirmDialog, EmptyState, ErrorState, KV, LoadingBlock, Panel, RecordedBadge, StatCard, StatusPill, type Tone } from "@/components/nexus";
import { MutationReport, ScenarioResults, TortureSummary } from "@/lib/api/schemas";
import { formatDuration, formatMs, formatNumber } from "@/lib/format";
import { cn } from "@/lib/utils";

export const RECORDED = {
  summary: "torture/default-500-seed12345.summary.json",
  scenarios: "torture/scenarios-quick.json",
  mutation: "torture/mutation-report.json",
};

function statusTone(s: string): Tone {
  const u = s.toUpperCase();
  if (u === "PASS" || u === "PASSED") return "ok";
  if (u === "FAIL" || u === "FAILED") return "bad";
  if (u === "RUNNING") return "running";
  return "warn";
}

/** The verification counters of a nexus-torture summary.json, exactly as the tester wrote them. */
export function SummaryView({ s, recorded }: { s: TortureSummary; recorded?: boolean }) {
  const v = s.verification;
  const violations = v.batch_atomicity_violations + v.data_integrity_violations + v.recovery_failures + v.protocol_or_liveness_failures;
  const perf = s.performance as Record<string, number> | undefined;
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <StatusPill tone={statusTone(s.result)}>{s.result}</StatusPill>
        <span className="font-mono text-muted-foreground">mode {s.mode} · seed {s.seed}</span>
        {recorded && <RecordedBadge />}
      </div>
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard label="Iterations" value={`${formatNumber(s.iterations_completed)} / ${formatNumber(s.iterations_planned)}`} hint={`${s.passed} passed · ${s.failed} failed · ${s.inconclusive} inconclusive`} />
        <StatCard label="Acknowledged writes" value={formatNumber(v.acknowledged_writes)} hint={`${formatNumber(v.acknowledged_writes_verified_after_restart)} verified after restart`} />
        <StatCard label="Integrity violations" value={formatNumber(violations)} hint="data, batch atomicity, recovery, protocol" />
        <StatCard label="Duration" value={perf?.total_sec !== undefined ? formatDuration(perf.total_sec) : "—"} hint={perf?.recovery_ms_avg !== undefined ? `recovery avg ${formatMs(perf.recovery_ms_avg)}` : undefined} />
      </div>
      <KV
        className="text-xs"
        rows={[
          ["Operations attempted", formatNumber(v.operations_attempted)],
          ["Acknowledged", formatNumber(v.acknowledged)],
          ["Cleanly rejected", formatNumber(v.cleanly_rejected)],
          ["Unknown outcome (in flight at the kill)", formatNumber(v.unknown_outcome)],
          ["Reads verified", formatNumber(v.reads_verified)],
          ["Batch atomicity violations", formatNumber(v.batch_atomicity_violations)],
          ["Data integrity violations", formatNumber(v.data_integrity_violations)],
          ["Recovery failures", formatNumber(v.recovery_failures)],
          ["Protocol / liveness failures", formatNumber(v.protocol_or_liveness_failures)],
          ["Oracle inconclusive", formatNumber(v.oracle_inconclusive)],
          ...(s.kills_with_write_in_flight !== undefined ? ([["Kills with a write in flight", formatNumber(s.kills_with_write_in_flight)]] as [string, string][]) : []),
          ...(s.recoveries_discarding_torn_tail !== undefined ? ([["Recoveries that discarded a torn tail", formatNumber(s.recoveries_discarding_torn_tail)]] as [string, string][]) : []),
          ...(s.recoveries_resolving_interrupted_compaction !== undefined
            ? ([["Recoveries resolving an interrupted compaction", formatNumber(s.recoveries_resolving_interrupted_compaction)]] as [string, string][])
            : []),
        ]}
      />
    </div>
  );
}

export function RecordedSummary() {
  const { api } = useNexus();
  const q = useQuery({ queryKey: [api.mode, "evidence-json", RECORDED.summary], queryFn: () => api.evidenceJson(RECORDED.summary, TortureSummary), staleTime: Infinity, retry: false });
  return (
    <Panel title={<span className="flex items-center gap-2">Recorded default run <RecordedBadge /></span>} description={`${RECORDED.summary}: a 500-iteration run from the final validation. Machine-dependent duration; the counters are what the tester wrote.`}>
      {q.isPending ? <LoadingBlock rows={4} /> : q.isError ? <ErrorState error={q.error} compact /> : <SummaryView s={q.data} recorded />}
    </Panel>
  );
}

type ScenarioRow = ScenarioResults["results"][number];

/** All 32 scenarios from the tester's own catalog, with the latest actual result from a live run or, failing that, the recorded run. */
export function ScenarioBoard({ live }: { live?: ScenarioResults | null }) {
  const { api } = useNexus();
  const cat = useQuery({ queryKey: [api.mode, "scenario-catalog"], queryFn: api.scenarioCatalog, staleTime: Infinity, retry: false });
  const rec = useQuery({ queryKey: [api.mode, "evidence-json", RECORDED.scenarios], queryFn: () => api.evidenceJson(RECORDED.scenarios, ScenarioResults), staleTime: Infinity, retry: false });
  const [sel, setSel] = useState<string | null>(null);
  const actual = useMemo(() => {
    const m = new Map<string, { r: ScenarioRow; src: "live" | "recorded" }>();
    for (const r of rec.data?.results ?? []) m.set(r.id, { r, src: "recorded" });
    for (const r of live?.results ?? []) m.set(r.id, { r, src: "live" });
    return m;
  }, [rec.data, live]);
  const scenarios = cat.data?.scenarios;
  const groups = useMemo(() => {
    const g = new Map<string, NonNullable<typeof scenarios>>();
    for (const s of scenarios ?? []) g.set(s.group, [...(g.get(s.group) ?? []), s]);
    return [...g];
  }, [scenarios]);

  if (cat.isPending) return <LoadingBlock rows={6} />;
  if (cat.isError) return <ErrorState error={cat.error} onRetry={() => cat.refetch()} />;
  const chosen = sel ? cat.data.scenarios.find((s) => s.id === sel) : undefined;
  const res = sel ? actual.get(sel) : undefined;
  const counts = { pass: 0, fail: 0, other: 0 };
  for (const s of cat.data.scenarios) {
    const a = actual.get(s.id);
    if (!a) continue;
    const t = statusTone(a.r.status);
    if (t === "ok") counts.pass++;
    else if (t === "bad") counts.fail++;
    else counts.other++;
  }
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
        <span>{cat.data.scenarios.length} scenarios from <span className="font-mono">{cat.data.source}</span></span>
        <span className="text-ok">{counts.pass} pass</span>
        <span className={counts.fail ? "text-bad" : ""}>{counts.fail} fail</span>
        <span>{counts.other} other</span>
        <span>{cat.data.scenarios.length - actual.size > 0 ? `${cat.data.scenarios.length - actual.size} without a result` : ""}</span>
        <span className="ml-auto flex items-center gap-2"><span className="size-2 rounded-full bg-foreground/70" /> live result <span className="size-2 rounded-full border border-faint" /> recorded result</span>
      </div>
      <div className="grid gap-3 lg:grid-cols-[1.3fr_1fr]">
        <div className="grid gap-2 sm:grid-cols-2">
          {groups.map(([group, items]) => (
            <div key={group} className="rounded-sm border border-border p-2">
              <div className="px-1 pb-1 text-[11px] uppercase tracking-wider text-faint">Group {group}</div>
              {items.map((s) => {
                const a = actual.get(s.id);
                return (
                  <button
                    key={s.id}
                    type="button"
                    onClick={() => setSel(s.id)}
                    className={cn("flex w-full items-center gap-2 rounded-sm px-1 py-1 text-left text-xs hover:bg-hover/60", sel === s.id && "bg-hover")}
                  >
                    <span className={cn("size-2 shrink-0 rounded-full", a ? (a.src === "live" ? "bg-foreground/70" : "border border-faint") : "bg-transparent")} />
                    <span className="w-6 shrink-0 font-mono text-muted-foreground">{s.id}</span>
                    <span className="truncate text-foreground">{s.title}</span>
                    <span className="ml-auto shrink-0">{a ? <StatusPill tone={statusTone(a.r.status)}>{a.r.status}</StatusPill> : <StatusPill tone="unknown">not run</StatusPill>}</span>
                  </button>
                );
              })}
            </div>
          ))}
        </div>
        <div className="rounded-sm border border-border bg-background p-3 text-xs">
          {!chosen ? (
            <p className="text-faint">Select a scenario to see what it checks, how, and the kill points of its latest result.</p>
          ) : (
            <div className="space-y-2">
              <div className="flex items-center gap-2">
                <span className="font-mono text-muted-foreground">{chosen.id}</span>
                <span className="text-sm text-foreground">{chosen.title}</span>
              </div>
              <KV
                rows={[
                  ["Expected (invariant)", chosen.invariant],
                  ["Method", chosen.method],
                  ["Actual", res ? `${res.r.status}${res.src === "recorded" ? " (recorded run)" : " (live run)"}${res.r.seconds !== undefined ? ` in ${formatDuration(res.r.seconds)}` : ""}` : "not run yet"],
                ]}
                className="[&_dd]:font-sans"
              />
              {res?.r.detail && <p className="text-muted-foreground">{res.r.detail}</p>}
              {res?.r.kill_points?.length ? (
                <ul className="max-h-64 space-y-1 overflow-y-auto">
                  {res.r.kill_points.map((k, i) => (
                    <li key={i} className="flex items-start gap-1.5">
                      {k.ok ? <CheckCircle2 className="mt-0.5 size-3 shrink-0 text-ok" /> : <XCircle className="mt-0.5 size-3 shrink-0 text-bad" />}
                      <span className="text-muted-foreground">{k.point}{k.outcome ? <span className="text-faint"> → {k.outcome}</span> : null}</span>
                    </li>
                  ))}
                </ul>
              ) : null}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

/** Mutation testing: deliberately broken engines must be caught. The control ("none") must pass. */
export function MutationPanel({ disabled, disabledReason, onStarted, liveReport }: { disabled: boolean; disabledReason?: string; onStarted: (id: string) => void; liveReport?: MutationReport | null }) {
  const { api } = useNexus();
  const qc = useQueryClient();
  const cat = useQuery({ queryKey: [api.mode, "mutant-catalog"], queryFn: api.mutantCatalog, staleTime: Infinity, retry: false });
  const rec = useQuery({ queryKey: [api.mode, "evidence-json", RECORDED.mutation], queryFn: () => api.evidenceJson(RECORDED.mutation, MutationReport), staleTime: Infinity, retry: false });
  const [confirm, setConfirm] = useState<string[] | null>(null);
  const start = useMutation({
    mutationFn: (only: string[]) => api.startMutants(only, 40),
    onSuccess: (st) => {
      toast.success("Mutation run started", { description: st.run.id });
      qc.invalidateQueries({ queryKey: [api.mode, "runs"] });
      onStarted(st.run.id);
    },
    onError: (e) => toastError("Start mutation run", e),
  });
  const report = liveReport ?? rec.data;
  const fromLive = !!liveReport;
  if (cat.isPending) return <LoadingBlock rows={4} />;
  if (cat.isError) return <ErrorState error={cat.error} compact />;
  const outcome = (m: string) => report?.outcomes.find((o) => o.mutation === m);
  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-foreground">
        kvdb-mutant is the real engine wrapped with one deliberate bug at a time. A good tester must report FAIL for each bug. The first row, <span className="font-mono">none</span>, is the control: an unmodified engine that must PASS. A passing control is not a detection; it shows the tester does not raise false alarms.
      </p>
      <div className="overflow-x-auto rounded-sm border border-border">
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b border-border text-left text-faint">
              <th className="px-3 py-1.5 font-normal">Mutation</th>
              <th className="px-3 py-1.5 font-normal">Simulates</th>
              <th className="px-3 py-1.5 font-normal">Expected</th>
              <th className="px-3 py-1.5 font-normal">Actual {report && (fromLive ? "(live)" : "(recorded)")}</th>
              <th className="px-3 py-1.5 font-normal" />
            </tr>
          </thead>
          <tbody>
            {cat.data.mutations.map((m) => {
              const o = outcome(m.mutation);
              const control = m.mutation === "none";
              return (
                <tr key={m.mutation} className="border-b border-border last:border-0" title={o?.failure}>
                  <td className="px-3 py-1.5 font-mono">{m.mutation}{control && <span className="ml-1.5 text-faint">(control)</span>}</td>
                  <td className="px-3 py-1.5 text-muted-foreground">{m.simulates}</td>
                  <td className="px-3 py-1.5 text-muted-foreground">{control ? "PASS, no failure" : `FAIL: ${m.expected_failure_kinds?.join(" / ")}`}</td>
                  <td className="px-3 py-1.5">
                    {o ? (
                      <span className="flex flex-wrap items-center gap-1.5">
                        <StatusPill tone={o.as_expected ? "ok" : "bad"}>{control ? (o.engine_result === "PASS" ? "control passed" : "false alarm") : o.detected ? "detected" : "missed"}</StatusPill>
                        <span className="font-mono text-faint">{o.engine_result}{o.failure_kind ? ` · ${o.failure_kind}` : ""}{o.detected_at_iteration ? ` · iter ${o.detected_at_iteration}` : ""}</span>
                      </span>
                    ) : (
                      <StatusPill tone="unknown">not run</StatusPill>
                    )}
                  </td>
                  <td className="px-3 py-1.5 text-right">
                    <Button size="xs" variant="ghost" disabled={disabled || !cat.data.available || start.isPending} title={!cat.data.available ? "kvdb-mutant not found by nexus-api" : disabledReason} onClick={() => setConfirm([m.mutation])}>
                      <Bug /> Run
                    </Button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" disabled={disabled || !cat.data.available || start.isPending} title={!cat.data.available ? "kvdb-mutant not found by nexus-api" : disabledReason} onClick={() => setConfirm(cat.data.mutations.map((m) => m.mutation))}>
          {start.isPending ? <Loader2 className="animate-spin" /> : <Bug />} Run all {cat.data.mutations.length}
        </Button>
        {!cat.data.available && <span className="flex items-center gap-1 text-xs text-warn"><CircleHelp className="size-3.5" /> kvdb-mutant was not found; showing the recorded report only.</span>}
        {report && <span className="text-xs text-faint">{report.all_as_expected ? "Every outcome as expected." : "At least one outcome was not as expected."} seed {report.seed}</span>}
      </div>
      <ConfirmDialog
        open={!!confirm}
        onOpenChange={(o) => !o && setConfirm(null)}
        destructive
        title="Run mutation testing?"
        confirmLabel="Run"
        description={`Runs nexus-torture mutants for ${confirm?.join(", ")} with up to 40 iterations each. It starts and kills kvdb-mutant processes on disposable databases in nexus-api's work directory; your open database is not touched. This can take a few minutes.`}
        onConfirm={() => confirm && start.mutate(confirm)}
      />
    </div>
  );
}

/** Parse a run's result file with the right schema, or null when it has another shape. */
export function parseResult(kind: string, summary: unknown) {
  if (!summary) return null;
  if (kind === "run") {
    const r = TortureSummary.safeParse(summary);
    return r.success ? { kind: "run" as const, data: r.data } : null;
  }
  if (kind === "scenarios") {
    const r = ScenarioResults.safeParse(summary);
    return r.success ? { kind: "scenarios" as const, data: r.data } : null;
  }
  if (kind === "mutants") {
    const r = MutationReport.safeParse(summary);
    return r.success ? { kind: "mutants" as const, data: r.data } : null;
  }
  return null;
}

export function NoResult() {
  return <EmptyState title="No parsed result" />;
}
