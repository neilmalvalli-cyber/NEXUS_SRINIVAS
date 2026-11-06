"use client";

import { Fragment, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { CheckCircle2, ChevronDown, ChevronRight, DatabaseZap, FolderOpen, Layers, ListTree, Loader2, Plus, RefreshCw, ScanSearch, Search, Trash2, XCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useNexus } from "@/components/providers";
import { useCanAct, useInvalidateDb, useOverview } from "@/lib/queries";
import { useVerify } from "@/lib/actions";
import { toastError } from "@/lib/mutations";
import { ConfirmDialog, CopyButton, DemoBadge, EmptyState, ErrorState, KV, LoadingBlock, PageHeader, Panel, StatusPill } from "@/components/nexus";
import { KeyOpsPanel } from "@/components/key-ops";
import { describeError } from "@/lib/api/client";
import type { BatchOp } from "@/lib/api/service";
import type { WriteResult } from "@/lib/api/schemas";
import { formatBytes, formatDateTime, formatMs, formatNumber, prettyJson } from "@/lib/format";
import { cn } from "@/lib/utils";

const NAME_RE = /^[A-Za-z0-9][A-Za-z0-9_-]{0,47}$/;

/** Choose, open or create the database the adapter serves. */
function DatabasePicker() {
  const { api, mode } = useNexus();
  const { ready, reason } = useCanAct();
  const qc = useQueryClient();
  const list = useQuery({ queryKey: [api.mode, "databases"], queryFn: api.databases, enabled: mode === "live" });
  const [name, setName] = useState("");
  const [confirm, setConfirm] = useState<{ name: string; create: boolean } | null>(null);
  const open = useMutation({
    mutationFn: (v: { name: string; create: boolean }) => api.openDatabase(v.name, v.create),
    onSuccess: (r) => {
      toast.success(r.action === "created" ? `Created database ${r.name}` : `Opened database ${r.name}`, {
        description: `${formatNumber(r.stats.keys)} keys · open took ${formatMs(r.last_open.duration_ms)}${r.last_open.tail_rule ? ` · torn tail discarded (${r.last_open.tail_rule})` : ""}`,
      });
      setName("");
      qc.invalidateQueries({ queryKey: [api.mode] });
    },
    onError: (e) => toastError("Open database", e),
  });
  if (mode === "demo") return null;
  const valid = NAME_RE.test(name);
  const exists = list.data?.files.some((f) => f.name.toLowerCase() === name.toLowerCase());
  return (
    <Panel
      title={<span className="flex items-center gap-2"><FolderOpen className="size-4 text-faint" /> Database</span>}
      description={list.data ? <>Files in <span className="break-all font-mono">{list.data.directory}</span>. Switching closes the current file and opens the other one with full recovery.</> : "Databases the adapter can open."}
    >
      {list.isPending ? (
        <LoadingBlock rows={2} />
      ) : list.isError ? (
        <ErrorState error={list.error} onRetry={() => list.refetch()} compact />
      ) : (
        <div className="space-y-3">
          <div className="flex flex-wrap gap-1.5">
            {list.data.files.map((f) => (
              <button
                key={f.file}
                type="button"
                disabled={f.current || !ready || open.isPending}
                onClick={() => setConfirm({ name: f.name, create: false })}
                className={cn(
                  "flex items-center gap-2 rounded-sm border px-2.5 py-1 font-mono text-xs transition-colors",
                  f.current ? "border-foreground/40 bg-hover text-foreground" : "border-border text-muted-foreground hover:bg-hover hover:text-foreground disabled:opacity-50",
                )}
                title={f.current ? "currently open" : `open ${f.file}`}
              >
                {f.current && <span className="size-1.5 rounded-full bg-ok" />}
                {f.name}
                <span className="text-faint">{formatBytes(f.size)}</span>
              </button>
            ))}
          </div>
          <form
            className="flex flex-wrap items-start gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (valid) setConfirm({ name, create: !exists });
            }}
          >
            <div className="space-y-1">
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="new database name" aria-label="Database name" className="h-7 w-56 font-mono text-xs" spellCheck={false} />
              {name && !valid && <p className="text-[11px] text-bad">1–48 characters: letters, digits, _ or -. No paths.</p>}
            </div>
            <Button size="sm" variant="outline" type="submit" disabled={!valid || !ready || open.isPending} title={!ready ? reason : undefined}>
              {open.isPending ? <Loader2 className="animate-spin" /> : exists ? <FolderOpen /> : <Plus />} {exists ? "Open" : "Create"}
            </Button>
          </form>
        </div>
      )}
      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(o) => !o && setConfirm(null)}
        title={confirm?.create ? `Create database “${confirm?.name}”?` : `Open database “${confirm?.name}”?`}
        confirmLabel={confirm?.create ? "Create" : "Open"}
        description={
          <span className="block space-y-2">
            <span className="block">The adapter closes <span className="font-mono text-foreground">{list.data?.current}</span> (every acknowledged write is already on disk) and {confirm?.create ? "creates" : "opens"} <span className="font-mono text-foreground">{confirm?.name}.kv</span> in the same directory.</span>
            <span className="block">All pages then show the newly opened database. Nothing is deleted.</span>
          </span>
        }
        onConfirm={() => confirm && open.mutate(confirm)}
      />
    </Panel>
  );
}

