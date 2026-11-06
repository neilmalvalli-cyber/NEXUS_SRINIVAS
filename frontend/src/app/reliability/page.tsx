"use client";

// Reliability Lab: the NEXUS Power-Cut Explorer (src/internal/powercut).
// It records the real engine's writes and syncs, builds modeled power-loss
// disk images from that trace, and opens each image with the real recovery.
// A run is a finite, seeded sample of modeled states, never a proof that
// every possible power loss is survived; the page says so wherever results
// are shown.
import { useState } from "react";
import Link from "next/link";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { AlertTriangle, Loader2, Play, ScanSearch, Square, Zap } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useNexus } from "@/components/providers";
import { useCanAct, useHealth } from "@/lib/queries";
import { toastError } from "@/lib/mutations";
import { CopyButton, EmptyState, ErrorState, KV, LoadingBlock, PageHeader, Panel, RecordedBadge, StatCard, StatusPill } from "@/components/nexus";
import { FindingCard, KindBadge } from "@/components/xray-view";
import { PowercutReport, type PowercutConfig } from "@/lib/api/xray-schemas";
import { formatBytes, formatDateTime, formatDuration, formatNumber } from "@/lib/format";

function Disclaimer({ text }: { text: string }) {
  return (
    <div className="flex items-start gap-2 rounded-md border border-warn/30 bg-warn/5 px-3 py-2 text-xs text-foreground" role="note">
      <AlertTriangle className="mt-0.5 size-4 shrink-0 text-warn" />
      <span>{text}</span>
    </div>
  );
}

