"use client";

import { createContext, useContext, useMemo, useSyncExternalStore } from "react";
import { isServer, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Toaster } from "@/components/ui/sonner";
import { isApiError } from "@/lib/api/client";
import { liveService, type NexusService } from "@/lib/api/service";
import { demoService } from "@/lib/api/demo";

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 5_000,
        refetchOnWindowFocus: true,
        // Do not hammer a backend that is down or answered with an engine error.
        retry: (count, err) => count < 1 && isApiError(err) && err.kind === "timeout",
      },
      mutations: { retry: false },
    },
  });
}

let browserQueryClient: QueryClient | undefined;
function getQueryClient() {
  if (isServer) return makeQueryClient();
  return (browserQueryClient ??= makeQueryClient());
}

type Mode = "live" | "demo";
interface ModeCtx {
  mode: Mode;
  api: NexusService;
  setMode: (m: Mode) => void;
}
const ModeContext = createContext<ModeCtx | null>(null);
const STORAGE_KEY = "nexus.mode";

/** The service for the current mode. Query keys must start with `api.mode`. */
export function useNexus(): ModeCtx {
  const ctx = useContext(ModeContext);
  if (!ctx) throw new Error("useNexus outside <Providers>");
  return ctx;
}

// The selected mode lives in localStorage (per-viewer convenience) with an
// in-memory fallback when storage is blocked.
const modeStore = (() => {
  let memory: Mode = "live";
  const listeners = new Set<() => void>();
  const read = (): Mode => {
    try {
      return window.localStorage.getItem(STORAGE_KEY) === "demo" ? "demo" : "live";
    } catch {
      return memory;
    }
  };
  return {
    get: read,
    set: (m: Mode) => {
      memory = m;
      try {
        window.localStorage.setItem(STORAGE_KEY, m);
      } catch {
        /* storage blocked: memory only */
      }
      listeners.forEach((l) => l());
    },
    subscribe: (l: () => void) => {
      listeners.add(l);
      const onStorage = (e: StorageEvent) => e.key === STORAGE_KEY && l();
      window.addEventListener("storage", onStorage);
      return () => {
        listeners.delete(l);
        window.removeEventListener("storage", onStorage);
      };
    },
  };
})();

export function Providers({ children }: { children: React.ReactNode }) {
  const queryClient = getQueryClient();
  // Live is the default (and always on the server). Demo mode is only ever
  // entered by the user's toggle; the choice is remembered per browser.
  const mode = useSyncExternalStore(modeStore.subscribe, modeStore.get, () => "live" as Mode);
  const setMode = modeStore.set;

  const value = useMemo<ModeCtx>(() => ({ mode, setMode, api: mode === "demo" ? demoService : liveService }), [mode, setMode]);

  return (
    <QueryClientProvider client={queryClient}>
      <ModeContext.Provider value={value}>
        <TooltipProvider delay={300}>
          {children}
          <Toaster position="bottom-right" closeButton />
        </TooltipProvider>
      </ModeContext.Provider>
    </QueryClientProvider>
  );
}