type Row = { op: "put" | "del"; key: string; value: string };

function BatchPanel() {
  const { api } = useNexus();
  const { ready, reason } = useCanAct();
  const invalidate = useInvalidateDb();
  const [rows, setRows] = useState<Row[]>([
    { op: "put", key: "account:alice", value: "90" },
    { op: "put", key: "account:bob", value: "110" },
    { op: "del", key: "transfer:pending", value: "" },
  ]);
  const [result, setResult] = useState<{ ok: true; res: WriteResult; n: number } | { ok: false; message: string } | null>(null);
  const run = useMutation({
    mutationFn: (ops: BatchOp[]) => api.batch(ops),
    onSuccess: (res, ops) => {
      setResult({ ok: true, res, n: ops.length });
      toast.success(`BATCH of ${ops.length} acknowledged`, { description: "one frame, one fsync: all or nothing" });
      invalidate();
    },
    onError: (e) => {
      setResult({ ok: false, message: describeError(e) });
      toastError("BATCH", e);
      invalidate();
    },
  });
  const set = (i: number, patch: Partial<Row>) => setRows((r) => r.map((x, j) => (j === i ? { ...x, ...patch } : x)));
  const empty = rows.some((r) => r.key === "");
  return (
    <Panel
      title={<span className="flex items-center gap-2"><Layers className="size-4 text-faint" /> BATCH</span>}
      description="Several puts and deletes applied atomically: the engine encodes them into ONE frame with one payload checksum and syncs once. After a crash either every operation is present or none is."
    >
      <div className="space-y-2">
        {rows.map((r, i) => (
          <div key={i} className="flex flex-wrap items-center gap-2">
            <select aria-label={`Operation ${i + 1}`} value={r.op} onChange={(e) => set(i, { op: e.target.value as Row["op"], value: e.target.value === "del" ? "" : r.value })} className="h-7 rounded-sm border border-border bg-background px-1 font-mono text-xs">
              <option value="put">PUT</option>
              <option value="del">DEL</option>
            </select>
            <Input aria-label={`Key ${i + 1}`} value={r.key} onChange={(e) => set(i, { key: e.target.value })} placeholder="key" className="h-7 w-44 font-mono text-xs" spellCheck={false} />
            <Input aria-label={`Value ${i + 1}`} value={r.value} disabled={r.op === "del"} onChange={(e) => set(i, { value: e.target.value })} placeholder={r.op === "del" ? "(no value)" : "value"} className="h-7 min-w-32 flex-1 font-mono text-xs" spellCheck={false} />
            <Button size="icon-xs" variant="ghost" aria-label={`Remove operation ${i + 1}`} onClick={() => setRows((x) => x.filter((_, j) => j !== i))} disabled={rows.length === 1}>
              <Trash2 />
            </Button>
          </div>
        ))}
        <div className="flex flex-wrap items-center gap-2 pt-1">
          <Button size="xs" variant="ghost" onClick={() => setRows((x) => [...x, { op: "put", key: "", value: "" }])} disabled={rows.length >= 100}>
            <Plus /> Add operation
          </Button>
          <Button size="sm" className="ml-auto" disabled={!ready || run.isPending || empty} title={!ready ? reason : empty ? "every operation needs a key" : undefined} onClick={() => run.mutate(rows.map((r) => (r.op === "put" ? { op: "put", key: r.key, value: r.value } : { op: "del", key: r.key })))}>
            {run.isPending ? <Loader2 className="animate-spin" /> : <DatabaseZap />} Run BATCH ({rows.length})
          </Button>
        </div>
        {result &&
          (result.ok ? (
            <div className="flex items-start gap-2 rounded-md border border-border bg-elevated/60 px-3 py-2 text-sm">
              <CheckCircle2 className="mt-0.5 size-4 text-ok" />
              <div className="text-xs text-muted-foreground">
                <span className="text-foreground">{result.n} operations committed as one frame.</span> Durable after one fsync, {formatMs(result.res.elapsed_ms)}.
              </div>
            </div>
          ) : (
            <div className="flex items-start gap-2 rounded-md border border-bad/40 bg-bad/5 px-3 py-2 text-sm">
              <XCircle className="mt-0.5 size-4 text-bad" />
              <div className="text-xs text-muted-foreground"><span className="text-foreground">BATCH rejected; nothing was applied.</span> {result.message}</div>
            </div>
          ))}
      </div>
    </Panel>
  );
}

