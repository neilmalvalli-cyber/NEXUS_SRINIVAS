"use client";

// The PUT write path, driven by a real PUT. While the request is in flight the
// steps light up in order to show the sequence; only the acknowledgement and
// the total time are facts reported by the backend. The engine does not
// report per-step timings, so none are shown.
import { useEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Check, Loader2, Send, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useNexus } from "@/components/providers";
import { useCanAct, useInvalidateDb } from "@/lib/queries";
import { describeError, isApiError } from "@/lib/api/client";
import { WRITE_PATH, putFrameSize } from "@/lib/format-spec";
import { byteLength, formatMs } from "@/lib/format";
import { cn } from "@/lib/utils";

export function WritePath({ compact }: { compact?: boolean }) {
  const { api } = useNexus();
  const { ready, reason } = useCanAct();
  const invalidate = useInvalidateDb();
  const [key, setKey] = useState("sensor:7");
  const [value, setValue] = useState('{"temp": 21.5}');
  const [lit, setLit] = useState(-1);
  const put = useMutation({
    mutationFn: () => api.putKey(key, value, "utf8"),
    onSettled: () => invalidate(),
  });

  // Walk steps 0..6 while waiting; step 7 (ACK) only on a real success.
  useEffect(() => {
    if (!put.isPending) return;
    const id = setInterval(() => setLit((n) => (n < 6 ? n + 1 : n)), 140);
    return () => clearInterval(id);
  }, [put.isPending]);

  const start = () => {
    setLit(0);
    put.mutate();
  };
  const done = put.isSuccess;
  const failed = put.isError;
  const shown = done ? 7 : failed ? -1 : lit;

  return (
    <div className="space-y-4">
      <form
        className="flex flex-wrap items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (key) start();
        }}
      >
        <label className="space-y-1 text-xs text-muted-foreground">
          Key
          <Input value={key} onChange={(e) => setKey(e.target.value)} className="h-7 w-40 font-mono text-xs" spellCheck={false} />
        </label>
        <label className="min-w-40 flex-1 space-y-1 text-xs text-muted-foreground">
          Value
          <Input value={value} onChange={(e) => setValue(e.target.value)} className="h-7 font-mono text-xs" spellCheck={false} />
        </label>
        <Button size="sm" type="submit" disabled={!ready || !key || put.isPending} title={!ready ? reason : undefined}>
          {put.isPending ? <Loader2 className="animate-spin" /> : <Send />} Send PUT
        </Button>
        <span className="w-full font-mono text-[11px] text-faint">
          This PUT becomes one frame of 32 + 11 + {byteLength(key)} + {byteLength(value)} = {putFrameSize(byteLength(key), byteLength(value))} bytes.
        </span>
      </form>

      <ol className={cn("grid gap-2", compact ? "sm:grid-cols-2" : "sm:grid-cols-2 xl:grid-cols-4")}>
        {WRITE_PATH.map((s, i) => {
          const on = i <= shown;
          const ack = i === 7;
          return (
            <li
              key={s.title}
              className={cn(
                "rounded-sm border px-3 py-2 transition-colors duration-200",
                on ? (ack ? "border-ok/60 bg-ok/10" : "border-foreground/30 bg-hover") : "border-border bg-background",
                ack && failed && "border-bad/60 bg-bad/10",
              )}
            >
              <div className="flex items-center gap-2 text-xs">
                <span className={cn("grid size-5 shrink-0 place-items-center rounded-full border font-mono text-[10px]", on ? "border-foreground/50 text-foreground" : "border-border text-faint")}>
                  {ack && done ? <Check className="size-3 text-ok" /> : ack && failed ? <X className="size-3 text-bad" /> : i + 1}
                </span>
                <span className={on ? "text-foreground" : "text-muted-foreground"}>{s.title}</span>
              </div>
              {!compact && <p className="mt-1 text-[11px] leading-relaxed text-faint">{s.detail}</p>}
            </li>
          );
        })}
      </ol>

      <div className="text-xs" aria-live="polite">
        {put.isPending && <span className="text-muted-foreground">Waiting for the engine… nothing is acknowledged until the fsync returns.</span>}
        {done && (
          <span className="text-muted-foreground">
            <span className="text-ok">Acknowledged</span> ({put.data.action ?? "stored"}) after the durable write, {formatMs(put.data.elapsed_ms)} measured by nexus-api around kvdb.Put. The steps are shown in order; the engine does not report per-step times.
          </span>
        )}
        {failed && isApiError(put.error) && put.error.outcomeUnknown ? (
          <span className="text-warn">
            Outcome unknown: {describeError(put.error)}. The engine may have made this write durable before the answer was lost. It was not retried; read the key back to find out.
          </span>
        ) : failed ? (
          <span className="text-bad">
            Not acknowledged: {describeError(put.error)}. A write that is not acknowledged may be absent after a crash.
          </span>
        ) : null}
      </div>
    </div>
  );
}
