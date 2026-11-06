"use client";

// PUT / GET / DELETE forms for one key, used by the Database page and by the
// quick actions on the Overview. Every result shown here is the adapter's
// answer; nothing is reported as stored until the backend acknowledged it.
import { useState } from "react";
import { useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { CheckCircle2, CircleSlash, Download, Loader2, Trash2, Upload, XCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useNexus } from "@/components/providers";
import { useCanAct, useInvalidateDb } from "@/lib/queries";
import { describeError, isApiError } from "@/lib/api/client";
import type { Encoding } from "@/lib/api/service";
import type { GetResult, WriteResult } from "@/lib/api/schemas";
import { toastError } from "@/lib/mutations";
import { byteLength, formatBytes, formatMs, prettyJson } from "@/lib/format";
import { ConfirmDialog, CopyButton } from "@/components/nexus";
import { cn } from "@/lib/utils";

export type KeyOp = "put" | "get" | "delete";

const MAX_KEY = 1024;
const MAX_VALUE = 1 << 20;

const keyField = z.string().min(1, "Enter a key");
const putSchema = z.object({ key: keyField, value: z.string(), encoding: z.enum(["utf8", "base64"]) });
const keySchema = z.object({ key: keyField });
type PutForm = z.infer<typeof putSchema>;
type KeyForm = z.infer<typeof keySchema>;

function FieldError({ msg }: { msg?: string }) {
  return msg ? <p className="text-xs text-bad">{msg}</p> : null;
}

function SizeHint({ text, limit, encoding }: { text: string; limit: number; encoding?: Encoding }) {
  const n = encoding === "base64" ? Math.floor((text.replace(/=+$/, "").length * 3) / 4) : byteLength(text);
  const over = n > limit;
  return (
    <span className={cn("font-mono text-[11px]", over ? "text-warn" : "text-faint")}>
      {formatBytes(n)} / {formatBytes(limit)}
      {over && " · over the engine limit; the backend will reject it"}
    </span>
  );
}

type Outcome =
  | { kind: "put"; key: string; res: WriteResult }
  | { kind: "get"; key: string; res: GetResult }
  | { kind: "delete"; key: string; res: WriteResult }
  | { kind: "notfound"; key: string }
  | { kind: "error"; op: string; key: string; message: string; unknown: boolean };

function OutcomeView({ o }: { o: Outcome }) {
  if (o.kind === "notfound")
    return (
      <div className="flex items-start gap-2 rounded-md border border-border bg-elevated/60 px-3 py-2.5 text-sm" data-testid="result-notfound">
        <CircleSlash className="mt-0.5 size-4 text-faint" />
        <div>
          <div className="text-foreground">Key not found</div>
          <div className="font-mono text-xs text-muted-foreground break-all">{o.key}</div>
          <div className="mt-1 text-xs text-faint">The backend answered 404 notfound: the key does not exist. This is not a failure.</div>
        </div>
      </div>
    );
  if (o.kind === "error")
    return (
      <div className="flex items-start gap-2 rounded-md border border-bad/40 bg-bad/5 px-3 py-2.5 text-sm" data-testid="result-error">
        <XCircle className="mt-0.5 size-4 text-bad" />
        <div className="min-w-0">
          <div className="text-foreground">
            {o.op.toUpperCase()} {o.unknown ? "outcome unknown" : "failed"}
          </div>
          <div className="break-words text-xs text-muted-foreground">{o.message}</div>
          {o.unknown && <div className="mt-1 text-xs text-warn">The write may or may not be durable. Read the key back to find out.</div>}
        </div>
      </div>
    );
  if (o.kind === "get") {
    const v = o.res.value;
    const pretty = v.encoding === "utf8" ? prettyJson(v.data) : null;
    return (
      <div className="space-y-2 rounded-md border border-border bg-elevated/60 p-3" data-testid="result-get">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
          <span className="flex items-center gap-1.5 text-foreground">
            <CheckCircle2 className="size-3.5 text-ok" /> Found
          </span>
          <span className="font-mono break-all">{o.key}</span>
          <span>{formatBytes(o.res.size)}</span>
          <span>{v.encoding === "base64" ? "binary (shown as base64)" : pretty ? "JSON" : "text"}</span>
          <span>{formatMs(o.res.elapsed_ms)}</span>
          <CopyButton text={v.data} label="Copy value" className="ml-auto" />
        </div>
        <pre className="max-h-80 overflow-auto rounded-sm border border-border bg-background p-2.5 font-mono text-xs whitespace-pre-wrap break-all text-foreground" data-testid="get-value">
          {pretty ?? (v.data.length > 200_000 ? v.data.slice(0, 200_000) + `\n… (${formatBytes(o.res.size)} total; truncated for display, Copy copies everything)` : v.data)}
        </pre>
      </div>
    );
  }
  return (
    <div className="flex items-start gap-2 rounded-md border border-border bg-elevated/60 px-3 py-2.5 text-sm" data-testid={`result-${o.kind}`}>
      <CheckCircle2 className="mt-0.5 size-4 text-ok" />
      <div className="min-w-0">
        <div className="text-foreground">
          {o.kind === "put"
            ? o.res.action === "inserted"
              ? "Inserted new key"
              : o.res.action === "updated"
                ? "Updated existing key"
                : "Stored"
            : o.res.action === "absent"
              ? "Key did not exist (nothing written)"
              : "Deleted"}{" "}
          <span className="font-mono text-xs text-muted-foreground break-all">{o.key}</span>
        </div>
        <div className="text-xs text-faint">
          {o.res.action === "absent"
            ? "NEXUS treats deleting a missing key as success and performs no I/O. A GET now answers NOTFOUND."
            : o.res.durable
              ? "Acknowledged after fsync: durable on disk."
              : "Acknowledged (sample store, not durable)."}
          {o.res.size !== undefined && ` ${formatBytes(o.res.size)}.`} {formatMs(o.res.elapsed_ms)}
        </div>
      </div>
    </div>
  );
}

function errorOutcome(op: string, key: string, err: unknown): Outcome {
  if (isApiError(err) && err.isNotFound) return { kind: "notfound", key };
  return { kind: "error", op, key, message: describeError(err), unknown: isApiError(err) && err.outcomeUnknown };
}

export function KeyOpsPanel({ initialOp = "put", initialKey = "", onChanged }: { initialOp?: KeyOp; initialKey?: string; onChanged?: () => void }) {
  const { api } = useNexus();
  const { ready, reason } = useCanAct();
  const invalidate = useInvalidateDb();
  const [op, setOp] = useState<KeyOp>(initialOp);
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [fillBytes, setFillBytes] = useState("");

  const putForm = useForm<PutForm>({ resolver: zodResolver(putSchema), defaultValues: { key: initialKey, value: "", encoding: "utf8" } });
  const getForm = useForm<KeyForm>({ resolver: zodResolver(keySchema), defaultValues: { key: initialKey } });
  const delForm = useForm<KeyForm>({ resolver: zodResolver(keySchema), defaultValues: { key: initialKey } });

  const put = useMutation({
    mutationFn: (f: PutForm) => api.putKey(f.key, f.value, f.encoding),
    onSuccess: (res, f) => {
      setOutcome({ kind: "put", key: f.key, res });
      toast.success(res.action === "updated" ? "PUT: updated existing key" : res.action === "inserted" ? "PUT: inserted new key" : "PUT acknowledged", { description: f.key });
      invalidate();
      onChanged?.();
    },
    onError: (err, f) => {
      setOutcome(errorOutcome("put", f.key, err));
      toastError("PUT", err);
      invalidate();
    },
  });
  const get = useMutation({
    mutationFn: (f: KeyForm) => api.getKey(f.key),
    onSuccess: (res, f) => setOutcome({ kind: "get", key: f.key, res }),
    onError: (err, f) => {
      setOutcome(errorOutcome("get", f.key, err));
      if (!(isApiError(err) && err.isNotFound)) toastError("GET", err);
    },
    onSettled: () => invalidate(),
  });
  const del = useMutation({
    mutationFn: (key: string) => api.deleteKey(key),
    onSuccess: (res, key) => {
      setOutcome({ kind: "delete", key, res });
      toast.success(res.action === "absent" ? "DELETE: key did not exist" : "DELETE acknowledged", { description: key });
      invalidate();
      onChanged?.();
    },
    onError: (err, key) => {
      setOutcome(errorOutcome("delete", key, err));
      toastError("DELETE", err);
      invalidate();
    },
  });

  const busy = put.isPending || get.isPending || del.isPending;
  const value = useWatch({ control: putForm.control, name: "value" });
  const encoding = useWatch({ control: putForm.control, name: "encoding" });
  const putKey = useWatch({ control: putForm.control, name: "key" });

  return (
    <div className="space-y-4">
      <Tabs value={op} onValueChange={(v) => { setOp(v as KeyOp); setOutcome(null); }}>
        <TabsList className="bg-elevated">
          <TabsTrigger value="put" className="px-3"><Upload className="size-3.5" /> PUT</TabsTrigger>
          <TabsTrigger value="get" className="px-3"><Download className="size-3.5" /> GET</TabsTrigger>
          <TabsTrigger value="delete" className="px-3"><Trash2 className="size-3.5" /> DELETE</TabsTrigger>
        </TabsList>

        <TabsContent value="put" className="pt-2">
          <form className="space-y-3" onSubmit={putForm.handleSubmit((f) => put.mutate(f))} noValidate>
            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <Label htmlFor="put-key">Key</Label>
                <SizeHint text={putKey} limit={MAX_KEY} />
              </div>
              <Input id="put-key" autoComplete="off" spellCheck={false} className="font-mono" placeholder="user:42" {...putForm.register("key")} />
              <FieldError msg={putForm.formState.errors.key?.message} />
            </div>
            <div className="space-y-1.5">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <Label htmlFor="put-value">Value</Label>
                <div className="flex items-center gap-2">
                  <SizeHint text={value} limit={MAX_VALUE} encoding={encoding} />
                  <select
                    aria-label="Value encoding"
                    className="h-6 rounded-sm border border-border bg-background px-1 text-xs text-muted-foreground"
                    {...putForm.register("encoding")}
                  >
                    <option value="utf8">text (UTF-8)</option>
                    <option value="base64">base64 (binary)</option>
                  </select>
                </div>
              </div>
              <Textarea id="put-value" spellCheck={false} rows={6} className="font-mono text-xs" placeholder='{"name": "Asha"}' {...putForm.register("value")} />
              <div className="flex flex-wrap items-center gap-2 text-xs text-faint">
                <span>Fill with</span>
                <Input
                  aria-label="Bytes to generate"
                  inputMode="numeric"
                  className="h-6 w-24 font-mono text-xs"
                  placeholder="bytes"
                  value={fillBytes}
                  onChange={(e) => setFillBytes(e.target.value.replace(/\D/g, ""))}
                />
                <span>generated bytes</span>
                <Button
                  type="button"
                  size="xs"
                  variant="outline"
                  disabled={!fillBytes}
                  onClick={() => {
                    const n = Math.min(Number(fillBytes), 2 * MAX_VALUE);
                    putForm.setValue("encoding", "utf8");
                    putForm.setValue("value", "x".repeat(n), { shouldDirty: true });
                  }}
                >
                  Fill
                </Button>
                <span>(for testing large values; up to 2 MiB so the limit can be tried)</span>
              </div>
            </div>
            <Button type="submit" disabled={!ready || busy} title={!ready ? reason : undefined}>
              {put.isPending ? <Loader2 className="animate-spin" /> : <Upload />} PUT
            </Button>
          </form>
        </TabsContent>

        <TabsContent value="get" className="pt-2">
          <form className="flex flex-wrap items-start gap-2" onSubmit={getForm.handleSubmit((f) => get.mutate(f))} noValidate>
            <div className="min-w-64 flex-1 space-y-1">
              <Label htmlFor="get-key" className="sr-only">Key</Label>
              <Input id="get-key" autoComplete="off" spellCheck={false} className="font-mono" placeholder="key to read" {...getForm.register("key")} />
              <FieldError msg={getForm.formState.errors.key?.message} />
            </div>
            <Button type="submit" disabled={!ready || busy} title={!ready ? reason : undefined}>
              {get.isPending ? <Loader2 className="animate-spin" /> : <Download />} GET
            </Button>
          </form>
        </TabsContent>

        <TabsContent value="delete" className="pt-2">
          <form className="flex flex-wrap items-start gap-2" onSubmit={delForm.handleSubmit((f) => setConfirmDelete(f.key))} noValidate>
            <div className="min-w-64 flex-1 space-y-1">
              <Label htmlFor="del-key" className="sr-only">Key</Label>
              <Input id="del-key" autoComplete="off" spellCheck={false} className="font-mono" placeholder="key to delete" {...delForm.register("key")} />
              <FieldError msg={delForm.formState.errors.key?.message} />
            </div>
            <Button type="submit" variant="destructive" disabled={!ready || busy} title={!ready ? reason : undefined}>
              {del.isPending ? <Loader2 className="animate-spin" /> : <Trash2 />} DELETE
            </Button>
          </form>
        </TabsContent>
      </Tabs>

      {outcome && <OutcomeView o={outcome} />}

      <ConfirmDialog
        open={confirmDelete !== null}
        onOpenChange={(o) => !o && setConfirmDelete(null)}
        title="Delete this key?"
        destructive
        confirmLabel="Delete"
        description={
          <span>
            <span className="block break-all font-mono text-foreground">{confirmDelete}</span>
            <span className="mt-2 block">A delete record is appended to the log and synced before the backend answers. The old value is gone for readers immediately.</span>
          </span>
        }
        onConfirm={() => confirmDelete !== null && del.mutate(confirmDelete)}
      />
    </div>
  );
}
