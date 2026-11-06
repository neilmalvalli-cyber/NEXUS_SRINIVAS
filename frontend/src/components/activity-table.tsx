"use client";

import { useQuery } from "@tanstack/react-query";
import { Activity as ActivityIcon } from "lucide-react";
import { useNexus } from "@/components/providers";
import { EmptyState, ErrorState, LoadingBlock, Panel, StatusPill, type Tone } from "@/components/nexus";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatMs, formatTime } from "@/lib/format";

export function resultTone(result: string): Tone {
  if (result === "ok") return "ok";
  if (result === "notfound") return "unknown";
  return "bad";
}

export function ActivityPanel({ limit = 12, poll = true }: { limit?: number; poll?: boolean }) {
  const { api } = useNexus();
  const q = useQuery({ queryKey: [api.mode, "activity", limit], queryFn: () => api.activity(limit), refetchInterval: poll ? 10_000 : false });
  return (
    <Panel
      title="Recent activity"
      description="Operations served by this nexus-api process since it started (not engine-wide)."
      bodyClassName="p-0"
    >
      {q.isPending ? (
        <div className="p-4"><LoadingBlock rows={4} /></div>
      ) : q.isError ? (
        <div className="p-4"><ErrorState error={q.error} onRetry={() => q.refetch()} compact /></div>
      ) : q.data.items.length === 0 ? (
        <EmptyState icon={ActivityIcon} title="No operations yet">
          Requests appear here as you PUT, GET, DELETE, list, compact or verify through the dashboard or the API.
        </EmptyState>
      ) : (
        <Table>
          <TableHeader>
            <TableRow className="border-border hover:bg-transparent">
              <TableHead className="h-8 pl-4 text-xs text-faint">Time</TableHead>
              <TableHead className="h-8 text-xs text-faint">Op</TableHead>
              <TableHead className="h-8 text-xs text-faint">Key / detail</TableHead>
              <TableHead className="h-8 text-xs text-faint">Result</TableHead>
              <TableHead className="h-8 pr-4 text-right text-xs text-faint">Latency</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {q.data.items.map((e) => (
              <TableRow key={e.id} className="border-border hover:bg-hover/50">
                <TableCell className="py-1.5 pl-4 font-mono text-xs text-muted-foreground">{formatTime(e.at)}</TableCell>
                <TableCell className="py-1.5 font-mono text-xs uppercase">{e.op}</TableCell>
                <TableCell className="max-w-[28rem] truncate py-1.5 font-mono text-xs text-muted-foreground" title={[e.key, e.detail].filter(Boolean).join(" · ")}>
                  {e.key || <span className="text-faint">—</span>}
                  {e.detail && <span className="text-faint"> · {e.detail}</span>}
                </TableCell>
                <TableCell className="py-1.5">
                  <StatusPill tone={resultTone(e.result)}>{e.result}</StatusPill>
                </TableCell>
                <TableCell className="py-1.5 pr-4 text-right font-mono text-xs text-muted-foreground">{formatMs(e.elapsed_ms)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Panel>
  );
}