function CheckPanel() {
  const { ready, reason } = useCanAct();
  const verify = useVerify();
  const ov = useOverview(false);
  const r = verify.data ?? ov.data?.last_verify ?? null;
  return (
    <Panel
      title={<span className="flex items-center gap-2"><ScanSearch className="size-4 text-faint" /> CHECK</span>}
      description="Re-reads the whole file through the open handle (kvdb.Verify, the check behind `kvdb check`): superblock, every frame and entry checksum, sequence numbers, the end on a frame boundary, and that the index matches the file."
      actions={
        <Button size="sm" variant="outline" disabled={!ready || verify.isPending} title={!ready ? reason : undefined} onClick={() => verify.mutate()}>
          {verify.isPending ? <Loader2 className="animate-spin" /> : <ScanSearch />} Run CHECK
        </Button>
      }
    >
      {verify.isPending ? (
        <div className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" /> Reading the file…</div>
      ) : verify.isError ? (
        <ErrorState error={verify.error} compact />
      ) : r ? (
        <KV
          rows={[
            ["Integrity", <StatusPill key="s" tone={r.ok ? "ok" : "bad"}>{r.ok ? "pass" : "fail"}</StatusPill>],
            ["Frames checked", formatNumber(r.frames)],
            ["Keys", formatNumber(r.keys)],
            ["File size", formatBytes(r.size)],
            ["Took", formatMs(r.elapsed_ms)],
            ["When", formatDateTime(r.at)],
            ...(r.error ? [["Error", <span key="e" className="text-bad">{r.error.class}: {r.error.message}</span>] as [string, React.ReactNode]] : []),
          ]}
        />
      ) : (
        <div className="flex items-center gap-2 text-sm text-faint"><StatusPill tone="unknown">not run</StatusPill> Integrity is unknown until a check runs.</div>
      )}
    </Panel>
  );
}

