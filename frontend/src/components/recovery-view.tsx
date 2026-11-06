"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Loader2, RotateCcw, ScanSearch, Scissors, ShieldCheck, Zap } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useNexus } from "@/components/providers";
import { useCanAct } from "@/lib/queries";
import { useVerify } from "@/lib/actions";
import { toastError } from "@/lib/mutations";
import { ConfirmDialog, ErrorState, KV, LoadingBlock, Panel, StatusPill, type Tone } from "@/components/nexus";
import type { DemoState, Recovery, VerifyResult } from "@/lib/api/schemas";
import { formatBytes, formatDateTime, formatMs, formatNumber, formatTime } from "@/lib/format";
import { cn } from "@/lib/utils";
import { RECOVERY_STEPS } from "@/lib/format-spec";
import { LogLegend, LogStrip, StopBanner } from "@/components/log-view";
import type { LastOpen } from "@/lib/api/schemas";

/** The eight recovery steps of kvdb.Open, annotated with what the last open actually reported. */
export function RecoveryProcess({ lo, title, description }: { lo: LastOpen; title: string; description: string }) {
  const facts: (string | null)[] = [
    lo.rules?.length ? `authority rules applied: ${lo.rules.join("; ")}` : "no leftover .new / .compact / .old files to resolve",
    lo.created ? "new file: superblock written, nothing to read" : "superblock valid (open would have failed otherwise)",
    lo.created ? null : `${formatNumber(lo.frames)} frames scanned`,
    lo.created ? null : "every scanned frame passed magic, header CRC and bounds",
    lo.created ? null : "payload and entry CRCs passed; sequence numbers contiguous",
    lo.created ? null : "entries of each valid frame applied in order",
    "index rebuilt",
    lo.tail_rule
      ? `torn tail: ${formatBytes(lo.discarded_bytes)} discarded under rule ${lo.tail_rule}`
      : lo.created
        ? null
        : "file ended on a frame boundary: nothing truncated",
  ];
  return (
    <Panel title={title} description={description}>
      <ol className="grid gap-2 md:grid-cols-2 xl:grid-cols-4">
        {RECOVERY_STEPS.map((s, i) => (
          <li key={s.title} className="rounded-sm border border-border bg-background px-3 py-2">
            <div className="flex items-center gap-2 text-xs text-foreground">
              <span className="grid size-5 shrink-0 place-items-center rounded-full border border-border font-mono text-[10px] text-muted-foreground">{i + 1}</span>
              {s.title}
            </div>
            <p className="mt-1 text-[11px] leading-relaxed text-faint">{s.detail}</p>
            {facts[i] && <p className={cn("mt-1.5 font-mono text-[11px]", i === 7 && lo.tail_rule ? "text-warn" : "text-muted-foreground")}>last open: {facts[i]}</p>}
          </li>
        ))}
      </ol>
      <p className="mt-3 text-xs text-faint">
        Not every damage is recoverable, by design: only a damaged final frame (the shape a killed write leaves) is truncated. Corruption in the middle of the log, or a frame whose checksums pass but whose contents are invalid, makes open fail without touching the file.
      </p>
    </Panel>
  );
}

export function DemoFileMap({ version }: { version: string }) {
  const { api } = useNexus();
  const q = useQuery({ queryKey: [api.mode, "demo-frames", version], queryFn: () => api.demoFrames(80), retry: false });
  return (
    <div className="space-y-2 rounded-sm border border-border bg-background p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="text-[11px] uppercase tracking-wider text-faint">Demo file, as it is on disk now</div>
        <LogLegend />
      </div>
      {q.isPending ? (
        <LoadingBlock rows={2} />
      ) : q.isError ? (
        <ErrorState error={q.error} compact />
      ) : (
        <>
          <LogStrip map={q.data} />
          <StopBanner map={q.data} />
          <div className="grid gap-2 text-[11px] text-muted-foreground sm:grid-cols-2 lg:grid-cols-5">
            <span><span className="text-foreground">Valid records:</span> {formatNumber(q.data.total_frames)} frames, {formatNumber(q.data.total_entries)} entries</span>
            <span><span className="text-foreground">Incomplete write:</span> {q.data.stop.kind === "damaged" ? `${formatNumber(q.data.stop.bytes_after)} B at the tail` : "none"}</span>
            <span><span className="text-foreground">Corrupted:</span> {["corrupt", "sequence"].includes(q.data.stop.kind) ? `at offset ${formatNumber(q.data.stop.offset)}` : "none found"}</span>
            <span><span className="text-foreground">Live keys:</span> {formatNumber(q.data.index_keys)}</span>
            <span><span className="text-foreground">Valid end:</span> {formatNumber(q.data.valid_end)} of {formatNumber(q.data.file_size)} B</span>
          </div>
        </>
      )}
    </div>
  );
}

