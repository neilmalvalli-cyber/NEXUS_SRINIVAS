"use client";

// NEXUS X-RAY: structured storage diagnostics produced by src/internal/xray.
// Scans are read-only. Repair exists only for the crash-demo copy and the
// test lab, needs an explicit confirmation, backs files up first and is
// reported as repaired only after a fresh scan verifies it. The live
// database is never repaired from here.
import { useMemo, useState, useSyncExternalStore } from "react";
import Link from "next/link";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { FlaskConical, Hammer, Loader2, Play, RotateCcw, ScanSearch, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useNexus } from "@/components/providers";
import { useCanAct } from "@/lib/queries";
import { toastError } from "@/lib/mutations";
import { ConfirmDialog, EmptyState, ErrorState, KV, LoadingBlock, PageHeader, Panel, StatCard, StatusPill } from "@/components/nexus";
import { CountsRow, ExportLink, FindingCard, KindBadge, NoFindings, OffsetMap, ReportFacts, healthTone, recoveryLabel, recoveryTone } from "@/components/xray-view";
import { SEVERITIES, type Severity, type XFinding, type XScanResult } from "@/lib/api/xray-schemas";
import { formatDateTime, formatNumber, relativeTime } from "@/lib/format";

const SINCE: Record<string, number> = { all: 0, "1h": 3_600_000, "24h": 86_400_000, "7d": 7 * 86_400_000 };

function Select({ value, onChange, options, label, className }: { value: string; onChange: (v: string) => void; options: [string, string][]; label: string; className?: string }) {
  return (
    <select aria-label={label} className={"h-7 rounded-md border border-input bg-background px-2 font-mono text-xs " + (className ?? "")} value={value} onChange={(e) => onChange(e.target.value)}>
      {options.map(([v, l]) => (
        <option key={v} value={v}>{l}</option>
      ))}
    </select>
  );
}

const noSubscribe = () => () => {};

