"use client";

// Guided Demo: ten steps for presenting NEXUS. Every widget here talks to the
// real backend through the same adapter as the other pages; nothing is
// simulated. Steps that need the Live API say so when it is not connected.
import Link from "next/link";
import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ArrowRight, ChevronLeft, ChevronRight, Loader2, Play, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useNexus } from "@/components/providers";
import { useCanAct, useHealth, useOverview } from "@/lib/queries";
import { useVerify } from "@/lib/actions";
import { toastError } from "@/lib/mutations";
import { ConfirmDialog, EmptyState, ErrorState, KV, LoadingBlock, Panel, RecordedBadge, StatCard, StatusPill } from "@/components/nexus";
import { KeyOpsPanel } from "@/components/key-ops";
import { WritePath } from "@/components/write-path";
import { LogLegend, LogStrip, StopBanner } from "@/components/log-view";
import { CrashDrill, DemoFileMap, RecoveryProcess } from "@/components/recovery-view";
import { RecordedSummary, SummaryView, parseResult } from "@/components/torture-extra";
import { Headline, LiveRun, RECORDED_BENCH } from "@/components/bench-view";
import { BenchReport } from "@/lib/api/schemas";
import { formatBytes, formatDuration, formatMs, formatNumber } from "@/lib/format";
import { cn } from "@/lib/utils";

function LiveOnly({ children }: { children: React.ReactNode }) {
  const { mode } = useNexus();
  const { ready, reason } = useCanAct();
  if (mode !== "live") return <EmptyState title="This step needs the Live API">Switch the data source in the sidebar to Live API. Nothing here is simulated.</EmptyState>;
  if (!ready) return <EmptyState title="Backend not reachable">{reason ?? "Start nexus-api and this step becomes interactive."}</EmptyState>;
  return <>{children}</>;
}

function LiveLog({ limit = 40 }: { limit?: number }) {
  const { api } = useNexus();
  const q = useQuery({ queryKey: [api.mode, "frames", limit], queryFn: () => api.frames(limit) });
  if (q.isPending) return <LoadingBlock rows={2} />;
  if (q.isError) return <ErrorState error={q.error} compact />;
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
        <span>{formatNumber(q.data.total_frames)} frames · {formatNumber(q.data.total_entries)} entries · {formatBytes(q.data.file_size)} · {formatNumber(q.data.index_keys)} live keys in the index</span>
        <LogLegend />
      </div>
      <LogStrip map={q.data} />
      <StopBanner map={q.data} />
    </div>
  );
}

function OverviewMini() {
  const q = useOverview();
  if (q.isPending) return <LoadingBlock rows={2} />;
  if (q.isError) return <ErrorState error={q.error} compact />;
  const d = q.data;
  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
      <StatCard label="Database" value={d.database ?? "—"} hint={`format version ${d.format_version ?? "?"}`} />
      <StatCard label="Live keys" value={formatNumber(d.stats.keys)} />
      <StatCard label="File size" value={formatBytes(d.stats.size)} />
      <StatCard label="Opened in" value={formatMs(d.last_open.duration_ms)} hint={`${formatNumber(d.last_open.frames)} frames replayed`} />
    </div>
  );
}

function PersistenceStep() {
  const { api } = useNexus();
  const rec = useQuery({ queryKey: [api.mode, "recovery"], queryFn: api.recovery });
  const verify = useVerify();
  return (
    <div className="space-y-3">
      {rec.isPending ? <LoadingBlock rows={3} /> : rec.isError ? <ErrorState error={rec.error} compact /> : <RecoveryProcess lo={rec.data.last_open} title="What happened when this file was opened" description="Every open, clean or after a crash, rebuilds the index by replaying the log." />}
      <div className="flex flex-wrap items-center gap-3">
        <Button size="sm" variant="outline" disabled={verify.isPending} onClick={() => verify.mutate()}>
          {verify.isPending ? <Loader2 className="animate-spin" /> : <ShieldCheck />} Verify every live entry on disk
        </Button>
        {verify.data && (
          <span className="flex items-center gap-2 text-xs">
            <StatusPill tone={verify.data.ok ? "ok" : "bad"}>{verify.data.ok ? "verified" : "failed"}</StatusPill>
            <span className="text-muted-foreground">{formatNumber(verify.data.keys)} keys re-read and checksum-checked in {formatMs(verify.data.elapsed_ms)}</span>
          </span>
        )}
        {verify.isError && <ErrorState error={verify.error} compact />}
      </div>
    </div>
  );
}

