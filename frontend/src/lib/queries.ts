"use client";

// Shared TanStack Query hooks. Every key starts with the mode ("live" or
// "demo") so demo data can never be served from, or into, the live cache.
// Polling is limited to the health check (sidebar), the overview while it is
// on screen, and a torture run while it is running.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNexus } from "@/components/providers";

export const HEALTH_POLL_MS = 5_000;

export function useHealth() {
  const { api } = useNexus();
  return useQuery({
    queryKey: [api.mode, "health"],
    queryFn: api.health,
    refetchInterval: HEALTH_POLL_MS,
    refetchIntervalInBackground: false,
    retry: false,
  });
}

export function useOverview(poll = true) {
  const { api } = useNexus();
  return useQuery({ queryKey: [api.mode, "overview"], queryFn: api.overview, refetchInterval: poll ? 10_000 : false });
}

export function useStorage() {
  const { api } = useNexus();
  return useQuery({ queryKey: [api.mode, "storage"], queryFn: api.storage });
}

/** Invalidate everything that depends on the database contents. */
export function useInvalidateDb() {
  const qc = useQueryClient();
  const { api } = useNexus();
  return () =>
    Promise.all(
      ["overview", "storage", "keys", "activity", "metrics", "recovery", "frames", "databases"].map((k) =>
        qc.invalidateQueries({ queryKey: [api.mode, k] }),
      ),
    );
}

/**
 * Whether actions may be sent: in live mode only after the adapter answered
 * the last health check; in demo mode always (they go to the demo store).
 */
export function useCanAct(): { ready: boolean; reason?: string } {
  const { mode } = useNexus();
  const health = useHealth();
  if (mode === "demo") return { ready: true };
  if (health.isSuccess && !health.isError) return { ready: true };
  if (health.isPending) return { ready: false, reason: "Checking the backend connection…" };
  return { ready: false, reason: "The backend is disconnected" };
}
