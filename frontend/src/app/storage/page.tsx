"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { FormatReference, FrameInspector, IndexMap, LogLegend, LogStrip, StopBanner, frameBySeq } from "@/components/log-view";
import { WritePath } from "@/components/write-path";
import { Archive, CheckCircle2, FileText, Loader2, RefreshCw, XCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useNexus } from "@/components/providers";
import { useCanAct, useStorage } from "@/lib/queries";
import { useCompact } from "@/lib/actions";
import { ConfirmDialog, DemoBadge, ErrorState, KV, LoadingBlock, PageHeader, Panel, StatCard, StatusPill } from "@/components/nexus";
import { formatBytes, formatDateTime, formatMs, formatNumber } from "@/lib/format";
import type { CompactResult } from "@/lib/api/schemas";

function CompactionResult({ r, live }: { r: CompactResult; live: boolean }) {
  const saved = r.size_before - r.size_after;
  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2 text-sm">
        {r.ok ? <CheckCircle2 className="size-4 text-ok" /> : <XCircle className="size-4 text-bad" />}
        <span className="text-foreground">{r.ok ? "Compaction completed" : "Compaction failed"}</span>
        <span className="text-xs text-faint">{formatDateTime(r.at)}{live ? "" : " · reported by the backend"}</span>
      </div>
      {r.error && <div className="text-xs text-bad">{r.error.class}: {r.error.message}</div>}
      <KV
        rows={[
          ["Size before", formatBytes(r.size_before)],
          ["Size after", formatBytes(r.size_after)],
          ["Reclaimed", saved >= 0 ? formatBytes(saved) : `grew ${formatBytes(-saved)}`],
          ["Live keys", formatNumber(r.keys)],
          ["Took", formatMs(r.elapsed_ms)],
        ]}
      />
    </div>
  );
}

function EngineView() {
  const { api, mode } = useNexus();
  const [seq, setSeq] = useState<number | undefined>(undefined);
  const q = useQuery({ queryKey: [api.mode, "frames", 120], queryFn: () => api.frames(120), enabled: mode === "live" });
  const frame = frameBySeq(q.data, seq);
  return (
    <>
      <Panel
        title="How NEXUS stores data"
        description="Two structures. The append-only binary log on disk is authoritative: every write is appended as a checksummed frame and never changed in place. The in-memory hash index maps each live key to the offset of its latest entry in that log, so a GET is one lookup plus one read. Lose the index (a crash) and it is rebuilt from the log; lose the log and nothing could rebuild it."
      >
        <div className="grid items-center gap-4 md:grid-cols-[1fr_auto_1.6fr]">
          <div className="rounded-sm border border-border bg-elevated p-3">
            <div className="text-sm text-foreground">In-memory hash index</div>
            <div className="mt-1 font-mono text-[11px] text-muted-foreground">map[key] → {"{offset, length}"}</div>
            <div className="mt-1 text-[11px] text-faint">src/internal/index · volatile, rebuilt at open</div>
          </div>
          <div className="text-center font-mono text-xs text-faint">points into →</div>
          <div className="rounded-sm border border-foreground/30 bg-elevated p-3">
            <div className="text-sm text-foreground">Append-only binary log (the data file)</div>
            <div className="mt-1 font-mono text-[11px] text-muted-foreground">superblock · frame · frame · frame · …</div>
            <div className="mt-1 text-[11px] text-faint">src/internal/storage · durable, authoritative</div>
          </div>
        </div>
      </Panel>

      {mode === "demo" ? null : (
        <Panel
          title="The log, frame by frame"
          description={q.data ? `${q.data.method}. Scanned in ${formatMs(q.data.elapsed_ms)}. Click a frame to decode it. The strip grows as you write.` : "A live read-only map of the open data file."}
          actions={<LogLegend />}
        >
          {q.isPending ? (
            <LoadingBlock rows={4} />
          ) : q.isError ? (
            <ErrorState error={q.error} onRetry={() => q.refetch()} />
          ) : (
            <div className="space-y-4">
              <LogStrip map={q.data} selected={frame?.seq} onSelect={setSeq} />
              <StopBanner map={q.data} />
              <div className="grid gap-4 lg:grid-cols-[1.4fr_1fr]">
                <div className="space-y-2">
                  <div className="text-[11px] uppercase tracking-wider text-faint">{frame ? `Frame seq ${frame.seq}` : "Frames"}</div>
                  {frame ? <FrameInspector frame={frame} /> : <p className="text-xs text-faint">The log has no frames yet. Write something.</p>}
                </div>
                <div className="space-y-2">
                  <div className="text-[11px] uppercase tracking-wider text-faint">Index → log</div>
                  <IndexMap map={q.data} />
                </div>
              </div>
              <KV
                className="text-xs"
                rows={[
                  ["Superblock", q.data.superblock.valid ? `magic ${q.data.superblock.magic}, version ${q.data.superblock.version}, first_seq ${q.data.superblock.first_seq}, snapshot_end ${formatNumber(q.data.superblock.snapshot_end)}` : q.data.superblock.error ?? "invalid"],
                ]}
              />
            </div>
          )}
        </Panel>
      )}

      <Panel title="Write path" description="Send a real PUT and follow it. The acknowledgement comes only after the frame has been appended and synced to disk.">
        <WritePath />
      </Panel>

      <Panel title="Binary format" description="Field layout of format version 1, taken from the Go source. Expand a structure for every field and why it exists.">
        <FormatReference />
      </Panel>
    </>
  );
}