export default function XRayPage() {
  const { api, mode } = useNexus();
  const qc = useQueryClient();
  const { ready, reason } = useCanAct();
  const live = mode === "live";

  const targets = useQuery({ queryKey: [api.mode, "xray-targets"], queryFn: api.xrayTargets, enabled: live });
  const summary = useQuery({ queryKey: [api.mode, "xray-summary"], queryFn: api.xraySummary, enabled: live });
  const reports = useQuery({ queryKey: [api.mode, "xray-reports"], queryFn: () => api.xrayReports(), enabled: live });

  // Deep links: /xray?target=lab:torn-tail or /xray?report=x-…
  // (read with useSyncExternalStore so the server render and hydration agree)
  const search = useSyncExternalStore(noSubscribe, () => window.location.search, () => "");
  const params = useMemo(() => new URLSearchParams(search), [search]);
  const [targetSel, setTarget] = useState<string | null>(null);
  const [reportSel, setReportId] = useState<string | null>(null);
  const target = targetSel ?? (params.get("target") || "main");
  const reportId = reportSel ?? params.get("report");
  const [result, setResult] = useState<XScanResult | null>(null);

  const loaded = useQuery({ queryKey: [api.mode, "xray-report", reportId], queryFn: () => api.xrayReport(reportId!), enabled: live && !!reportId && result?.id !== reportId });
  const current = result && (!reportId || result.id === reportId) ? result : loaded.data ?? null;

  const refreshLists = () => {
    for (const k of ["xray-summary", "xray-reports", "xray-targets"]) qc.invalidateQueries({ queryKey: [api.mode, k] });
  };
  const scan = useMutation({
    mutationFn: () => api.xrayScan(target),
    onSuccess: (r) => {
      setResult(r);
      setReportId(r.id);
      refreshLists();
      toast.success(`X-RAY: ${r.report.health}`, { description: `${r.target} · ${r.report.findings.length} finding(s) · file ${r.report.modified ? "MODIFIED" : "unchanged"}` });
    },
    onError: (e) => toastError("X-RAY scan", e),
  });
  const repair = useMutation({
    mutationFn: () => api.xrayRepair(target),
    onSuccess: (r) => {
      setResult(r);
      setReportId(r.id);
      refreshLists();
      if (r.report.repair?.verified) toast.success("Repair verified", { description: `Backups: ${r.report.repair.backups?.length ?? 0} file(s)` });
      else toast.warning("Repair not performed or not verified", { description: r.report.repair?.open_error || r.report.repair?.verify_error || "X-RAY refused: see the findings" });
    },
    onError: (e) => toastError("X-RAY repair", e),
  });
  const labReset = useMutation({
    mutationFn: api.xrayLabReset,
    onSuccess: (l) => {
      refreshLists();
      toast.success("Test lab built", { description: `${l.cases?.length ?? 0} damaged databases created from the real engine` });
    },
    onError: (e) => toastError("Build lab", e),
  });
  const labScan = useMutation({
    mutationFn: api.xrayLabScanAll,
    onSuccess: () => refreshLists(),
    onError: (e) => toastError("Scan lab", e),
  });
  const [confirmRepair, setConfirmRepair] = useState(false);
  const [confirmLab, setConfirmLab] = useState(false);

  // filters
  const [sev, setSev] = useState<Set<Severity>>(new Set(SEVERITIES));
  const [cat, setCat] = useState("all");
  const [rec, setRec] = useState("all");
  const [comp, setComp] = useState("all");
  const [since, setSinceKey] = useState("all");
  const [cutoff, setCutoff] = useState(0);
  const setSince = (v: string) => {
    setSinceKey(v);
    setCutoff(SINCE[v] ? Date.now() - SINCE[v] : 0);
  };
  const [kind, setKind] = useState("all");

  const findings = useMemo(() => current?.report.findings ?? [], [current]);
  const facets = useMemo(() => {
    const u = (f: (x: XFinding) => string) => Array.from(new Set(findings.map(f))).sort();
    return { cats: u((f) => f.category), recs: u((f) => f.recovery_status), comps: u((f) => f.component) };
  }, [findings]);
  const shown = findings
    .filter((f) => sev.has(f.severity) && (cat === "all" || f.category === cat) && (rec === "all" || f.recovery_status === rec) && (comp === "all" || f.component === comp))
    .filter((f) => !cutoff || Date.parse(f.timestamp) >= cutoff)
    .sort((a, b) => SEVERITIES.indexOf(a.severity) - SEVERITIES.indexOf(b.severity) || (a.offset ?? -1) - (b.offset ?? -1));
  const history = (reports.data?.reports ?? []).filter((r) => (kind === "all" || r.kind === kind) && (!cutoff || Date.parse(r.started_at) >= cutoff));

  const tgt = targets.data?.targets.find((t) => t.id === target);
  const busy = scan.isPending || repair.isPending || labReset.isPending || labScan.isPending;
  const canRepair = !!tgt?.repair_allowed && !!current && current.target === target && current.report.recovery.status === "recoverable";

  if (!live) {
    return (
      <div className="space-y-6">
        <PageHeader title="X-RAY" description="Structured storage diagnostics for NEXUS databases." />
        <EmptyState icon={ScanSearch} title="X-RAY needs the Live API">
          Demo mode does not simulate diagnostics: every X-RAY finding comes from scanning real bytes. Switch the data source to Live API.
        </EmptyState>
      </div>
    );
  }

  const liveSum = summary.data?.live;
  const ops = summary.data?.operational_findings ?? [];

  return (
    <div className="space-y-6">
      <PageHeader
        title="X-RAY"
        description="Explains what is wrong with a database file, where, why, what the engine's recovery would do about it, and whether data is at risk. Scans are read-only: the file's size, modification time and SHA-256 are compared before and after every scan."
        actions={<StatusPill tone="neutral">read-only by default</StatusPill>}
      />

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatCard label="Live database" value={liveSum ? liveSum.health : "not scanned"} tone={liveSum ? healthTone(liveSum.health) : "unknown"} hint={liveSum ? `scanned ${relativeTime(liveSum.started_at)}` : "run a scan of “main”"} />
        <StatCard label="Live critical / error" value={liveSum ? liveSum.counts.CRITICAL + liveSum.counts.ERROR : "—"} tone={liveSum && liveSum.counts.CRITICAL + liveSum.counts.ERROR > 0 ? "bad" : liveSum ? "ok" : "unknown"} hint="from the latest live scan" />
        <StatCard label="Live warnings" value={liveSum ? liveSum.counts.WARNING : "—"} tone={liveSum?.counts.WARNING ? "warn" : liveSum ? "ok" : "unknown"} />
        <StatCard label="Operational events" value={summary.data ? ops.length : "—"} tone={!summary.data ? "unknown" : ops.length ? "warn" : "ok"} hint="errors returned to API callers" />
      </div>

      <Panel
        title="Run a scan"
        description="Targets are fixed IDs; the browser cannot name a file path. “main” is scanned while adapter writes are paused for the duration of the scan (a few milliseconds for small files)."
        actions={current && <ExportLink href={api.xrayExportUrl(current.id)} />}
      >
        <div className="flex flex-wrap items-center gap-2">
          {targets.isPending ? (
            <LoadingBlock rows={1} className="w-60" />
          ) : targets.isError ? (
            <ErrorState error={targets.error} compact onRetry={() => targets.refetch()} />
          ) : (
            <Select
              label="Target"
              value={target}
              onChange={setTarget}
              className="max-w-full"
              options={targets.data.targets.map((t) => [t.id, `${t.kind === "lab" ? "lab · " : ""}${t.label}`])}
            />
          )}
          <Button size="sm" disabled={!ready || busy} title={!ready ? reason : undefined} onClick={() => scan.mutate()}>
            {scan.isPending ? <Loader2 className="animate-spin" /> : <Play />} Run X-RAY
          </Button>
          {tgt?.repair_allowed && (
            <Button size="sm" variant="outline" disabled={!ready || busy || !canRepair} title={canRepair ? "Back up, open with the engine, verify, rescan" : "Scan first; repair is offered only when recovery would fix the file"} onClick={() => setConfirmRepair(true)}>
              {repair.isPending ? <Loader2 className="animate-spin" /> : <Hammer />} Repair…
            </Button>
          )}
          {tgt && <KindBadge kind={tgt.kind} />}
        </div>
        {tgt?.description && <p className="mt-2 text-xs text-muted-foreground">{tgt.description}</p>}
        {tgt?.note && <p className="mt-2 text-xs text-muted-foreground">{tgt.note}</p>}
        {tgt?.expect && (
          <p className="mt-1 font-mono text-[11px] text-faint">
            designed to produce: {tgt.expect.code} · {tgt.expect.severity} · {tgt.expect.recovery}
            {tgt.expect.rule ? ` · ${tgt.expect.rule}` : ""}
          </p>
        )}
      </Panel>

      {loaded.isError && <ErrorState error={loaded.error} />}
      {reportId && loaded.isPending && !current && <LoadingBlock rows={5} />}

      {current && (
        <Panel
          title={
            <span className="flex flex-wrap items-center gap-2">
              Report <span className="font-mono text-xs text-muted-foreground">{current.id}</span>
              <KindBadge kind={current.report.live ? "live" : current.target.startsWith("lab:") ? "lab" : current.target === "demo" ? "demo" : "powercut"} />
            </span>
          }
          description={`${current.target} · ${current.report.path} · ${formatDateTime(current.report.started_at)} · ${current.report.mode}`}
        >
          <div className="space-y-5">
            <div className="flex flex-wrap items-center gap-3">
              <StatusPill tone={healthTone(current.report.health)}>{current.report.health}</StatusPill>
              <StatusPill tone={recoveryTone[current.report.recovery.status]}>{recoveryLabel[current.report.recovery.status]}</StatusPill>
              <CountsRow counts={current.report.counts} />
              <span className={current.report.modified ? "text-xs text-bad" : "text-xs text-faint"}>
                {current.report.modified ? "file changed during this run" : "file unchanged by this scan"}
              </span>
            </div>
            <OffsetMap report={current.report} />
            <ReportFacts report={current.report} />
            {current.report.repair && (
              <div className="rounded-md border border-border p-3">
                <div className="mb-2 flex items-center gap-2 text-sm">
                  <ShieldCheck className="size-4" /> Repair {current.report.repair.verified ? <StatusPill tone="ok">verified</StatusPill> : <StatusPill tone="bad">not verified</StatusPill>}
                </div>
                <KV
                  className="text-xs"
                  rows={[
                    ["Backups", current.report.repair.backups?.join(", ") || "none (nothing was changed)"],
                    ["Planned", current.report.repair.planned?.join("; ") || "—"],
                    ["Applied by the engine", current.report.repair.applied?.join("; ") || "—"],
                    ...(current.report.repair.open_error ? ([["Open error", current.report.repair.open_error]] as [string, string][]) : []),
                    ...(current.report.repair.verify_error ? ([["Verify error", current.report.repair.verify_error]] as [string, string][]) : []),
                    ...(current.report.repair.after ? ([["Rescan after repair", `${current.report.repair.after.health} · ${current.report.repair.after.engine_outcome}`]] as [string, string][]) : []),
                  ]}
                />
              </div>
            )}

            <div className="space-y-3">
              <div className="flex flex-wrap items-center gap-2">
                {SEVERITIES.map((s) => (
                  <button
                    key={s}
                    type="button"
                    aria-pressed={sev.has(s)}
                    className={"h-7 rounded-md border px-2 font-mono text-[11px] " + (sev.has(s) ? "border-foreground/40 text-foreground" : "border-border text-faint")}
                    onClick={() => setSev((p) => { const n = new Set(p); if (n.has(s)) n.delete(s); else n.add(s); return n; })}
                  >
                    {s} {current.report.counts[s]}
                  </button>
                ))}
                <Select label="Category" value={cat} onChange={setCat} options={[["all", "all categories"], ...facets.cats.map((c) => [c, c] as [string, string])]} />
                <Select label="Recovery status" value={rec} onChange={setRec} options={[["all", "any recovery status"], ...facets.recs.map((c) => [c, c] as [string, string])]} />
                <Select label="Component" value={comp} onChange={setComp} options={[["all", "all components"], ...facets.comps.map((c) => [c, c] as [string, string])]} />
                <Select label="Date" value={since} onChange={setSince} options={[["all", "any time"], ["1h", "last hour"], ["24h", "last 24 h"], ["7d", "last 7 days"]]} />
              </div>
              {shown.length ? (
                <div className="space-y-2">
                  {shown.map((f, i) => <FindingCard key={f.id} f={f} live={current.report.live} defaultOpen={i === 0 && f.severity !== "INFO"} />)}
                </div>
              ) : (
                <NoFindings />
              )}
            </div>
          </div>
        </Panel>
      )}

      <Panel
        title="Scan timeline"
        description="Every scan this nexus-api process has run, newest first. Test-environment scans are labelled and never count toward live health."
        actions={<Select label="Kind" value={kind} onChange={setKind} options={[["all", "all kinds"], ["live", "live"], ["demo", "crash demo"], ["lab", "test lab"]]} />}
      >
        {reports.isPending ? (
          <LoadingBlock rows={3} />
        ) : reports.isError ? (
          <ErrorState error={reports.error} compact onRetry={() => reports.refetch()} />
        ) : history.length === 0 ? (
          <EmptyState icon={ScanSearch} title="No scans yet">Run a scan above.</EmptyState>
        ) : (
          <ol className="relative space-y-1 border-l border-border pl-4">
            {history.slice(0, 40).map((r) => (
              <li key={r.id} className="relative">
                <span className={"absolute -left-[21px] top-2.5 size-2 rounded-full " + (healthTone(r.health) === "ok" ? "bg-ok" : healthTone(r.health) === "warn" ? "bg-warn" : "bg-bad")} />
                <button
                  type="button"
                  className={"flex w-full flex-wrap items-center gap-x-3 gap-y-1 rounded-sm px-2 py-1.5 text-left text-xs hover:bg-elevated " + (current?.id === r.id ? "bg-elevated" : "")}
                  onClick={() => { setResult(null); setReportId(r.id); }}
                >
                  <span className="font-mono text-faint">{formatDateTime(r.started_at)}</span>
                  <KindBadge kind={r.kind} />
                  <span className="font-mono text-foreground">{r.target}</span>
                  <span className={healthTone(r.health) === "ok" ? "text-ok" : healthTone(r.health) === "warn" ? "text-warn" : "text-bad"}>{r.health}</span>
                  <span className="text-muted-foreground">{r.engine_outcome}</span>
                  <span className="font-mono text-faint">C{r.counts.CRITICAL} E{r.counts.ERROR} W{r.counts.WARNING}</span>
                  {r.mode === "repair" && <StatusPill tone="neutral">repair</StatusPill>}
                </button>
              </li>
            ))}
          </ol>
        )}
      </Panel>

      <Panel
        title={<span className="flex items-center gap-2"><FlaskConical className="size-4" /> Diagnostic test lab <KindBadge kind="lab" /></span>}
        description="Disposable databases created with the real kvdb API in the adapter's work directory, then damaged with exact byte edits (torn tail, bad checksums, interrupted compaction, bad superblock…). Never the main database."
        actions={
          <>
            <Button size="xs" variant="outline" disabled={!ready || busy} onClick={() => setConfirmLab(true)}>
              {labReset.isPending ? <Loader2 className="animate-spin" /> : <RotateCcw />} {targets.data?.lab_built ? "Rebuild lab" : "Build lab"}
            </Button>
            <Button size="xs" disabled={!ready || busy || !targets.data?.lab_built} onClick={() => labScan.mutate()}>
              {labScan.isPending ? <Loader2 className="animate-spin" /> : <ScanSearch />} Scan every case
            </Button>
          </>
        }
      >
        {!targets.data?.lab_built ? (
          <p className="text-sm text-muted-foreground">The lab is not built yet.</p>
        ) : labScan.data ? (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[640px] text-xs">
              <thead className="text-left text-faint">
                <tr><th className="py-1 pr-3 font-normal">Case</th><th className="pr-3 font-normal">Expected finding</th><th className="pr-3 font-normal">Health</th><th className="pr-3 font-normal">Engine</th><th className="pr-3 font-normal">Recovery</th><th className="font-normal">Diagnosed as designed</th></tr>
              </thead>
              <tbody>
                {labScan.data.results.map((r) => (
                  <tr key={r.target} className="border-t border-border">
                    <td className="py-1.5 pr-3">
                      <button type="button" className="font-mono text-foreground underline-offset-2 hover:underline" onClick={() => { setResult(null); setTarget(r.target); if (r.report) setReportId(r.report.id); }}>
                        {r.target.replace("lab:", "")}
                      </button>
                    </td>
                    <td className="pr-3 font-mono text-muted-foreground">{r.expected ?? "—"}</td>
                    <td className="pr-3">{r.report?.health ?? r.error}</td>
                    <td className="pr-3 text-muted-foreground">{r.report?.engine_outcome}</td>
                    <td className="pr-3">{r.report && recoveryLabel[r.report.recovery_status]}</td>
                    <td>{r.matched === undefined ? "—" : r.matched ? <StatusPill tone="ok">yes</StatusPill> : <StatusPill tone="bad">no</StatusPill>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <p className="mt-2 text-[11px] text-faint">
              {formatNumber(labScan.data.results.filter((r) => r.matched).length)} of {labScan.data.results.length} cases produced the finding they were built to produce. Select a case to open its report.
            </p>
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            Lab built {targets.data.lab_built_at ? relativeTime(targets.data.lab_built_at) : ""}. Pick a <span className="font-mono">lab ·</span> target above, or scan every case.
          </p>
        )}
      </Panel>

      {ops.length > 0 && (
        <Panel title="Operational events" description="Errors the adapter returned to callers (disk full, lock conflicts, unknown write outcomes), converted from typed engine errors.">
          <div className="space-y-2">{ops.slice(0, 10).map((f, i) => <FindingCard key={f.id + i} f={f} />)}</div>
        </Panel>
      )}

      <p className="text-[11px] text-faint">
        How X-RAY decides: <Link className="underline underline-offset-2" href="/architecture">Architecture</Link> · rules C0–C8 and A1–A7 on <Link className="underline underline-offset-2" href="/recovery">Recovery</Link> · modeled power-loss images on{" "}
        <Link className="underline underline-offset-2" href="/reliability">Reliability Lab</Link>.
      </p>

      <ConfirmDialog
        open={confirmRepair}
        onOpenChange={setConfirmRepair}
        title={`Repair ${target}?`}
        destructive
        confirmLabel="Back up and repair"
        description={
          <div className="space-y-2 text-sm">
            <p>This modifies test files. X-RAY will:</p>
            <ol className="list-decimal space-y-1 pl-5 text-xs">
              <li>copy every database file of this target to a backup directory (refused if a copy cannot be made);</li>
              <li>open it with the real engine, which applies the recovery listed in the report;</li>
              <li>run Verify and a fresh read-only scan; it is reported as repaired only if both pass.</li>
            </ol>
            <p className="text-xs text-muted-foreground">Corruption the engine refuses is never “repaired”. The live database is never offered here.</p>
          </div>
        }
        onConfirm={() => repair.mutate()}
      />
      <ConfirmDialog
        open={confirmLab}
        onOpenChange={setConfirmLab}
        title={targets.data?.lab_built ? "Rebuild the test lab?" : "Build the test lab?"}
        confirmLabel={targets.data?.lab_built ? "Rebuild" : "Build"}
        description="Deletes and recreates the disposable lab databases in the adapter's work directory (xray/lab). Your databases are not touched."
        onConfirm={() => labReset.mutate()}
      />
    </div>
  );
}
