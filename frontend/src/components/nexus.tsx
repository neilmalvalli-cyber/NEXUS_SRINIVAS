"use client";

// Reusable building blocks for every NEXUS page: headers, stat cards,
// status pills, error/empty/loading states, copy buttons and confirmations.
import { useState } from "react";
import { AlertTriangle, Check, CircleSlash, Copy, Loader2, RefreshCw, WifiOff } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { API_BASE_URL, describeError, isApiError } from "@/lib/api/client";
import { useNexus } from "@/components/providers";
import { toast } from "sonner";

export function PageHeader({ title, description, actions }: { title: string; description?: React.ReactNode; actions?: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-3 border-b border-border pb-4">
      <div className="min-w-0">
        <h1 className="text-lg font-semibold tracking-tight text-foreground">{title}</h1>
        {description && <p className="mt-1 max-w-3xl text-sm text-muted-foreground [overflow-wrap:anywhere]">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

export function Panel({
  title,
  description,
  actions,
  children,
  className,
  bodyClassName,
}: {
  title?: React.ReactNode;
  description?: React.ReactNode;
  actions?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
  bodyClassName?: string;
}) {
  return (
    <section className={cn("rounded-md border border-border bg-card", className)}>
      {(title || actions) && (
        <header className="flex flex-wrap items-start justify-between gap-2 border-b border-border px-4 py-3">
          <div className="min-w-0">
            {title && <h2 className="text-sm font-medium text-foreground">{title}</h2>}
            {description && <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>}
          </div>
          {actions && <div className="flex items-center gap-2">{actions}</div>}
        </header>
      )}
      <div className={cn("p-4", bodyClassName)}>{children}</div>
    </section>
  );
}

export function StatCard({
  label,
  value,
  hint,
  icon: Icon,
  tone,
}: {
  label: string;
  value: React.ReactNode;
  hint?: React.ReactNode;
  icon?: React.ComponentType<{ className?: string }>;
  tone?: Tone;
}) {
  return (
    <div className="rounded-md border border-border bg-card px-4 py-3">
      <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
        <span>{label}</span>
        {Icon && <Icon className="size-3.5 text-faint" />}
      </div>
      <div className={cn("mt-1.5 truncate font-mono text-xl text-foreground", tone && toneText[tone])}>{value}</div>
      {hint && <div className="mt-1 truncate text-xs text-faint">{hint}</div>}
    </div>
  );
}

export type Tone = "ok" | "bad" | "warn" | "unknown" | "running" | "neutral";
const toneText: Record<Tone, string> = {
  ok: "text-ok",
  bad: "text-bad",
  warn: "text-warn",
  unknown: "text-faint",
  running: "text-foreground",
  neutral: "text-foreground",
};
const toneDot: Record<Tone, string> = {
  ok: "bg-ok",
  bad: "bg-bad",
  warn: "bg-warn",
  unknown: "bg-faint",
  running: "bg-foreground animate-pulse",
  neutral: "bg-muted-foreground",
};

export function StatusDot({ tone, className }: { tone: Tone; className?: string }) {
  return <span aria-hidden className={cn("inline-block size-1.5 shrink-0 rounded-full", toneDot[tone], className)} />;
}

export function StatusPill({ tone, children, className }: { tone: Tone; children: React.ReactNode; className?: string }) {
  return (
    <span
      className={cn(
        "inline-flex h-5 items-center gap-1.5 rounded-sm border border-border bg-elevated px-1.5 font-mono text-[11px] uppercase tracking-wide",
        toneText[tone],
        className,
      )}
    >
      {tone === "running" ? <Loader2 className="size-3 animate-spin" /> : <StatusDot tone={tone} />}
      {children}
    </span>
  );
}

export function DemoBadge({ className }: { className?: string }) {
  return (
    <span
      className={cn(
        "inline-flex h-5 items-center rounded-sm border border-warn/50 px-1.5 font-mono text-[10px] font-semibold tracking-widest text-warn",
        className,
      )}
    >
      SAMPLE DATA
    </span>
  );
}

/** Shown wherever numbers come from a recorded file rather than a live call. */
export function RecordedBadge({ when }: { when?: string }) {
  return (
    <span className="inline-flex h-5 items-center rounded-sm border border-border px-1.5 font-mono text-[10px] tracking-widest text-muted-foreground" title={when}>
      RECORDED
    </span>
  );
}

export function KV({ rows, className }: { rows: [React.ReactNode, React.ReactNode][]; className?: string }) {
  return (
    <dl className={cn("grid grid-cols-[minmax(6rem,auto)_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-sm", className)}>
      {rows.map(([k, v], i) => (
        <div key={i} className="contents">
          <dt className="text-muted-foreground">{k}</dt>
          <dd className="min-w-0 break-words font-mono text-[13px] text-foreground">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

export function EmptyState({ icon: Icon = CircleSlash, title, children }: { icon?: React.ComponentType<{ className?: string }>; title: string; children?: React.ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 px-4 py-10 text-center">
      <Icon className="size-5 text-faint" />
      <div className="text-sm text-foreground">{title}</div>
      {children && <div className="max-w-md text-xs text-muted-foreground">{children}</div>}
    </div>
  );
}

export function LoadingBlock({ rows = 3, className }: { rows?: number; className?: string }) {
  return (
    <div className={cn("space-y-2", className)} aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-4 bg-elevated" style={{ width: `${90 - i * 12}%` }} />
      ))}
    </div>
  );
}

/** Error panel that tells "backend unreachable" apart from "backend answered with an error". */
export function ErrorState({ error, onRetry, compact }: { error: unknown; onRetry?: () => void; compact?: boolean }) {
  const disconnected = isApiError(error) && error.isDisconnected;
  const demoOnly = isApiError(error) && error.kind === "unavailable-in-demo";
  const Icon = disconnected ? WifiOff : AlertTriangle;
  return (
    <div className={cn("flex items-start gap-3 rounded-md border border-border bg-elevated/50 px-3", compact ? "py-2" : "py-3")} role="alert">
      <Icon className={cn("mt-0.5 size-4 shrink-0", demoOnly ? "text-faint" : "text-bad")} />
      <div className="min-w-0 flex-1 text-sm">
        <div className="text-foreground">{demoOnly ? "Not available in demo mode" : disconnected ? "Backend disconnected" : "Request failed"}</div>
        <div className="mt-0.5 break-words text-xs text-muted-foreground">{describeError(error)}</div>
      </div>
      {onRetry && !demoOnly && (
        <Button size="xs" variant="outline" onClick={onRetry}>
          <RefreshCw /> Retry
        </Button>
      )}
    </div>
  );
}

export function CopyButton({ text, label = "Copy", className }: { text: string; label?: string; className?: string }) {
  const [done, setDone] = useState(false);
  return (
    <Button
      size="xs"
      variant="outline"
      className={className}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setDone(true);
          setTimeout(() => setDone(false), 1200);
        } catch {
          toast.error("Clipboard is not available in this browser context");
        }
      }}
    >
      {done ? <Check /> : <Copy />}
      {done ? "Copied" : label}
    </Button>
  );
}

/** A confirmation dialog for destructive or long-running actions. */
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  destructive,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  title: string;
  description: React.ReactNode;
  confirmLabel: string;
  destructive?: boolean;
  onConfirm: () => void;
}) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent className="bg-elevated ring-border">
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription render={<div />}>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter className="bg-transparent">
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <Button
            variant={destructive ? "destructive" : "default"}
            onClick={() => {
              onOpenChange(false);
              onConfirm();
            }}
          >
            {confirmLabel}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

/** Banner for live mode when the adapter is unreachable. */
export function DisconnectedBanner() {
  const { setMode } = useNexus();
  return (
    <div className="flex flex-wrap items-center gap-3 border-b border-bad/30 bg-bad/5 px-6 py-2.5 text-sm" role="status">
      <WifiOff className="size-4 text-bad" />
      <span className="text-foreground">Backend disconnected.</span>
      <span className="text-muted-foreground">
        Cannot reach nexus-api at <code className="font-mono text-xs">{API_BASE_URL}</code>. Values on screen may be out of date; actions are disabled until it answers.
      </span>
      <Button size="xs" variant="ghost" className="ml-auto text-muted-foreground" onClick={() => setMode("demo")}>
        Use offline sample data
      </Button>
    </div>
  );
}

export function Mono({ children, className }: { children: React.ReactNode; className?: string }) {
  return <span className={cn("font-mono text-[13px]", className)}>{children}</span>;
}