function RecoveryStep() {
  const { api } = useNexus();
  const q = useQuery({ queryKey: [api.mode, "recovery"], queryFn: api.recovery });
  if (q.isPending) return <LoadingBlock rows={4} />;
  if (q.isError) return <ErrorState error={q.error} compact />;
  const demo = q.data.demo;
  return (
    <div className="space-y-3">
      <CrashDrill demo={demo} />
      {demo.file.exists && <DemoFileMap version={`${demo.file.size}-${demo.file.modified ?? ""}-${demo.events?.[0]?.at ?? ""}`} />}
    </div>
  );
}

function TortureStep() {
  const { api } = useNexus();
  const qc = useQueryClient();
  const health = useHealth();
  const [id, setId] = useState<string | null>(null);
  const [confirm, setConfirm] = useState(false);
  const start = useMutation({
    mutationFn: () => api.startRun("default", "12345", 5),
    onSuccess: (st) => {
      setId(st.run.id);
      qc.invalidateQueries({ queryKey: [api.mode, "runs"] });
      toast.success("Torture run started", { description: st.run.id });
    },
    onError: (e) => toastError("Start run", e),
  });
  const st = useQuery({
    queryKey: [api.mode, "run", id],
    queryFn: () => api.runStatus(id!),
    enabled: !!id,
    refetchInterval: (query) => (query.state.data?.run.state === "running" ? 1_500 : false),
  });
  const missing = health.data ? !health.data.tools.nexus_torture : false;
  const busy = !!health.data?.torture_running && !id;
  const run = st.data?.run;
  const parsed = st.data ? parseResult("run", st.data.summary) : null;
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <Button size="sm" disabled={missing || busy || start.isPending || run?.state === "running"} title={missing ? "nexus-torture not found" : busy ? "Another run is in progress" : undefined} onClick={() => setConfirm(true)}>
          {start.isPending || run?.state === "running" ? <Loader2 className="animate-spin" /> : <Play />} Run 5 crash iterations
        </Button>
        {run && (
          <span className="flex items-center gap-2 text-xs text-muted-foreground">
            <StatusPill tone={run.state === "running" ? "running" : run.state === "passed" ? "ok" : run.state === "failed" ? "bad" : "warn"}>{run.state}</StatusPill>
            {st.data?.progress?.completed ?? 0} / {run.iterations} iterations · {formatDuration(st.data?.elapsed_seconds ?? 0)}
            {st.data?.progress?.last && <> · last: {formatNumber(st.data.progress.last.acked_writes)} acked writes, kill {st.data.progress.last.kill_point || `@${st.data.progress.last.kill_after_ms} ms`}</>}
          </span>
        )}
        <Link href="/torture" className="ml-auto flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">Full Torture Lab <ArrowRight className="size-3" /></Link>
      </div>
      {parsed?.kind === "run" ? <SummaryView s={parsed.data} /> : <RecordedSummary />}
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        destructive
        title="Run 5 destructive iterations?"
        confirmLabel="Start"
        description="nexus-torture run --mode default --seed 12345 --iterations 5. Each iteration force-kills a kvdb server mid-write on a throw-away database in nexus-api's work directory, restarts it and checks every acknowledged write. Your open database is not touched."
        onConfirm={() => start.mutate()}
      />
    </div>
  );
}

function BenchStep() {
  const { api, mode } = useNexus();
  const qc = useQueryClient();
  const health = useHealth();
  const [id, setId] = useState<string | null>(null);
  const rec = useQuery({ queryKey: [api.mode, "evidence-json", RECORDED_BENCH], queryFn: () => api.evidenceJson(RECORDED_BENCH, BenchReport), staleTime: Infinity, retry: false });
  const start = useMutation({
    mutationFn: () => api.startBench({ n: 2000, runs: 1, value_size: 100, batch_size: 1000 }),
    onSuccess: (st) => {
      setId(st.run.id);
      qc.invalidateQueries({ queryKey: [api.mode, "runs"] });
    },
    onError: (e) => toastError("Start benchmark", e),
  });
  const missing = health.data ? !health.data.tools.kvdb_bench : false;
  return (
    <div className="space-y-4">
      <div>
        <div className="mb-2 flex items-center gap-2 text-xs text-muted-foreground">Recorded: 100,000 keys × 3 runs, median <RecordedBadge /></div>
        {rec.isPending ? <LoadingBlock rows={2} /> : rec.isError ? <ErrorState error={rec.error} compact /> : <Headline r={rec.data} />}
      </div>
      {mode === "live" && (
        <div className="space-y-3">
          <div className="flex flex-wrap items-center gap-3">
            <Button size="sm" variant="outline" disabled={missing || start.isPending || !!health.data?.torture_running} title={missing ? "kvdb-bench not found" : undefined} onClick={() => start.mutate()}>
              {start.isPending ? <Loader2 className="animate-spin" /> : <Play />} Quick live run (2,000 keys)
            </Button>
            <span className="text-xs text-faint">Small n: a sanity check on this machine, not comparable to the recorded run.</span>
            <Link href="/benchmark" className="ml-auto flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">Benchmark page <ArrowRight className="size-3" /></Link>
          </div>
          {id && <LiveRun id={id} recorded={rec.data} />}
        </div>
      )}
    </div>
  );
}