function CountTable({ title, data, note }: { title: string; data: Record<string, number>; note?: string }) {
  const rows = Object.entries(data).sort((a, b) => b[1] - a[1]);
  return (
    <div>
      <div className="mb-1 text-[11px] uppercase tracking-wider text-faint">{title}</div>
      <table className="w-full text-xs">
        <tbody>
          {rows.map(([k, v]) => (
            <tr key={k} className="border-t border-border">
              <td className="py-1 pr-3 font-mono text-muted-foreground">{k}</td>
              <td className="py-1 text-right font-mono text-foreground">{formatNumber(v)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {note && <p className="mt-1 text-[11px] text-faint">{note}</p>}
    </div>
  );
}

/** X-RAY report of one failed or control image, loaded on demand. */
function ImageXRay({ runId, image }: { runId: string; image: string }) {
  const { api } = useNexus();
  const q = useQuery({ queryKey: [api.mode, "powercut-xray", runId, image], queryFn: () => api.powercutImageXRay(runId, image), retry: false });
  if (q.isPending) return <LoadingBlock rows={2} />;
  if (q.isError) return <ErrorState error={q.error} compact />;
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <KindBadge kind="powercut" /> <span className="font-mono text-muted-foreground">{q.data.health} · {q.data.engine.outcome}</span>
      </div>
      {q.data.findings.filter((f) => f.code !== "SCAN_CLEAN").map((f) => <FindingCard key={f.id} f={f} />)}
    </div>
  );
}

function PowercutResult({ report, runId }: { report: PowercutReport; runId?: string }) {
  const [openImg, setOpenImg] = useState<string | null>(null);
  const controls = report.negative_controls ?? [];
  const failures = report.failures ?? [];
  const detected = controls.filter((c) => c.detected).length;
  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatCard label="Crash points tested" value={`${formatNumber(report.crash_points_tested)} / ${formatNumber(report.crash_points_in_trace)}`} hint="trace positions where power was cut" />
        <StatCard label="Modeled images validated" value={formatNumber(report.images_validated)} hint="each opened with the real recovery" />
        <StatCard label="Failures" value={formatNumber(report.failed)} tone={report.failed ? "bad" : "ok"} hint={`${formatNumber(report.passed)} passed`} />
        <StatCard label="Negative controls detected" value={`${detected} / ${controls.length}`} tone={controls.length && detected === controls.length ? "ok" : controls.length ? "bad" : "unknown"} hint="deliberate violations of the model" />
      </div>
      <p className="text-sm text-foreground">{report.verdict}</p>
      <div className="grid gap-5 lg:grid-cols-3">
        <CountTable title="Recovery outcome per image" data={report.outcomes} note="Rule names (C1, C4, A3…) are what the engine's recovery decided for that image." />
        <CountTable title="Images per variant family" data={report.images_by_variant_family} />
        <CountTable title="Oracle checks performed" data={report.checks} />
      </div>
      <KV
        className="text-xs"
        rows={[
          ["Recorded trace", `${formatNumber(report.trace.events)} events · ${formatBytes(report.trace.bytes_written)} written · ${formatNumber(report.trace.frames_written)} frames`],
          ["Trace operations", Object.entries(report.trace.by_op).map(([k, v]) => `${k} ${v}`).join(" · ")],
          ["Workload", `${formatNumber(report.trace.acknowledged_ops)} of ${formatNumber(report.trace.workload_ops)} ops acknowledged · ${Object.entries(report.trace.by_kind).map(([k, v]) => `${k} ${v}`).join(" · ")}`],
          ["Configuration", `seed ${report.config.seed} · ${report.config.writers} writers × ${report.config.ops_per_writer} ops · ≤${report.config.max_crash_points} points · ≤${report.config.max_variants_per_point} variants/point`],
          ["Environment", `${report.environment.goos}/${report.environment.goarch} · ${report.environment.go_version} · ${report.environment.cpus} CPUs`],
          ["Run", `${formatDateTime(report.started_at)} · ${formatDuration(report.duration_ms / 1000)}`],
          ["Reproduce", <span key="r" className="flex flex-wrap items-center gap-2"><code className="break-all">{report.reproduce}</code><CopyButton text={report.reproduce} /></span>],
        ]}
      />

      {failures.length > 0 && (
        <div className="space-y-2">
          <div className="text-[11px] uppercase tracking-wider text-bad">Failures ({failures.length})</div>
          {failures.map((f) => (
            <div key={f.image} className="rounded-md border border-bad/40 p-3 text-xs">
              <div className="flex flex-wrap items-center gap-2">
                <StatusPill tone="bad">{f.kind}</StatusPill>
                <span className="font-mono text-foreground">{f.image}</span>
                <span className="text-muted-foreground">point {f.crash_point} · {f.variant}</span>
              </div>
              <p className="mt-1 text-foreground">{f.detail}</p>
              {f.xray && (
                <ul className="mt-1 list-disc pl-5 font-mono text-[11px] text-muted-foreground">
                  {(f.xray.findings ?? []).map((s) => <li key={s}>{s}</li>)}
                </ul>
              )}
              {runId && (
                <Button size="xs" variant="outline" className="mt-2" onClick={() => setOpenImg(openImg === f.image ? null : f.image)}>
                  <ScanSearch /> {openImg === f.image ? "Hide X-RAY" : "X-RAY of the pre-recovery image"}
                </Button>
              )}
              {runId && openImg === f.image && <div className="mt-2"><ImageXRay runId={runId} image={f.image} /></div>}
            </div>
          ))}
        </div>
      )}

      {controls.length > 0 && (
        <div>
          <div className="mb-1 text-[11px] uppercase tracking-wider text-faint">Negative controls (outside the model: the harness must catch them)</div>
          <div className="overflow-x-auto">
          <table className="w-full min-w-[560px] text-xs">
            <tbody>
              {controls.map((c, i) => (
                <tr key={c.name + i} className="border-t border-border align-top">
                  <td className="py-1.5 pr-3 font-mono text-foreground">{c.name}</td>
                  <td className="py-1.5 pr-3 font-mono text-faint">point {c.crash_point}</td>
                  <td className="py-1.5 pr-3 text-muted-foreground">violates {c.violates}</td>
                  <td className="py-1.5 pr-3">{c.detected ? <StatusPill tone="ok">detected</StatusPill> : <StatusPill tone="bad">missed</StatusPill>}</td>
                  <td className="py-1.5 text-muted-foreground">
                    {c.how}
                    {c.xray && <div className="font-mono text-[11px] text-faint">x-ray: {c.xray.health} · {(c.xray.findings ?? []).slice(0, 2).join(" · ")}</div>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          </div>
        </div>
      )}
    </div>
  );
}

function LivePowercutRun({ id }: { id: string }) {
  const { api } = useNexus();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: [api.mode, "run", id], queryFn: () => api.runStatus(id), refetchInterval: (query) => (query.state.data?.run.state === "running" ? 1_500 : false) });
  const cancel = useMutation({ mutationFn: () => api.cancelRun(id), onSuccess: () => qc.invalidateQueries({ queryKey: [api.mode, "run", id] }), onError: (e) => toastError("Cancel", e) });
  if (q.isPending) return <LoadingBlock rows={4} />;
  if (q.isError) return <ErrorState error={q.error} onRetry={() => q.refetch()} />;
  const { run, progress, summary } = q.data;
  const parsed = summary ? PowercutReport.safeParse(summary) : null;
  const done = progress?.crash_points_done ?? 0;
  const total = progress?.crash_points_total ?? 0;
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3 text-xs">
        <StatusPill tone={run.state === "running" ? "running" : run.state === "passed" ? "ok" : "bad"}>{run.state}</StatusPill>
        <span className="font-mono text-muted-foreground">{run.id}</span>
        <span className="text-faint">{formatDuration(q.data.elapsed_seconds)}</span>
        <KindBadge kind="powercut" />
        {run.state === "running" && (
          <Button size="xs" variant="outline" disabled={cancel.isPending} onClick={() => cancel.mutate()}>
            <Square /> Cancel
          </Button>
        )}
      </div>
      {run.error && <ErrorState error={new Error(run.error)} compact />}
      {run.state === "running" && (
        <div className="space-y-1">
          <div className="h-1.5 overflow-hidden rounded-full bg-elevated">
            <div className="h-full bg-foreground/70 transition-[width]" style={{ width: total ? `${(done / total) * 100}%` : "0%" }} />
          </div>
          <div className="font-mono text-[11px] text-faint">
            {total ? `${done} / ${total} crash points validated` : "recording the workload trace…"}
            {progress?.failed ? ` · ${progress.failed} failed` : ""}
          </div>
        </div>
      )}
      {parsed?.success ? <PowercutResult report={parsed.data} runId={run.id} /> : summary && run.state !== "running" ? <ErrorState error={new Error("report.json did not match the expected schema")} compact /> : null}
      {!summary && run.state !== "running" && q.data.log_tail?.length ? (
        <pre className="max-h-48 overflow-auto rounded-md border border-border bg-background p-2 font-mono text-[11px] text-muted-foreground">{q.data.log_tail.join("\n")}</pre>
      ) : null}
    </div>
  );
}

export default function ReliabilityPage() {
  const { api, mode } = useNexus();
  const qc = useQueryClient();
  const health = useHealth();
  const { ready, reason } = useCanAct();
  const live = mode === "live";
  const info = useQuery({ queryKey: [api.mode, "powercut"], queryFn: api.powercutInfo, enabled: live });
  const [cfg, setCfg] = useState<Record<keyof Omit<PowercutConfig, "no_controls">, string> | null>(null);
  const [controls, setControls] = useState(true);
  const [selected, setSelected] = useState<string | null>(null);
  const d = info.data?.defaults;
  const form = cfg ?? (d ? { seed: String(d.seed), writers: String(d.writers), ops_per_writer: String(d.ops_per_writer), max_crash_points: String(d.max_crash_points), max_variants_per_point: String(d.max_variants_per_point) } : null);
  const num = (k: keyof NonNullable<typeof form>) => Number(form?.[k]);
  const invalid =
    !form ||
    !Number.isInteger(num("seed")) || num("seed") < 0 || num("seed") > 999999999999 ||
    !Number.isInteger(num("writers")) || num("writers") < 1 || num("writers") > 8 ||
    !Number.isInteger(num("ops_per_writer")) || num("ops_per_writer") < 1 || num("ops_per_writer") > 500 ||
    !Number.isInteger(num("max_crash_points")) || num("max_crash_points") < 1 || num("max_crash_points") > 2000 ||
    !Number.isInteger(num("max_variants_per_point")) || num("max_variants_per_point") < 1 || num("max_variants_per_point") > 16;
  const busy = !!health.data?.torture_running;
  const start = useMutation({
    mutationFn: () =>
      api.startPowercut({ seed: num("seed"), writers: num("writers"), ops_per_writer: num("ops_per_writer"), max_crash_points: num("max_crash_points"), max_variants_per_point: num("max_variants_per_point"), no_controls: !controls }),
    onSuccess: (st) => {
      toast.success("Power-Cut Explorer started", { description: st.run.args.join(" ") });
      setSelected(st.run.id);
      qc.invalidateQueries({ queryKey: [api.mode, "powercut"] });
      qc.invalidateQueries({ queryKey: [api.mode, "health"] });
    },
    onError: (e) => toastError("Start Power-Cut Explorer", e),
  });

  if (!live) {
    return (
      <div className="space-y-6">
        <PageHeader title="Reliability Lab" description="Power-Cut Explorer: modeled power-loss states validated with the real recovery." />
        <EmptyState icon={Zap} title="The Reliability Lab needs the Live API">Demo mode does not simulate power-loss testing. Switch the data source to Live API.</EmptyState>
      </div>
    );
  }
  if (info.isPending || info.isError) {
    return (
      <div className="space-y-6">
        <PageHeader title="Reliability Lab" description="Power-Cut Explorer: modeled power-loss states validated with the real recovery." />
        {info.isPending ? <LoadingBlock rows={6} /> : <ErrorState error={info.error} onRetry={() => info.refetch()} />}
      </div>
    );
  }
  const i = info.data;
  const current = selected ?? i.runs[0]?.id ?? null;
  const set = (k: keyof NonNullable<typeof form>) => (e: React.ChangeEvent<HTMLInputElement>) => setCfg({ ...form!, [k]: e.target.value });

  return (
    <div className="space-y-6">
      <PageHeader
        title="Reliability Lab"
        description="Power-Cut Explorer records every write and fsync the real engine issues during a concurrent workload, cuts the power at many points of that trace, builds the disk images a storage device could plausibly leave under a stated failure model, and opens each one with the real NEXUS recovery."
        actions={i.available ? <StatusPill tone="ok">nexus-powercut ready</StatusPill> : <StatusPill tone="bad">nexus-powercut not found</StatusPill>}
      />
      <Disclaimer text={i.disclaimer} />

      <div className="grid gap-4 lg:grid-cols-2">
        <Panel title="Storage failure model" description={`What a generated image may contain. Sector size ${i.sector_size} bytes.`}>
          <ul className="space-y-1.5 text-xs text-foreground">{i.model.map((m) => <li key={m}>{m}</li>)}</ul>
        </Panel>
        <Panel title="Not modeled (coverage limits)" description="Failures these images cannot represent; other tools cover some of them.">
          <ul className="space-y-1.5 text-xs text-muted-foreground">{i.not_modeled.map((m) => <li key={m}>{m}</li>)}</ul>
        </Panel>
      </div>

      {i.recorded && (
        <Panel
          title={<span className="flex items-center gap-2">Recorded run <RecordedBadge when={i.recorded.modified} /></span>}
          description={`${i.recorded.source} · recorded ${formatDateTime(i.recorded.report.started_at)} with the optimized engine. Historical; a run below is measured now.`}
        >
          <PowercutResult report={i.recorded.report} />
        </Panel>
      )}

      <Panel title="Run the explorer now" description="Runs nexus-powercut in the adapter's work directory with a fresh scratch database; your databases are not used. Only one torture, benchmark or power-cut run executes at a time. Validated arguments only.">
        {form && (
          <div className="flex flex-wrap items-end gap-3">
            {([
              ["seed", "Seed", i.limits.seed],
              ["writers", "Writers", i.limits.writers],
              ["ops_per_writer", "Ops / writer", i.limits.ops_per_writer],
              ["max_crash_points", "Max crash points", i.limits.max_crash_points],
              ["max_variants_per_point", "Variants / point", i.limits.max_variants_per_point],
            ] as const).map(([k, l, lim]) => (
              <div key={k} className="space-y-1">
                <Label htmlFor={`pc-${k}`} className="text-xs text-muted-foreground">{l} <span className="text-faint">({lim})</span></Label>
                <Input id={`pc-${k}`} className="h-8 w-32 font-mono text-xs" inputMode="numeric" value={form[k]} onChange={set(k)} />
              </div>
            ))}
            <label className="flex h-8 items-center gap-2 text-xs text-muted-foreground">
              <input type="checkbox" checked={controls} onChange={(e) => setControls(e.target.checked)} /> negative controls
            </label>
            <Button
              disabled={!i.available || !ready || busy || invalid || start.isPending}
              title={!i.available ? "nexus-powercut executable not found" : !ready ? reason : busy ? "Another run is in progress" : invalid ? "Check the inputs" : undefined}
              onClick={() => start.mutate()}
            >
              {start.isPending ? <Loader2 className="animate-spin" /> : <Play />} Run Power-Cut Explorer
            </Button>
          </div>
        )}
        {invalid && <p className="mt-2 text-xs text-bad">Values out of range.</p>}
      </Panel>

      {i.runs.length > 0 && current && (
        <Panel
          title="Live run"
          description="Measured now by the run selected here."
          actions={
            i.runs.length > 1 ? (
              <select aria-label="Run" className="h-7 rounded-md border border-input bg-background px-2 font-mono text-xs" value={current} onChange={(e) => setSelected(e.target.value)}>
                {i.runs.map((r) => <option key={r.id} value={r.id}>{r.id} · {r.state}</option>)}
              </select>
            ) : undefined
          }
        >
          <LivePowercutRun key={current} id={current} />
        </Panel>
      )}

      <p className="text-[11px] text-faint">
        Related: process-kill crash testing in the <Link className="underline underline-offset-2" href="/torture">Torture Lab</Link> · image diagnostics in{" "}
        <Link className="underline underline-offset-2" href="/xray">X-RAY</Link> · method in docs/POWER_CUT_EXPLORER.md.
      </p>
    </div>
  );
}