function verdict(ok: boolean | undefined | null): { tone: Tone; text: string } {
  if (ok === true) return { tone: "ok", text: "pass" };
  if (ok === false) return { tone: "bad", text: "fail" };
  return { tone: "unknown", text: "unknown" };
}

export function MainDatabase({ data }: { data: Recovery }) {
  const { ready, reason } = useCanAct();
  const verify = useVerify();
  const lo = data.last_open;
  const history = data.verify_history ?? [];
  const latest: VerifyResult | undefined = verify.data ?? history[0];
  const v = verdict(verify.isPending ? null : latest?.ok);
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Panel title="Recovery at open" description="kvdb.Open scans the log when nexus-api starts: it checks every frame and discards a torn tail left by a crash.">
        <KV
          rows={[
            ["Opened", formatDateTime(lo.at)],
            ["Duration", formatMs(lo.duration_ms)],
            ["New file", lo.created ? "yes (created at open)" : "no"],
            ["Frames replayed", formatNumber(lo.frames)],
            ["Tail rule", lo.tail_rule || "none (file ended on a frame boundary)"],
            ["Discarded bytes", formatBytes(lo.discarded_bytes)],
            ["Authority rules", lo.rules?.length ? lo.rules.join("; ") : "none"],
            ["Warnings", lo.warnings?.length ? lo.warnings.join("; ") : "none"],
          ]}
        />
      </Panel>
      <Panel
        title="Inspection"
        description="kvdb.Verify re-reads the whole open file and checks the superblock, every CRC, sequence numbers, the frame boundary at the end, and that the index matches the file."
        actions={
          <Button size="sm" variant="outline" disabled={!ready || verify.isPending} title={!ready ? reason : undefined} onClick={() => verify.mutate()}>
            {verify.isPending ? <Loader2 className="animate-spin" /> : <ScanSearch />} Inspect now
          </Button>
        }
      >
        <div className="mb-3 flex items-center gap-3">
          <StatusPill tone={verify.isPending ? "running" : v.tone} className="h-6 px-2 text-xs">
            {verify.isPending ? "checking" : latest ? v.text : "not run"}
          </StatusPill>
          <span className="text-xs text-muted-foreground">
            {verify.isPending ? "Reading the file…" : latest ? `${formatDateTime(latest.at)} · ${formatMs(latest.elapsed_ms)}` : "No inspection has run since nexus-api started. Status is unknown, not passing."}
          </span>
        </div>
        {verify.isError && <ErrorState error={verify.error} compact />}
        {latest && !verify.isPending && (
          <KV
            rows={[
              ["Frames", formatNumber(latest.frames)],
              ["Keys", formatNumber(latest.keys)],
              ["File size", formatBytes(latest.size)],
              ["Live bytes", formatBytes(latest.live_bytes)],
              ...(latest.error ? [["Error", <span key="e" className="text-bad">{latest.error.class}: {latest.error.message}</span>] as [string, React.ReactNode]] : []),
            ]}
          />
        )}
        <p className="mt-3 text-xs text-faint">{data.read_only_check}</p>
      </Panel>
      {history.length > 0 && (
        <Panel title="Inspection history" description="Inspections requested through this adapter, newest first." className="lg:col-span-2" bodyClassName="p-0">
          <Table>
            <TableHeader>
              <TableRow className="border-border hover:bg-transparent">
                <TableHead className="h-8 pl-4 text-xs text-faint">When</TableHead>
                <TableHead className="h-8 text-xs text-faint">Result</TableHead>
                <TableHead className="h-8 text-right text-xs text-faint">Frames</TableHead>
                <TableHead className="h-8 text-right text-xs text-faint">Keys</TableHead>
                <TableHead className="h-8 text-right text-xs text-faint">Size</TableHead>
                <TableHead className="h-8 pr-4 text-right text-xs text-faint">Took</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {history.map((h) => (
                <TableRow key={h.at} className="border-border hover:bg-hover/50">
                  <TableCell className="py-1.5 pl-4 font-mono text-xs text-muted-foreground">{formatDateTime(h.at)}</TableCell>
                  <TableCell className="py-1.5"><StatusPill tone={h.ok ? "ok" : "bad"}>{h.ok ? "pass" : "fail"}</StatusPill></TableCell>
                  <TableCell className="py-1.5 text-right font-mono text-xs">{formatNumber(h.frames)}</TableCell>
                  <TableCell className="py-1.5 text-right font-mono text-xs">{formatNumber(h.keys)}</TableCell>
                  <TableCell className="py-1.5 text-right font-mono text-xs">{formatBytes(h.size)}</TableCell>
                  <TableCell className="py-1.5 pr-4 text-right font-mono text-xs">{formatMs(h.elapsed_ms)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Panel>
      )}
    </div>
  );
}

function NumField({ id, label, value, onChange, min, max, hint }: { id: string; label: string; value: string; onChange: (v: string) => void; min: number; max: number; hint: string }) {
  const n = Number(value);
  const bad = value === "" || !Number.isInteger(n) || n < min || n > max;
  return (
    <div className="space-y-1">
      <Label htmlFor={id} className="text-xs text-muted-foreground">{label}</Label>
      <Input id={id} inputMode="numeric" value={value} onChange={(e) => onChange(e.target.value.replace(/\D/g, ""))} className={cn("h-7 w-28 font-mono text-xs", bad && "border-bad/60")} aria-invalid={bad} />
      <div className={cn("text-[11px]", bad ? "text-bad" : "text-faint")}>{bad ? `${min}–${max}` : hint}</div>
    </div>
  );
}

function Step({ n, title, children, state }: { n: number; title: string; children: React.ReactNode; state?: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[1.5rem_1fr] gap-3 border-b border-border py-4 last:border-b-0">
      <div className="grid size-6 place-items-center rounded-sm border border-border bg-elevated font-mono text-xs text-muted-foreground">{n}</div>
      <div className="min-w-0 space-y-3">
        <div className="flex flex-wrap items-center gap-2">
          <h3 className="text-sm text-foreground">{title}</h3>
          {state}
        </div>
        {children}
      </div>
    </div>
  );
}

export function CrashDrill({ demo }: { demo: DemoState }) {
  const { api } = useNexus();
  const { ready, reason } = useCanAct();
  const qc = useQueryClient();
  const [keys, setKeys] = useState("1000");
  const [writes, setWrites] = useState("5000");
  const [killMs, setKillMs] = useState("300");
  const [tearBytes, setTearBytes] = useState("7");
  const [confirmTear, setConfirmTear] = useState(false);
  const hasKvdb = !!demo.kvdb;
  const exists = demo.file.exists;

  const act = useMutation({
    mutationFn: (a: { kind: "reset" | "crash" | "inspect" | "tear" | "recover" }) => {
      switch (a.kind) {
        case "reset": return api.demoReset(Number(keys));
        case "crash": return api.demoCrash(Number(writes), Number(killMs));
        case "inspect": return api.demoInspect();
        case "tear": return api.demoTear(Number(tearBytes));
        case "recover": return api.demoRecover();
      }
    },
    onSuccess: (state, a) => {
      qc.setQueryData([api.mode, "recovery"], (old: Recovery | undefined) => (old ? { ...old, demo: state } : old));
      const ev = state.events?.[0];
      if (ev) (ev.ok ? toast.success : toast.error)(`${a.kind}: ${ev.ok ? "done" : "failed"}`, { description: ev.summary });
    },
    onError: (e, a) => toastError(a.kind, e),
    onSettled: () => qc.invalidateQueries({ queryKey: [api.mode, "recovery"] }),
  });
  const running = act.isPending ? act.variables?.kind : undefined;
  const dis = !ready || act.isPending;
  const c = demo.last_crash;
  const ins = demo.last_inspect;
  const rec = demo.last_recovery;
  const valid = (v: string, lo: number, hi: number) => v !== "" && Number(v) >= lo && Number(v) <= hi;

  const btn = (kind: "reset" | "crash" | "inspect" | "tear" | "recover", label: string, Icon: React.ComponentType<{ className?: string }>, extraDisabled = false, onClick?: () => void) => (
    <Button size="sm" variant="outline" disabled={dis || extraDisabled} title={!ready ? reason : undefined} onClick={onClick ?? (() => act.mutate({ kind }))}>
      {running === kind ? <Loader2 className="animate-spin" /> : <Icon />} {label}
    </Button>
  );

  return (
    <Panel
      title="Crash drill"
      description={
        <>
          Runs against a separate demo database (<span className="font-mono">{demo.db_path}</span>), never the main one. The crash is real: nexus-api starts a <span className="font-mono">kvdb serve</span> process, streams writes, and kills it with no warning.
        </>
      }
    >
      {!hasKvdb && (
        <div className="mb-3">
          <ErrorState error={new Error("BLOCKED: the kvdb executable was not found, so the crash step cannot run. Build it with `go build -o . ./src/cmd/kvdb` next to nexus-api, or pass --kvdb.")} compact />
        </div>
      )}
      <Step n={1} title="Create a fresh demo database" state={<StatusPill tone={exists ? "ok" : "unknown"}>{exists ? `present · ${formatBytes(demo.file.size)}` : "absent"}</StatusPill>}>
        <div className="flex flex-wrap items-end gap-3">
          <NumField id="keys" label="Base keys" value={keys} onChange={setKeys} min={0} max={10000} hint="written in one batch" />
          {btn("reset", "Reset", RotateCcw, !valid(keys, 0, 10000))}
        </div>
      </Step>

      <Step
        n={2}
        title="Crash a live writer"
        state={c ? <StatusPill tone="neutral">{formatNumber(c.acked)} acked · killed at {formatMs(c.killed_at_ms)}</StatusPill> : undefined}
      >
        <div className="flex flex-wrap items-end gap-3">
          <NumField id="writes" label="Writes to send" value={writes} onChange={setWrites} min={1} max={20000} hint="each one synced before ACK" />
          <NumField id="kill" label="Kill after (ms)" value={killMs} onChange={setKillMs} min={1} max={10000} hint="taskkill / SIGKILL" />
          {btn("crash", "Run & kill", Zap, !hasKvdb || !exists || !valid(writes, 1, 20000) || !valid(killMs, 1, 10000))}
        </div>
        {c && (
          <KV
            rows={[
              ["When", formatDateTime(c.at)],
              ["Sent / acked", `${formatNumber(c.sent)} / ${formatNumber(c.acked)}`],
              ["Rejected", formatNumber(c.rejected)],
              ["In flight at kill", `${formatNumber(c.unknown)} (outcome unknown: may or may not be durable)`],
              ["Killed", c.exited_before_kill ? "process had already exited" : `at ${formatMs(c.killed_at_ms)}`],
              ...(c.torn_by_hand ? [["Tail torn by hand", "yes"] as [string, React.ReactNode]] : []),
            ]}
          />
        )}
      </Step>

      <Step n={3} title="Optional: tear the tail" state={c?.torn_by_hand ? <StatusPill tone="warn">torn</StatusPill> : undefined}>
        <p className="text-xs text-muted-foreground">Cuts bytes off the end of the file, like a write that was half on disk at power loss. This can remove acknowledged frames on purpose, so missing writes afterwards are expected.</p>
        <div className="flex flex-wrap items-end gap-3">
          <NumField id="tear" label="Bytes to cut" value={tearBytes} onChange={setTearBytes} min={1} max={4096} hint="truncate the file" />
          {btn("tear", "Tear tail", Scissors, !exists || !valid(tearBytes, 1, 4096), () => setConfirmTear(true))}
        </div>
      </Step>

      <Step
        n={4}
        title="Read-only inspection"
        state={ins ? <StatusPill tone={ins.error ? "bad" : ins.needs_repair ? "warn" : "ok"}>{ins.error ? "fail" : ins.needs_repair ? "needs repair" : "clean"}</StatusPill> : <StatusPill tone="unknown">not run</StatusPill>}
      >
        <div className="flex flex-wrap items-center gap-3">
          {btn("inspect", "Inspect", ScanSearch, !exists)}
          <span className="text-xs text-faint">kvdb check --no-repair: reports what open would do, writes nothing.</span>
        </div>
        {ins && (
          <KV
            rows={[
              ["When", formatDateTime(ins.at)],
              ["Keys / frames", `${formatNumber(ins.keys)} / ${formatNumber(ins.frames)}`],
              ["File size / valid end", `${formatBytes(ins.size)} / ${formatBytes(ins.end)}`],
              ["Tail rule", ins.tail_rule || "none"],
              ["Would repair", ins.repairs?.length ? ins.repairs.join("; ") : "nothing"],
              ...(ins.error ? [["Error", <span key="e" className="text-bad">{ins.error.class}: {ins.error.message}</span>] as [string, React.ReactNode]] : []),
            ]}
          />
        )}
      </Step>

      <Step
        n={5}
        title="Recover and check every acknowledged write"
        state={rec ? <StatusPill tone={rec.ok ? "ok" : "bad"}>{rec.ok ? "pass" : "fail"}</StatusPill> : <StatusPill tone="unknown">not run</StatusPill>}
      >
        <div className="flex flex-wrap items-center gap-3">
          {btn("recover", "Recover", ShieldCheck, !exists)}
          <span className="text-xs text-faint">kvdb.Open (recovery), then GET every acked key, then Verify.</span>
        </div>
        {rec && (
          <div className="space-y-2">
            <KV
              rows={[
                ["When", formatDateTime(rec.at)],
                ["Recovery time", formatMs(rec.open.duration_ms)],
                ["Torn tail discarded", rec.open.tail_rule ? `${formatBytes(rec.open.discarded_bytes)} (rule ${rec.open.tail_rule})` : "none"],
                ["Acked writes present", `${formatNumber(rec.acked_checked - rec.acked_missing - rec.acked_wrong_value)} of ${formatNumber(rec.acked_checked)}`],
                ["Missing / wrong value", `${formatNumber(rec.acked_missing)} / ${formatNumber(rec.acked_wrong_value)}`],
                ["File verified", rec.file_verified ? "yes" : "no"],
                ["Keys after", formatNumber(rec.keys)],
                ...(rec.error ? [["Error", <span key="e" className="text-bad">{rec.error.class}: {rec.error.message}</span>] as [string, React.ReactNode]] : []),
              ]}
            />
            <p className="text-xs text-faint">Expectation: {rec.expectation}.</p>
            {rec.missing_keys_sample && rec.missing_keys_sample.length > 0 && (
              <p className="font-mono text-xs text-muted-foreground">missing: {rec.missing_keys_sample.join(", ")}</p>
            )}
          </div>
        )}
      </Step>

      {exists && <DemoFileMap version={`${demo.file.size}-${demo.file.modified ?? ""}-${demo.events?.[0]?.at ?? ""}`} />}

      {rec && rec.open.at && (
        <div className="mt-3">
          <RecoveryProcess lo={rec.open} title="What the recovery of the demo file did" description="kvdb.Open on the demo database, step by step, with the facts it reported." />
        </div>
      )}

      {demo.events && demo.events.length > 0 && (
        <div className="mt-2 rounded-sm border border-border bg-background">
          <div className="border-b border-border px-3 py-1.5 text-[11px] uppercase tracking-wider text-faint">Drill log</div>
          <ul className="max-h-56 overflow-y-auto font-mono text-xs">
            {demo.events.map((e, i) => (
              <li key={i} className="flex gap-3 border-b border-border/60 px-3 py-1.5 last:border-b-0">
                <span className="text-faint">{formatTime(e.at)}</span>
                <span className={cn("w-16 shrink-0", e.ok ? "text-muted-foreground" : "text-bad")}>{e.action}</span>
                <span className="text-muted-foreground">{e.summary}</span>
              </li>
            ))}
          </ul>
        </div>
      )}

      <ConfirmDialog
        open={confirmTear}
        onOpenChange={setConfirmTear}
        destructive
        title={`Cut ${tearBytes} bytes off the demo file?`}
        confirmLabel="Tear tail"
        description="Only the demo database is touched. The next open will detect and discard the torn frame."
        onConfirm={() => act.mutate({ kind: "tear" })}
      />
    </Panel>
  );
}
