"use client";

import { useQuery } from "@tanstack/react-query";
import { useNexus } from "@/components/providers";
import { useHealth } from "@/lib/queries";
import { EmptyState, ErrorState, LoadingBlock, PageHeader } from "@/components/nexus";
import { CrashDrill, MainDatabase, RecoveryProcess } from "@/components/recovery-view";
import { RelatedLinks } from "@/components/related-links";

export default function RecoveryPage() {
  const { api, mode } = useNexus();
  const health = useHealth();
  const q = useQuery({ queryKey: [api.mode, "recovery"], queryFn: api.recovery });
  return (
    <div className="space-y-6">
      <PageHeader
        title="Recovery"
        description="How NEXUS comes back after a crash, and experiments you can run now. Destructive steps run only on a separate disposable demo database. Pass and fail are shown only when the backend reported them; anything not yet checked is unknown."
      />
      {mode === "live" && (
        <RelatedLinks
          links={[
            { href: "/xray?target=main", title: "X-RAY the main database", text: "Read-only structured diagnosis: what recovery would do on the next open, where, and whether data is at risk.", scope: "live" },
            { href: "/xray?target=demo", title: "X-RAY the crash-demo copy", text: "Diagnose the drill database below after a crash or tear, and repair it with a backup and verification.", scope: "test" },
            { href: "/reliability", title: "Power-Cut Explorer", text: "Modeled power-loss disk images from the real write/fsync trace, each opened with this recovery.", scope: "test" },
          ]}
        />
      )}
      {mode === "demo" ? (
        <EmptyState title="Recovery is not simulated in demo mode">Switch the data source to Live API. Showing a recovery result that did not happen would be misleading.</EmptyState>
      ) : q.isPending ? (
        <LoadingBlock rows={8} />
      ) : q.isError ? (
        <ErrorState error={q.error} onRetry={() => q.refetch()} />
      ) : (
        <>
          <RecoveryProcess lo={q.data.last_open} title="Recovery process (kvdb.Open)" description="What happens every time NEXUS opens a file, including after a crash. Each step shows what the open of the main database reported when nexus-api started or switched databases." />
          <MainDatabase data={q.data} />
          <CrashDrill demo={q.data.demo} />
          {health.data && !health.data.tools.kvdb && <p className="text-xs text-faint">kvdb executable: not found by nexus-api.</p>}
        </>
      )}
    </div>
  );
}
