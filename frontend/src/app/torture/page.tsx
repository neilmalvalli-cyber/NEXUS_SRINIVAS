"use client";

import { useEffect, useMemo, useState } from "react";
import { useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ExternalLink, FileText, FlaskConical, ListChecks, Loader2, Play, RotateCcw, Square } from "lucide-react";
import { MutationPanel, RecordedSummary, ScenarioBoard, SummaryView, parseResult } from "@/components/torture-extra";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useNexus } from "@/components/providers";
import { useCanAct, useHealth } from "@/lib/queries";
import { toastError } from "@/lib/mutations";
import { ConfirmDialog, CopyButton, EmptyState, ErrorState, KV, LoadingBlock, PageHeader, Panel, RecordedBadge, StatusPill, type Tone } from "@/components/nexus";
import type { RunState, RunStatus } from "@/lib/api/schemas";
import { formatBytes, formatDateTime, formatDuration, formatMs, formatNumber } from "@/lib/format";
import { cn } from "@/lib/utils";
import { RelatedLinks } from "@/components/related-links";

const STATE: Record<RunState, { tone: Tone; label: string }> = {
  running: { tone: "running", label: "Running" },
  passed: { tone: "ok", label: "Passed" },
  failed: { tone: "bad", label: "Failed" },
  inconclusive: { tone: "warn", label: "Inconclusive" },
  interrupted: { tone: "unknown", label: "Interrupted" },
  error: { tone: "bad", label: "Tool error" },
};

function RunStatePill({ state }: { state: RunState }) {
  const s = STATE[state];
  return <StatusPill tone={s.tone}>{s.label}</StatusPill>;
}

const runSchema = z.object({
  mode: z.string().min(1, "Choose a mode"),
  seed: z.string().regex(/^\d{1,18}$/, "1–18 digits"),
  iterations: z.coerce.number<string>().int("Whole number").min(1, "At least 1").max(1000, "At most 1000"),
});
type RunFormIn = z.input<typeof runSchema>;
type RunFormOut = z.output<typeof runSchema>;

