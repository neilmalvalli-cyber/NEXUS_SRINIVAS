"use client";

import { useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Boxes, Database, FlaskConical, Gauge, GitCompareArrows, HardDrive, LayoutDashboard, LifeBuoy, Menu, Network, Presentation, ScanSearch, ShieldCheck, X, Zap } from "lucide-react";
import { cn } from "@/lib/utils";
import { useNexus } from "@/components/providers";
import { useHealth, useOverview } from "@/lib/queries";
import { API_BASE_URL, isApiError } from "@/lib/api/client";
import { DemoBadge, DisconnectedBanner, StatusDot, type Tone } from "@/components/nexus";
import { formatNumber, relativeTime } from "@/lib/format";

const NAV = [
  { href: "/", label: "Overview", icon: LayoutDashboard },
  { href: "/database", label: "Database Explorer", icon: Database },
  { href: "/storage", label: "Storage Engine", icon: HardDrive },
  { href: "/recovery", label: "Recovery", icon: LifeBuoy },
  { href: "/torture", label: "Torture Lab", icon: FlaskConical },
  { href: "/xray", label: "X-RAY", icon: ScanSearch },
  { href: "/reliability", label: "Reliability Lab", icon: Zap },
  { href: "/benchmark", label: "Benchmark", icon: Gauge },
  { href: "/architecture", label: "Architecture", icon: Network },
  { href: "/comparison", label: "Database Comparison", icon: GitCompareArrows },
  { href: "/judge-readiness", label: "Judge Readiness", icon: ShieldCheck },
] as const;

function Logo() {
  return (
    <div className="flex items-center gap-2.5">
      <div className="grid size-7 place-items-center rounded-sm border border-border bg-elevated">
        <Boxes className="size-4 text-foreground" />
      </div>
      <div className="leading-tight">
        <div className="text-sm font-semibold tracking-[0.18em] text-foreground">NEXUS</div>
        <div className="text-[10px] text-faint">key-value store</div>
      </div>
    </div>
  );
}

function ConnectionStatus() {
  const { mode } = useNexus();
  const health = useHealth();
  const overview = useOverview(false);

  let conn: { tone: Tone; label: string; detail: string };
  if (mode === "demo") conn = { tone: "warn", label: "Offline sample", detail: "Not connected: in-browser sample data" };
  else if (health.isPending) conn = { tone: "unknown", label: "Connecting…", detail: API_BASE_URL };
  else if (health.isError)
    conn = {
      tone: "bad",
      label: "Disconnected",
      detail: isApiError(health.error) && health.error.isDisconnected ? API_BASE_URL : "health check failed",
    };
  else conn = { tone: "ok", label: "Connected", detail: API_BASE_URL };

  // Database health: only what the backend reported. Unknown until it has.
  let db: { tone: Tone; label: string };
  if (mode === "live" && (health.isError || !health.data)) db = { tone: "unknown", label: "Unknown" };
  else if (!health.data?.db_open) db = { tone: "bad", label: "Not open" };
  else {
    const lv = overview.data?.last_verify;
    if (lv && !lv.ok) db = { tone: "bad", label: "Verify failed" };
    else if (lv?.ok) db = { tone: "ok", label: "Open · verified" };
    else db = { tone: "ok", label: "Open" };
  }

  return (
    <div className="space-y-3 border-t border-border px-4 py-4 text-xs">
      <div>
        <div className="mb-1 text-[10px] uppercase tracking-wider text-faint">Backend</div>
        <div className="flex items-center gap-2 text-foreground">
          <StatusDot tone={conn.tone} />
          <span>{conn.label}</span>
        </div>
        <div className="mt-0.5 truncate font-mono text-[11px] text-faint" title={conn.detail}>
          {conn.detail}
        </div>
      </div>
      <div>
        <div className="mb-1 text-[10px] uppercase tracking-wider text-faint">Database</div>
        <div className="flex items-center gap-2 text-foreground">
          <StatusDot tone={db.tone} />
          <span>{db.label}</span>
        </div>
        {overview.data && !(mode === "live" && health.isError) && (
          <div className="mt-0.5 font-mono text-[11px] text-faint">
            {formatNumber(overview.data.stats.keys)} keys · checked {relativeTime(health.data?.measured_at)}
          </div>
        )}
      </div>
    </div>
  );
}