function ComparisonStep() {
  const rows = [
    ["Bitcask", "Closest relative: in-memory hash index over append-only data files. NEXUS uses one file, framed batches with sequence numbers, and always syncs before ACK."],
    ["Redis", "In-memory first; disk persistence (RDB / AOF) is optional and its fsync policy is configurable."],
    ["LevelDB / RocksDB", "LSM trees: WAL + memtable + sorted files with background compaction; ordered keys and huge datasets. Per-write sync is off by default."],
  ];
  return (
    <div className="space-y-3">
      <KV rows={rows.map(([a, b]) => [a, b] as [string, string])} className="[&_dd]:font-sans [&_dd]:text-xs" />
      <p className="text-xs text-muted-foreground">NEXUS did not invent append-only logs or hash indexes, and does not claim to outperform or replace these systems. Its value is a small, fully specified engine whose crash behaviour is tested by an independent oracle, fault scenarios and mutation testing.</p>
      <Link href="/comparison" className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">Full comparison and design decisions <ArrowRight className="size-3" /></Link>
    </div>
  );
}

type DemoStep = { title: string; what: string; internal: string; why: string; widget: React.ReactNode };

const STEPS: DemoStep[] = [
  {
    title: "Introduction",
    what: "NEXUS is an embedded key-value database written in Go with only the standard library. This dashboard is connected to a real instance through nexus-api.",
    internal: "nexus-api has opened one data file with kvdb.Open, which ran recovery and rebuilt the in-memory index. The numbers below come from that open handle.",
    why: "Everything in this demo is the real engine. When something is not available it says so instead of showing made-up data.",
    widget: <LiveOnly><OverviewMini /></LiveOnly>,
  },
  {
    title: "Architecture",
    what: "Two structures: an append-only binary log on disk (authoritative) and an in-memory hash index pointing into it.",
    internal: "Every write goes through one commit path: encode a checksummed frame, append it, fsync, update the index. Recovery, compaction and checking tools all work on that one log.",
    why: "With one durable structure there is one thing to get right after a crash. The index can always be rebuilt from the log.",
    widget: (
      <div className="space-y-3">
        <div className="grid items-center gap-3 md:grid-cols-[1fr_auto_1.6fr]">
          <div className="rounded-sm border border-border bg-elevated p-3"><div className="text-sm">In-memory hash index</div><div className="font-mono text-[11px] text-muted-foreground">key → {"{offset, size}"}</div></div>
          <div className="text-center font-mono text-xs text-faint">points into →</div>
          <div className="rounded-sm border border-foreground/30 bg-elevated p-3"><div className="text-sm">Append-only log</div><div className="font-mono text-[11px] text-muted-foreground">superblock · frame · frame · …</div></div>
        </div>
        <Link href="/architecture" className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">Interactive architecture with animated flows <ArrowRight className="size-3" /></Link>
      </div>
    ),
  },
  {
    title: "Insert and retrieve",
    what: "Write a key with PUT, then read it back with GET. Try a GET for a key that does not exist to see NOTFOUND.",
    internal: "PUT appends a frame with one PUT entry and syncs it before answering. GET is one hash lookup plus one read of exactly that entry, whose checksum is verified.",
    why: "The acknowledgement means the data is on disk. A missing key is answered from the index without touching the disk.",
    widget: <LiveOnly><KeyOpsPanel initialOp="put" initialKey="demo:greeting" /></LiveOnly>,
  },
  {
    title: "Update and delete",
    what: "PUT the same key again (reported as an update), then DELETE it. Delete a key that does not exist to see that nothing is written.",
    internal: "An update appends a new entry and moves the index pointer; the old entry stays in the file as dead bytes. A delete appends a DEL entry and removes the key from the index. Deleting an absent key does no I/O.",
    why: "Nothing is ever overwritten in place, so a crash can never leave a half-updated value.",
    widget: <LiveOnly><KeyOpsPanel initialOp="delete" initialKey="demo:greeting" /></LiveOnly>,
  },
  {
    title: "Storage engine",
    what: "Send a PUT through the write path and watch the log grow by one frame.",
    internal: "The frame is 32 header bytes + 11 entry-header bytes + key + value, with CRC32C on the header, payload and entry. The ACK step lights up only after the backend confirms the fsync.",
    why: "Sync-before-ACK is the durability promise. Its cost is visible: each synced write waits for the disk.",
    widget: <LiveOnly><div className="space-y-4"><WritePath compact /><LiveLog /></div></LiveOnly>,
  },
  {
    title: "Persistence",
    what: "See what happened when this file was opened, and verify every live entry on disk.",
    internal: "On open, recovery scanned every frame from the start, checked every checksum and sequence number, and rebuilt the index. Verify re-reads each live entry and checks it again.",
    why: "Data survives restarts because the log is the database; memory is only an index.",
    widget: <LiveOnly><PersistenceStep /></LiveOnly>,
  },
  {
    title: "Crash recovery",
    what: "On a separate disposable database: start a writer, kill it mid-write, then recover and check every acknowledged write.",
    internal: "A killed write can leave a torn final frame. Recovery detects it by header/payload checksum or length, truncates only that tail, and refuses to open a file corrupted elsewhere.",
    why: "This is the core guarantee: every acknowledged write is still there after a crash, and nothing half-written is exposed.",
    widget: <LiveOnly><RecoveryStep /></LiveOnly>,
  },
  {
    title: "Torture testing",
    what: "Run a short crash-and-verify test, and see the recorded 500-iteration result.",
    internal: "nexus-torture runs kvdb serve under concurrent load, force-kills it at random points (including during compaction), restarts it and checks every acknowledged write and batch against an independent oracle.",
    why: "Claims about crash safety need evidence. The tester is itself checked with mutation testing: known bugs must be caught.",
    widget: <LiveOnly><TortureStep /></LiveOnly>,
  },
  {
    title: "Benchmark",
    what: "The recorded throughput, and an optional quick run on this machine.",
    internal: "kvdb-bench uses the library in-process. PUT and DELETE each wait for an fsync; GET is served from the index plus one read; a batch shares one fsync across many operations.",
    why: "The numbers show the cost of the durability choice honestly. They depend on the machine and disk.",
    widget: <BenchStep />,
  },
  {
    title: "Comparison",
    what: "How NEXUS relates to Bitcask, Redis, LevelDB and RocksDB.",
    internal: "Same building blocks as Bitcask (hash index + append-only log), with framed atomic batches, mandatory sync and explicit recovery rules.",
    why: "Being clear about what is borrowed and what is specific is part of an honest design.",
    widget: <ComparisonStep />,
  },
];

