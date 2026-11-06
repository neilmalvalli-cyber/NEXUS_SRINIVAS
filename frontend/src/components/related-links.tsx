"use client";

// A row of cross-links between the reliability pages (Recovery, Torture
// Lab, X-RAY, Reliability Lab). Each card says whether it concerns the live
// database or a disposable test environment.
import Link from "next/link";
import { ArrowRight } from "lucide-react";

export interface RelatedLink {
  href: string;
  title: string;
  text: string;
  scope: "live" | "test";
}

export function RelatedLinks({ links }: { links: RelatedLink[] }) {
  return (
    <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
      {links.map((l) => (
        <Link key={l.href + l.title} href={l.href} className="group rounded-md border border-border bg-card p-3 hover:border-foreground/30">
          <div className="flex items-center justify-between gap-2">
            <span className="text-sm text-foreground">{l.title}</span>
            <span className="font-mono text-[10px] tracking-widest text-faint">{l.scope === "live" ? "LIVE DATABASE" : "TEST ENVIRONMENT"}</span>
          </div>
          <p className="mt-1 text-xs text-muted-foreground">{l.text}</p>
          <span className="mt-2 inline-flex items-center gap-1 text-[11px] text-faint group-hover:text-foreground">
            Open <ArrowRight className="size-3" />
          </span>
        </Link>
      ))}
    </div>
  );
}