function DumpPanel() {
  const { api } = useNexus();
  const [input, setInput] = useState("");
  const [q, setQ] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  const dump = useQuery({ queryKey: [api.mode, "keys", "dump", q], queryFn: () => api.dump(q, 1000) });
  return (
    <Panel
      title={<span className="flex items-center gap-2"><ListTree className="size-4 text-faint" /> DUMP</span>}
      description="Every current record, in key order, read through the engine (kvdb.Range: the index gives each key's latest offset, the value is read from the log). Click a row for the full value."
      bodyClassName="p-0"
      actions={
        <Button size="xs" variant="ghost" onClick={() => dump.refetch()} disabled={dump.isFetching} aria-label="Refresh dump">
          <RefreshCw className={dump.isFetching ? "animate-spin" : ""} />
        </Button>
      }
    >
      <form className="flex items-center gap-2 border-b border-border px-4 py-3" onSubmit={(e) => { e.preventDefault(); setQ(input.trim()); }}>
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-faint" />
          <Input value={input} onChange={(e) => setInput(e.target.value)} placeholder="Search keys (substring, case-insensitive)" aria-label="Search keys" className="pl-7 font-mono text-xs" spellCheck={false} />
        </div>
        <Button size="sm" variant="outline" type="submit">Search</Button>
        {q && <Button size="sm" variant="ghost" type="button" onClick={() => { setInput(""); setQ(""); }}>Clear</Button>}
      </form>
      {dump.isPending ? (
        <div className="p-4"><LoadingBlock rows={6} /></div>
      ) : dump.isError ? (
        <div className="p-4"><ErrorState error={dump.error} onRetry={() => dump.refetch()} /></div>
      ) : dump.data.items.length === 0 ? (
        <EmptyState title={q ? `No key contains “${q}”` : "The database is empty"}>{q ? "Try another search." : "PUT a key or run a BATCH and it appears here."}</EmptyState>
      ) : (
        <div className="max-h-[32rem] overflow-y-auto">
          <Table>
            <TableHeader>
              <TableRow className="border-border hover:bg-transparent">
                <TableHead className="h-8 w-6 pl-4" />
                <TableHead className="h-8 text-xs text-faint">Key</TableHead>
                <TableHead className="h-8 text-xs text-faint">Value</TableHead>
                <TableHead className="h-8 pr-4 text-right text-xs text-faint">Size</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {dump.data.items.map((it) => {
                const id = it.key.encoding + it.key.data;
                const isOpen = open === id;
                const pretty = it.value.encoding === "utf8" ? prettyJson(it.value.data) : null;
                return (
                  <Fragment key={id}>
                    <TableRow className="cursor-pointer border-border hover:bg-hover/50" onClick={() => setOpen(isOpen ? null : id)}>
                      <TableCell className="py-1.5 pl-4 text-faint">{isOpen ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}</TableCell>
                      <TableCell className="max-w-[16rem] truncate py-1.5 font-mono text-xs" title={it.key.data}>
                        {it.key.data}
                        {it.key.encoding === "base64" && <span className="ml-1 text-faint">(base64)</span>}
                      </TableCell>
                      <TableCell className="max-w-[28rem] truncate py-1.5 font-mono text-xs text-muted-foreground">
                        {it.value.encoding === "base64" ? <span className="text-faint">binary ({it.value.data.slice(0, 24)}…)</span> : it.value.data || <span className="text-faint">(empty)</span>}
                      </TableCell>
                      <TableCell className="py-1.5 pr-4 text-right font-mono text-xs text-muted-foreground">{formatBytes(it.size)}</TableCell>
                    </TableRow>
                    {isOpen && (
                      <TableRow className="border-border bg-background hover:bg-background">
                        <TableCell colSpan={4} className="px-4 py-3">
                          <div className="mb-2 flex flex-wrap gap-2">
                            <CopyButton text={it.key.data} label="Copy key" />
                            <CopyButton text={it.value.data} label="Copy value" />
                            {it.value_truncated && <span className="text-xs text-faint">value shown up to 4 KiB of {formatBytes(it.size)}; GET reads all of it</span>}
                          </div>
                          <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-sm border border-border bg-card p-2 font-mono text-xs">{pretty ?? it.value.data}</pre>
                        </TableCell>
                      </TableRow>
                    )}
                  </Fragment>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}
      {dump.data && (
        <div className="border-t border-border px-4 py-2 text-xs text-muted-foreground">
          {formatNumber(dump.data.returned)} shown · {formatNumber(dump.data.matched)} matching · {formatNumber(dump.data.total_keys)} keys in total · {formatMs(dump.data.elapsed_ms)}
          {dump.data.truncated && " · list capped at 1,000 records; narrow the search"}
        </div>
      )}
    </Panel>
  );
}

export default function DatabaseExplorerPage() {
  const { mode } = useNexus();
  const ov = useOverview(false);
  const lim = ov.data?.limits;
  return (
    <div className="space-y-6">
      <PageHeader
        title="Database Explorer"
        description={
          <span className="flex flex-wrap items-center gap-2">
            {mode === "demo" && <DemoBadge />}
            Every operation here runs against the real NEXUS database through nexus-api. Writes are acknowledged only after the engine has synced them to disk.
            {ov.data && (
              <span className="font-mono text-xs text-faint">
                open: {ov.data.database ?? ov.data.db_path} · {formatNumber(ov.data.stats.keys)} keys{lim && ` · key ≤ ${formatBytes(lim.max_key_bytes)} · value ≤ ${formatBytes(lim.max_value_bytes)} · batch ≤ ${formatNumber(lim.max_batch_ops)}`}
              </span>
            )}
          </span>
        }
      />
      <DatabasePicker />
      <div className="grid gap-4 xl:grid-cols-2">
        <Panel title="PUT · GET · DELETE" description="PUT tells you whether it inserted a new key or updated an existing one; GET and DELETE show NOTFOUND for missing keys.">
          <KeyOpsPanel />
        </Panel>
        <div className="space-y-4">
          <BatchPanel />
          <CheckPanel />
        </div>
      </div>
      <DumpPanel />
    </div>
  );
}