function RunForm({ onStarted, disabled, disabledReason }: { onStarted: (id: string) => void; disabled: boolean; disabledReason?: string }) {
  const { api } = useNexus();
  const qc = useQueryClient();
  const modes = useQuery({ queryKey: [api.mode, "torture-modes"], queryFn: api.tortureModes, staleTime: Infinity, retry: false });
  const form = useForm<RunFormIn, unknown, RunFormOut>({
    resolver: zodResolver(runSchema),
    defaultValues: { mode: "default", seed: "12345", iterations: "20" },
  });
  const start = useMutation({
    mutationFn: (f: RunFormOut) => api.startRun(f.mode, f.seed, f.iterations),
    onSuccess: (st) => {
      toast.success("Torture run started", { description: `${st.run.id}: ${st.run.args.join(" ")}` });
      qc.invalidateQueries({ queryKey: [api.mode, "runs"] });
      qc.invalidateQueries({ queryKey: [api.mode, "health"] });
      onStarted(st.run.id);
    },
    onError: (e) => toastError("Start run", e),
  });
  const mode = useWatch({ control: form.control, name: "mode" });
  const desc = modes.data?.modes.find((m) => m.name === mode)?.description;
  const [pending, setPending] = useState<RunFormOut | null>(null);

  if (modes.isError) return <ErrorState error={modes.error} onRetry={() => modes.refetch()} />;
  return (
    <form className="space-y-3" onSubmit={form.handleSubmit((f) => setPending(f))} noValidate>
      <div className="grid gap-3 sm:grid-cols-[1fr_9rem_7rem]">
        <div className="space-y-1">
          <Label htmlFor="mode" className="text-xs text-muted-foreground">Workload mode</Label>
          <select id="mode" className="h-8 w-full rounded-md border border-input bg-background px-2 font-mono text-xs" disabled={modes.isPending} {...form.register("mode")}>
            {modes.isPending && <option>loading…</option>}
            {modes.data?.modes.map((m) => <option key={m.name} value={m.name}>{m.name}</option>)}
          </select>
        </div>
        <div className="space-y-1">
          <Label htmlFor="seed" className="text-xs text-muted-foreground">Seed</Label>
          <Input id="seed" className="h-8 font-mono text-xs" inputMode="numeric" {...form.register("seed")} aria-invalid={!!form.formState.errors.seed} />
          {form.formState.errors.seed && <p className="text-[11px] text-bad">{form.formState.errors.seed.message}</p>}
        </div>
        <div className="space-y-1">
          <Label htmlFor="iterations" className="text-xs text-muted-foreground">Iterations</Label>
          <Input id="iterations" className="h-8 font-mono text-xs" inputMode="numeric" {...form.register("iterations")} aria-invalid={!!form.formState.errors.iterations} />
          {form.formState.errors.iterations && <p className="text-[11px] text-bad">{form.formState.errors.iterations.message}</p>}
        </div>
      </div>
      {desc && <p className="text-xs text-muted-foreground">{desc}</p>}
      <div className="flex items-center gap-3">
        <Button type="submit" disabled={disabled || start.isPending || modes.isPending} title={disabled ? disabledReason : undefined}>
          {start.isPending ? <Loader2 className="animate-spin" /> : <Play />} Start run
        </Button>
        <span className="text-xs text-faint">Each iteration starts a kvdb server, writes under load, kills it at a random point, restarts it and checks every acknowledged write with an independent oracle.</span>
      </div>
      <ConfirmDialog
        open={!!pending}
        onOpenChange={(o) => !o && setPending(null)}
        destructive
        title="Start a destructive torture run?"
        confirmLabel="Start run"
        description={`nexus-torture run --mode ${pending?.mode} --seed ${pending?.seed} --iterations ${pending?.iterations}. Every iteration force-kills kvdb processes mid-write on throw-away databases inside nexus-api's work directory. Your open database is not touched.`}
        onConfirm={() => pending && start.mutate(pending)}
      />
    </form>
  );
}