export default function DemoPage() {
  const [i, setI] = useState(0);
  const s = STEPS[i];
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable)) return;
      if (e.key === "ArrowRight") setI((v) => Math.min(STEPS.length - 1, v + 1));
      if (e.key === "ArrowLeft") setI((v) => Math.max(0, v - 1));
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <div className="text-[11px] uppercase tracking-wider text-faint">Guided demo · step {i + 1} of {STEPS.length}</div>
          <h1 className="mt-1 text-xl font-medium text-foreground">{s.title}</h1>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="ghost" disabled={i === 0} onClick={() => setI(i - 1)}><ChevronLeft /> Previous</Button>
          <Button size="sm" disabled={i === STEPS.length - 1} onClick={() => setI(i + 1)}>Next <ChevronRight /></Button>
        </div>
      </div>
      <nav className="flex flex-wrap gap-1" aria-label="Demo steps">
        {STEPS.map((st, k) => (
          <button key={st.title} type="button" onClick={() => setI(k)} className={cn("rounded-sm border px-2 py-1 text-[11px] transition-colors", k === i ? "border-foreground/60 bg-elevated text-foreground" : k < i ? "border-border text-muted-foreground" : "border-border text-faint hover:text-muted-foreground")}>
            {k + 1}. {st.title}
          </button>
        ))}
      </nav>
      <div key={i} className="animate-in fade-in slide-in-from-bottom-1 space-y-5 duration-300">
        <div className="grid gap-3 md:grid-cols-3">
          {([["What is happening", s.what], ["What the engine does internally", s.internal], ["Why it matters", s.why]] as const).map(([h, t]) => (
            <div key={h} className="rounded-sm border border-border bg-card p-3">
              <div className="text-[11px] uppercase tracking-wider text-faint">{h}</div>
              <p className="mt-1.5 text-sm leading-relaxed text-foreground">{t}</p>
            </div>
          ))}
        </div>
        <Panel title="Try it">{s.widget}</Panel>
      </div>
      <p className="text-[11px] text-faint">Use ← and → to move between steps.</p>
    </div>
  );
}