export default function StoragePage() {
  const { mode } = useNexus();
  const { ready, reason } = useCanAct();
  const q = useStorage();
  const compact = useCompact();
  const [confirm, setConfirm] = useState(false);
  const d = q.data;
  const dead = d ? Math.max(0, d.stats.size - 32 - d.stats.live_bytes) : null;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Storage Engine"
        description={
          <span className="flex flex-wrap items-center gap-2">
            {mode === "demo" && <DemoBadge />}
            <span className="font-mono text-xs">{d?.directory ?? "—"}</span>
          </span>
        }
        actions={
          <>
            <Button size="sm" variant="ghost" onClick={() => q.refetch()} disabled={q.isFetching}>
              <RefreshCw className={q.isFetching ? "animate-spin" : ""} /> Refresh
            </Button>
            <Button size="sm" variant="outline" disabled={!ready || compact.isPending} title={!ready ? reason : undefined} onClick={() => setConfirm(true)}>
              {compact.isPending ? <Loader2 className="animate-spin" /> : <Archive />} {compact.isPending ? "Compacting…" : "Compact now"}
            </Button>
          </>
        }
      />

      <EngineView />

      {q.isError && !d ? (
        <ErrorState error={q.error} onRetry={() => q.refetch()} />
      ) : !d ? (
        <LoadingBlock rows={6} />
      ) : (
        <>
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            <StatCard label="File size" value={formatBytes(d.stats.size)} hint="data file on disk" />
            <StatCard label="Live data" value={formatBytes(d.stats.live_bytes)} hint={`${formatNumber(d.stats.keys)} keys (key + value bytes)`} />
            <StatCard label="Overhead & dead records" value={formatBytes(dead)} hint="superseded records, frame headers, superblock" />
            <StatCard label="Size cap" value={d.stats.max_bytes ? formatBytes(d.stats.max_bytes) : "None"} hint="--max-bytes of nexus-api" />
          </div>

          <Panel title="Files" description="Files in the database directory that belong to this database. Temporary files exist only while a compaction runs." bodyClassName="p-0">
            <Table>
              <TableHeader>
                <TableRow className="border-border hover:bg-transparent">
                  <TableHead className="h-8 pl-4 text-xs text-faint">File</TableHead>
                  <TableHead className="h-8 text-xs text-faint">Role</TableHead>
                  <TableHead className="h-8 text-xs text-faint">State</TableHead>
                  <TableHead className="h-8 text-right text-xs text-faint">Size</TableHead>
                  <TableHead className="h-8 text-xs text-faint">Modified</TableHead>
                  <TableHead className="h-8 pr-4 text-xs text-faint">Meaning</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {d.files.map((f) => (
                  <TableRow key={f.role + f.name} className="border-border hover:bg-hover/50">
                    <TableCell className="py-2 pl-4 font-mono text-xs"><FileText className="mr-1.5 inline size-3.5 text-faint" />{f.name}</TableCell>
                    <TableCell className="py-2 font-mono text-xs text-muted-foreground">{f.role}</TableCell>
                    <TableCell className="py-2"><StatusPill tone={f.exists ? "ok" : "unknown"}>{f.exists ? "present" : "absent"}</StatusPill></TableCell>
                    <TableCell className="py-2 text-right font-mono text-xs">{f.exists ? formatBytes(f.size) : "—"}</TableCell>
                    <TableCell className="py-2 font-mono text-xs text-muted-foreground">{f.exists ? formatDateTime(f.modified) : "—"}</TableCell>
                    <TableCell className="max-w-md py-2 pr-4 text-xs whitespace-normal text-muted-foreground">{f.meaning}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </Panel>

          <div className="grid gap-4 lg:grid-cols-2">
            <Panel title="Compaction" description="Rewrites only the live keys into a new file, syncs it, and atomically swaps it in. Requests through this page are reported below; automatic compactions inside the engine are not reported individually.">
              {compact.isPending ? (
                <div className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" /> Compaction running… writes wait until it finishes.</div>
              ) : compact.isError ? (
                <ErrorState error={compact.error} />
              ) : compact.data ? (
                <CompactionResult r={compact.data} live />
              ) : d.last_compaction ? (
                <CompactionResult r={d.last_compaction} live={false} />
              ) : (
                <div className="flex items-center gap-2 text-sm text-faint"><StatusPill tone="unknown">none</StatusPill> No compaction requested through this adapter yet.</div>
              )}
            </Panel>
            <Panel title="Layout" description="How NEXUS stores data. These are facts about the design, not measurements.">
              <KV
                rows={[
                  ["Kind", d.layout.kind],
                  ["WAL", d.layout.wal],
                  ["Levels", d.layout.levels],
                  ["Auto-compaction", d.layout.auto_compaction],
                  ["Measured", formatDateTime(d.measured_at)],
                ]}
                className="[&_dd]:font-sans [&_dd]:text-sm"
              />
            </Panel>
          </div>
        </>
      )}

      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title="Compact the database?"
        confirmLabel="Compact"
        description="The engine rewrites the live keys into a temporary file, syncs it, and renames it over the data file. Writes wait while it runs. If the process dies mid-way, the old file stays authoritative."
        onConfirm={() => compact.mutate()}
      />
    </div>
  );
}