function ScenarioForm({ onStarted, disabled, disabledReason }: { onStarted: (id: string) => void; disabled: boolean; disabledReason?: string }) {
  const { api } = useNexus();
  const qc = useQueryClient();
  const cat = useQuery({ queryKey: [api.mode, "scenario-catalog"], queryFn: api.scenarioCatalog, staleTime: Infinity, retry: false });
  const [picked, setPicked] = useState<Set<string>>(new Set(["A1"]));
  const [confirm, setConfirm] = useState(false);
  const start = useMutation({
    mutationFn: (ids: string[]) => api.startScenarios(ids),
    onSuccess: (st) => {
      toast.success("Scenario run started", { description: st.run.only?.join(", ") });
      qc.invalidateQueries({ queryKey: [api.mode, "runs"] });
      onStarted(st.run.id);
    },
    onError: (e) => toastError("Start scenarios", e),
  });
  const scenarios = cat.data?.scenarios;
  const groups = useMemo(() => {
    const g = new Map<string, NonNullable<typeof scenarios>>();
    for (const s of scenarios ?? []) g.set(s.group, [...(g.get(s.group) ?? []), s]);
    return [...g];
  }, [scenarios]);
  if (cat.isPending) return <LoadingBlock rows={6} />;
  if (cat.isError) return <ErrorState error={cat.error} onRetry={() => cat.refetch()} />;
  const toggle = (id: string) => setPicked((p) => { const n = new Set(p); if (n.has(id)) n.delete(id); else n.add(id); return n; });
  return (
    <div className="space-y-3">
      <div className="grid max-h-96 gap-3 overflow-y-auto pr-1 md:grid-cols-2">
        {groups.map(([group, items]) => (
          <fieldset key={group} className="rounded-sm border border-border p-2">
            <legend className="px-1 text-[11px] uppercase tracking-wider text-faint">{group}</legend>
            {items.map((s) => (
              <label key={s.id} className="flex cursor-pointer items-start gap-2 rounded-sm px-1 py-1 text-xs hover:bg-hover/60" title={`${s.invariant}\n\nMethod: ${s.method}`}>
                <input type="checkbox" className="mt-0.5 accent-neutral-300" checked={picked.has(s.id)} onChange={() => toggle(s.id)} />
                <span className="font-mono text-muted-foreground">{s.id}</span>
                <span className="text-foreground">{s.title}</span>
              </label>
            ))}
          </fieldset>
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Button disabled={disabled || picked.size === 0 || start.isPending} title={disabled ? disabledReason : undefined} onClick={() => setConfirm(true)}>
          {start.isPending ? <Loader2 className="animate-spin" /> : <ListChecks />} Run {picked.size} scenario{picked.size === 1 ? "" : "s"}
        </Button>
        <Button size="sm" variant="ghost" onClick={() => setPicked(new Set(cat.data.scenarios.map((s) => s.id)))}>Select all</Button>
        <Button size="sm" variant="ghost" onClick={() => setPicked(new Set())}>Clear</Button>
        <span className="text-xs text-faint">Deterministic fault scenarios with kill points (nexus-torture scenarios --only …). A full set takes several minutes.</span>
      </div>
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        destructive
        title="Run destructive scenarios?"
        confirmLabel="Run"
        description={`Scenarios ${[...picked].sort().join(", ")} kill kvdb processes and damage files on purpose, only in disposable databases inside nexus-api's work directory.`}
        onConfirm={() => start.mutate([...picked].sort())}
      />
    </div>
  );
}

function ProgressBar({ done, total, failed }: { done: number; total?: number; failed: number }) {
  const pct = total ? Math.min(100, (done / total) * 100) : 0;
  return (
    <div className="space-y-1">
      <div className="h-1.5 overflow-hidden rounded-full bg-elevated">
        <div className={cn("h-full transition-[width]", failed > 0 ? "bg-bad" : "bg-foreground/70")} style={{ width: `${pct}%` }} />
      </div>
      <div className="font-mono text-[11px] text-faint">{done}{total ? ` / ${total}` : ""} complete</div>
    </div>
  );
}

function RunDetail({ id, onReplay }: { id: string; onReplay?: (id: string) => void }) {
  const { api } = useNexus();
  const qc = useQueryClient();
  const [confirm, setConfirm] = useState(false);
  const q = useQuery({
    queryKey: [api.mode, "run", id],
    queryFn: () => api.runStatus(id),
    refetchInterval: (query) => (query.state.data?.run.state === "running" ? 2_000 : false),
  });
  const running = q.data?.run.state === "running";
  const log = useQuery({
    queryKey: [api.mode, "run-log", id],
    queryFn: () => api.runLog(id, 400),
    refetchInterval: running ? 2_000 : false,
  });
  const prevState = q.data?.run.state;
  useEffect(() => {
    if (prevState && prevState !== "running") {
      qc.invalidateQueries({ queryKey: [api.mode, "runs"] });
      qc.invalidateQueries({ queryKey: [api.mode, "health"] });
    }
  }, [prevState, api.mode, qc]);
  const cancel = useMutation({
    mutationFn: () => api.cancelRun(id),
    onSuccess: (st) => {
      toast.info("Run cancelled", { description: `${st.run.id}: ${STATE[st.run.state].label}` });
      qc.setQueryData([api.mode, "run", id], st);
      qc.invalidateQueries({ queryKey: [api.mode, "runs"] });
      qc.invalidateQueries({ queryKey: [api.mode, "run-log", id] });
    },
    onError: (e) => toastError("Cancel", e),
  });
  const replay = useMutation({
    mutationFn: (iteration: number) => api.replay(id, iteration),
    onSuccess: (st) => {
      toast.success("Replay started", { description: `${st.run.id}: iteration ${st.run.iteration} from the saved pre-crash file` });
      qc.invalidateQueries({ queryKey: [api.mode, "runs"] });
      onReplay?.(st.run.id);
    },
    onError: (e) => toastError("Replay", e),
  });

  if (q.isPending) return <LoadingBlock rows={6} />;
  if (q.isError) return <ErrorState error={q.error} onRetry={() => q.refetch()} />;
  const { run, progress, summary } = q.data;
  const total = run.kind === "run" ? run.iterations : progress?.total;
  const failed = (progress?.failed ?? 0) + (progress?.scenarios?.filter((s) => s.status === "FAIL").length ?? 0);
  const iters = progress?.iterations ?? [];
  const command = ["nexus-torture", ...run.args].join(" ");
  const parsed = parseResult(run.kind, summary);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <RunStatePill state={run.state} />
        <span className="font-mono text-xs text-muted-foreground">{run.id}</span>
        <span className="text-xs text-faint">{formatDuration(q.data.elapsed_seconds)}{running ? " elapsed" : ""}</span>
        {run.exit_code !== null && <span className="font-mono text-xs text-faint">exit {run.exit_code}</span>}
        {running && (
          <Button size="sm" variant="destructive" className="ml-auto" disabled={cancel.isPending} onClick={() => setConfirm(true)}>
            {cancel.isPending ? <Loader2 className="animate-spin" /> : <Square />} Cancel
          </Button>
        )}
      </div>
      {run.error && <ErrorState error={new Error(run.error)} compact />}
      <ProgressBar done={progress?.completed ?? 0} total={total} failed={failed} />
      <div className="grid gap-4 lg:grid-cols-2">
        <KV
          rows={[
            ["Kind", run.kind === "run" ? `workload run · mode ${run.mode}${run.replay_of ? ` · replay of ${run.replay_of} iteration ${run.iteration}` : ""}` : run.kind === "mutants" ? `mutation testing · ${run.only?.join(", ") || "all"}` : `scenarios · ${run.only?.join(", ")}`],
            ...(run.kind === "run"
              ? ([
                  ["Seed", run.seed],
                  ["Passed / failed / inconclusive", `${progress?.passed ?? 0} / ${progress?.failed ?? 0} / ${progress?.inconclusive ?? 0}`],
                  ["Last iteration", progress?.last ? `#${progress.last.iter} ${progress.last.verdict} · ${formatNumber(progress.last.acked_writes)} acked · kill ${progress.last.kill_point || `@${progress.last.kill_after_ms}ms`}` : "—"],
                ] as [string, React.ReactNode][])
              : run.kind === "mutants"
                ? ([["Mutations done", progress?.mutations?.map((m) => `${m.mutation} ${m.verdict}`).join(", ") || "—"]] as [string, React.ReactNode][])
                : ([["Scenarios done", progress?.scenarios?.map((s) => `${s.id} ${s.status}`).join(", ") || "—"]] as [string, React.ReactNode][])),
            ["Started", formatDateTime(run.started_at)],
            ["Ended", run.ended_at ? formatDateTime(run.ended_at) : "—"],
          ]}
        />
        <div className="space-y-1.5">
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <span>Command (run by nexus-api with validated arguments, no shell)</span>
            <CopyButton text={command} />
          </div>
          <pre className="overflow-x-auto rounded-sm border border-border bg-background p-2 font-mono text-[11px] text-muted-foreground">{command}</pre>
        </div>
      </div>

      <Tabs defaultValue="log">
        <TabsList className="bg-elevated">
          <TabsTrigger value="log" className="px-3">Log</TabsTrigger>
          {run.kind === "run" && <TabsTrigger value="iters" className="px-3">Iterations ({iters.length})</TabsTrigger>}
          <TabsTrigger value="summary" className="px-3">Result</TabsTrigger>
        </TabsList>
        <TabsContent value="log">
          {log.isError ? (
            <ErrorState error={log.error} compact />
          ) : (
            <pre className="h-80 overflow-auto rounded-sm border border-border bg-background p-2.5 font-mono text-[11px] leading-relaxed text-muted-foreground" data-testid="run-log">
              {log.data?.lines?.length ? log.data.lines.join("\n") : running ? "waiting for output…" : "(no output)"}
            </pre>
          )}
        </TabsContent>
        {run.kind === "run" && (
          <TabsContent value="iters">
            {iters.length === 0 ? (
              <EmptyState title="No iteration finished yet" />
            ) : (
              <div className="max-h-80 overflow-auto rounded-sm border border-border">
                <Table>
                  <TableHeader>
                    <TableRow className="border-border hover:bg-transparent">
                      <TableHead className="h-7 pl-3 text-xs text-faint">#</TableHead>
                      <TableHead className="h-7 text-xs text-faint">Verdict</TableHead>
                      <TableHead className="h-7 text-xs text-faint">Kill</TableHead>
                      <TableHead className="h-7 text-right text-xs text-faint">Sent</TableHead>
                      <TableHead className="h-7 text-right text-xs text-faint">Acked</TableHead>
                      <TableHead className="h-7 text-right text-xs text-faint">Unknown</TableHead>
                      <TableHead className="h-7 text-right text-xs text-faint">Keys</TableHead>
                      <TableHead className="h-7 text-right text-xs text-faint">Oracle</TableHead>
                      <TableHead className="h-7 pr-3 text-xs text-faint" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {[...iters].reverse().map((it) => (
                      <TableRow key={it.iter} className="border-border" title={it.failure}>
                        <TableCell className="py-1 pl-3 font-mono text-xs">{it.iter}</TableCell>
                        <TableCell className="py-1"><StatusPill tone={it.verdict === "PASS" ? "ok" : it.verdict === "FAIL" ? "bad" : "warn"}>{it.verdict}</StatusPill></TableCell>
                        <TableCell className="py-1 font-mono text-xs text-muted-foreground">{it.kill_point || `${it.kill_after_ms} ms`}</TableCell>
                        <TableCell className="py-1 text-right font-mono text-xs">{formatNumber(it.sent)}</TableCell>
                        <TableCell className="py-1 text-right font-mono text-xs">{formatNumber(it.acked_writes)}</TableCell>
                        <TableCell className="py-1 text-right font-mono text-xs">{formatNumber(it.unknown)}</TableCell>
                        <TableCell className="py-1 text-right font-mono text-xs">{formatNumber(it.keys)}</TableCell>
                        <TableCell className="py-1 text-right font-mono text-xs">{formatMs(it.oracle_ms)}</TableCell>
                        <TableCell className="py-1 pr-3 text-right">
                          {it.verdict !== "PASS" && !running && !run.replay_of && (
                            <Button size="xs" variant="ghost" disabled={replay.isPending} title="Re-run this iteration from the database file saved just before its crash (failures/iter-N/pre/db.kv)" onClick={() => replay.mutate(it.iter)}>
                              <RotateCcw /> Replay
                            </Button>
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </TabsContent>
        )}
        <TabsContent value="summary">
          {parsed?.kind === "run" ? (
            <SummaryView s={parsed.data} />
          ) : parsed?.kind === "scenarios" ? (
            <ScenarioBoard live={parsed.data} />
          ) : parsed?.kind === "mutants" ? (
            <p className="text-xs text-muted-foreground">Mutation report: {parsed.data.all_as_expected ? "every outcome as expected" : "at least one outcome NOT as expected"}. See the Mutation testing panel below for the table.</p>
          ) : summary ? (
            <pre className="max-h-80 overflow-auto rounded-sm border border-border bg-background p-2.5 font-mono text-[11px] text-muted-foreground">{JSON.stringify(summary, null, 2)}</pre>
          ) : (
            <EmptyState title={running ? "The result is written when the run ends" : "No result file"}>
              {running ? "nexus-torture writes summary.json (or scenarios.json) at the end." : "The run ended without writing a result file; see the log."}
            </EmptyState>
          )}
        </TabsContent>
      </Tabs>

      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        destructive
        title="Cancel this run?"
        confirmLabel="Cancel run"
        description="nexus-api stops nexus-torture and its kvdb children. The run is recorded as Interrupted; iterations already finished keep their results."
        onConfirm={() => cancel.mutate()}
      />
    </div>
  );
}

function Evidence() {
  const { api } = useNexus();
  const q = useQuery({ queryKey: [api.mode, "evidence"], queryFn: api.evidence, staleTime: 60_000 });
  return (
    <Panel
      title={<span className="flex items-center gap-2">Recorded validation evidence <RecordedBadge /></span>}
      description="Files from earlier validation runs, served read-only from the evidence directory. These are records, not live results."
      bodyClassName="p-0"
    >
      {q.isPending ? (
        <div className="p-4"><LoadingBlock /></div>
      ) : q.isError ? (
        <div className="p-4"><ErrorState error={q.error} compact /></div>
      ) : !q.data.files?.length ? (
        <EmptyState title="No evidence files">The evidence directory is empty or missing.</EmptyState>
      ) : (
        <ul className="max-h-72 divide-y divide-border overflow-y-auto">
          {q.data.files.map((f) => {
            const url = api.evidenceFileUrl(f.name);
            return (
              <li key={f.name} className="flex items-center gap-3 px-4 py-1.5 text-xs">
                <FileText className="size-3.5 shrink-0 text-faint" />
                {url ? (
                  <a href={url} target="_blank" rel="noreferrer" className="truncate font-mono text-muted-foreground hover:text-foreground hover:underline">
                    {f.name}
                  </a>
                ) : (
                  <span className="truncate font-mono">{f.name}</span>
                )}
                <span className="ml-auto shrink-0 font-mono text-faint">{formatBytes(f.size)}</span>
                <span className="w-36 shrink-0 text-right font-mono text-faint">{formatDateTime(f.modified)}</span>
                {url && <ExternalLink className="size-3 shrink-0 text-faint" />}
              </li>
            );
          })}
        </ul>
      )}
    </Panel>
  );
}

export default function TorturePage() {
  const { api, mode } = useNexus();
  const health = useHealth();
  const { ready, reason } = useCanAct();
  const runs = useQuery({ queryKey: [api.mode, "runs"], queryFn: api.listRuns, enabled: mode === "live", refetchInterval: health.data?.torture_running ? 5_000 : false });
  const [selected, setSelected] = useState<string | null>(null);
  const tortureRuns = runs.data?.runs.filter((r) => r.run.kind === "run" || r.run.kind === "scenarios" || r.run.kind === "mutants") ?? [];
  const current = selected ?? tortureRuns[0]?.run.id ?? null;
  const lastScenarios = tortureRuns.find((r) => r.run.kind === "scenarios" && r.summary);
  const lastMutants = tortureRuns.find((r) => r.run.kind === "mutants" && r.summary);
  const liveScen = lastScenarios ? parseResult("scenarios", lastScenarios.summary) : null;
  const liveMut = lastMutants ? parseResult("mutants", lastMutants.summary) : null;
  const toolMissing = health.data ? !health.data.tools.nexus_torture : false;
  const busy = !!health.data?.torture_running || !!runs.data?.runs.some((r) => r.run.state === "running");
  const disabled = !ready || toolMissing || busy;
  const disabledReason = !ready ? reason : toolMissing ? "nexus-torture executable not found" : busy ? "A run is already in progress" : undefined;

  if (mode === "demo")
    return (
      <div className="space-y-6">
        <PageHeader title="Torture Lab" />
        <EmptyState icon={FlaskConical} title="The torture lab is not simulated in demo mode">
          Torture results must come from real runs of nexus-torture. Switch the data source to Live API.
        </EmptyState>
      </div>
    );

  return (
    <div className="space-y-6">
      <PageHeader
        title="Torture Lab"
        description="Run the real crash-and-verify tester (torture2/cmd/nexus-torture) through nexus-api. One run at a time; results come from the run's own files."
        actions={
          health.data &&
          (toolMissing ? <StatusPill tone="bad">Blocked: tester not found</StatusPill> : busy ? <StatusPill tone="running">run in progress</StatusPill> : <StatusPill tone="ok">tester ready</StatusPill>)
        }
      />
      <RelatedLinks
        links={[
          { href: "/reliability", title: "Power-Cut Explorer (Reliability Lab)", text: "Process kills here test what the OS kept; the explorer models what a disk could keep after power loss.", scope: "test" },
          { href: "/xray?target=lab:torn-tail", title: "X-RAY diagnostic lab", text: "Damaged test databases and the structured findings X-RAY produces for each. Never counted as live health.", scope: "test" },
        ]}
      />
      {toolMissing && (
        <ErrorState error={new Error("BLOCKED: nexus-api did not find nexus-torture (and needs kvdb too). Build both with `go build -o . ./src/cmd/kvdb ./torture2/cmd/nexus-torture` and restart nexus-api, or pass --torture and --kvdb.")} />
      )}

      <Panel title="New run">
        <Tabs defaultValue="workload">
          <TabsList className="bg-elevated">
            <TabsTrigger value="workload" className="px-3">Workload</TabsTrigger>
            <TabsTrigger value="scenarios" className="px-3">Scenarios</TabsTrigger>
          </TabsList>
          <TabsContent value="workload" className="pt-3">
            <RunForm onStarted={setSelected} disabled={disabled} disabledReason={disabledReason} />
          </TabsContent>
          <TabsContent value="scenarios" className="pt-3">
            <ScenarioForm onStarted={setSelected} disabled={disabled} disabledReason={disabledReason} />
          </TabsContent>
        </Tabs>
      </Panel>

      <div className="grid gap-4 xl:grid-cols-[18rem_1fr]">
        <Panel title="Runs" description="Started by this nexus-api process." bodyClassName="p-0">
          {runs.isPending ? (
            <div className="p-4"><LoadingBlock /></div>
          ) : runs.isError ? (
            <div className="p-4"><ErrorState error={runs.error} onRetry={() => runs.refetch()} compact /></div>
          ) : tortureRuns.length === 0 ? (
            <EmptyState title="No runs yet">Start a workload or scenario run above.</EmptyState>
          ) : (
            <ul className="max-h-[32rem] overflow-y-auto">
              {tortureRuns.map((r: RunStatus) => (
                <li key={r.run.id}>
                  <button
                    type="button"
                    onClick={() => setSelected(r.run.id)}
                    className={cn("flex w-full flex-col gap-1 border-b border-border px-4 py-2 text-left text-xs hover:bg-hover/60", current === r.run.id && "bg-hover")}
                  >
                    <span className="flex items-center gap-2">
                      <RunStatePill state={r.run.state} />
                      <span className="truncate font-mono text-muted-foreground">{r.run.kind === "run" ? `${r.run.mode}${r.run.replay_of ? ` replay #${r.run.iteration}` : ""}` : r.run.kind === "mutants" ? "mutation testing" : `scenarios ${r.run.only?.join(",")}`}</span>
                    </span>
                    <span className="font-mono text-faint">{r.run.id} · {formatDuration(r.elapsed_seconds)}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </Panel>
        <Panel title="Run detail">
          {current ? <RunDetail key={current} id={current} onReplay={setSelected} /> : <EmptyState title="Select or start a run" />}
        </Panel>
      </div>

      <Panel
        title="Scenarios"
        description="The 32 fault scenarios defined by the tester, grouped A to F. Each shows its latest actual result: from a scenario run in this session when there is one, otherwise from the recorded validation run (marked with a hollow dot)."
      >
        <ScenarioBoard live={liveScen?.kind === "scenarios" ? liveScen.data : null} />
      </Panel>

      <Panel title="Mutation testing" description="Does the tester actually catch bugs? Each mutation is a deliberately broken engine.">
        <MutationPanel disabled={disabled} disabledReason={disabledReason} onStarted={setSelected} liveReport={liveMut?.kind === "mutants" ? liveMut.data : null} />
      </Panel>

      <RecordedSummary />

      <Evidence />
    </div>
  );
}