function ModeToggle() {
  const { mode, setMode } = useNexus();
  return (
    <div className="border-t border-border px-4 py-3">
      <div className="mb-1.5 text-[10px] uppercase tracking-wider text-faint">Data source</div>
      <div className="grid grid-cols-2 rounded-sm border border-border bg-background p-0.5 text-xs" role="radiogroup" aria-label="Data source">
        {(["live", "demo"] as const).map((m) => (
          <button
            key={m}
            type="button"
            role="radio"
            aria-checked={mode === m}
            onClick={() => setMode(m)}
            className={cn(
              "rounded-[3px] py-1 capitalize transition-colors",
              mode === m ? "bg-elevated text-foreground" : "text-faint hover:text-muted-foreground",
            )}
          >
            {m === "live" ? "Live API" : "Offline sample"}
          </button>
        ))}
      </div>
    </div>
  );
}

export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const { mode } = useNexus();
  const health = useHealth();
  const disconnected = mode === "live" && health.isError;
  // Below the md breakpoint the sidebar is a drawer opened from a top bar;
  // from md up it is the fixed column it always was.
  const [navOpen, setNavOpen] = useState(false);
  const close = () => setNavOpen(false);

  return (
    <div className="flex h-dvh overflow-hidden">
      {navOpen && <div className="fixed inset-0 z-30 bg-black/60 md:hidden" aria-hidden onClick={close} />}
      <aside
        id="main-nav"
        className={cn(
          "fixed inset-y-0 left-0 z-40 flex w-56 shrink-0 flex-col border-r border-border bg-sidebar transition-transform md:static md:translate-x-0",
          navOpen ? "translate-x-0" : "-translate-x-full",
        )}
      >
        <div className="flex h-14 items-center justify-between border-b border-border px-4">
          <Logo />
          <button type="button" className="rounded-sm p-1 text-muted-foreground hover:text-foreground md:hidden" aria-label="Close navigation" onClick={close}>
            <X className="size-4" />
          </button>
        </div>
        <nav className="flex-1 space-y-0.5 overflow-y-auto px-2 py-3" aria-label="Main">
          {NAV.map(({ href, label, icon: Icon }) => {
            const active = href === "/" ? pathname === "/" : pathname.startsWith(href);
            return (
              <Link
                key={href}
                href={href}
                onClick={close}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "relative flex items-center gap-2.5 rounded-sm px-2.5 py-1.5 text-sm transition-colors",
                  active ? "bg-hover text-foreground" : "text-muted-foreground hover:bg-hover/60 hover:text-foreground",
                )}
              >
                {active && <span aria-hidden className="absolute top-1.5 bottom-1.5 left-0 w-0.5 rounded-full bg-foreground" />}
                <Icon className="size-4" />
                {label}
              </Link>
            );
          })}
          <div className="mt-3 border-t border-border pt-3">
            <Link
              href="/demo"
              onClick={close}
              aria-current={pathname.startsWith("/demo") ? "page" : undefined}
              className={cn(
                "relative flex items-center gap-2.5 rounded-sm px-2.5 py-1.5 text-sm transition-colors",
                pathname.startsWith("/demo") ? "bg-hover text-foreground" : "text-muted-foreground hover:bg-hover/60 hover:text-foreground",
              )}
            >
              {pathname.startsWith("/demo") && <span aria-hidden className="absolute top-1.5 bottom-1.5 left-0 w-0.5 rounded-full bg-foreground" />}
              <Presentation className="size-4" />
              Guided Demo
            </Link>
          </div>
        </nav>
        <ModeToggle />
        <ConnectionStatus />
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex h-12 shrink-0 items-center gap-3 border-b border-border bg-sidebar px-4 md:hidden">
          <button type="button" className="rounded-sm p-1 text-muted-foreground hover:text-foreground" aria-label="Open navigation" aria-expanded={navOpen} aria-controls="main-nav" onClick={() => setNavOpen(true)}>
            <Menu className="size-5" />
          </button>
          <Logo />
        </div>
        {mode === "demo" && (
          <div className="flex items-center gap-3 border-b border-warn/30 bg-warn/5 px-6 py-2 text-xs text-muted-foreground" role="status">
            <DemoBadge />
            Everything on screen comes from an in-browser stand-in, not from NEXUS. Switch the data source to Live API for real results.
          </div>
        )}
        {disconnected && <DisconnectedBanner />}
        <main className="flex-1 overflow-y-auto">
          <div className="mx-auto max-w-7xl px-4 py-4 md:px-6 md:py-6">{children}</div>
        </main>
      </div>
    </div>
  );
}
