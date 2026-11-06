"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { useNexus } from "@/components/providers";
import { useInvalidateDb } from "@/lib/queries";
import { toastError } from "@/lib/mutations";
import { formatBytes, formatMs } from "@/lib/format";

/** POST /api/compact; the result toast reports the sizes the backend measured. */
export function useCompact() {
  const { api } = useNexus();
  const invalidate = useInvalidateDb();
  return useMutation({
    mutationFn: () => api.compact(),
    onSuccess: (r) => {
      toast.success("Compaction finished", {
        description: `${formatBytes(r.size_before)} → ${formatBytes(r.size_after)}, ${r.keys.toLocaleString("en-US")} keys, ${formatMs(r.elapsed_ms)}`,
      });
    },
    onError: (err) => toastError("Compaction", err),
    onSettled: () => invalidate(),
  });
}

/** POST /api/verify (kvdb.Verify on the open database). A failed check is a result, not a transport error. */
export function useVerify() {
  const { api } = useNexus();
  const qc = useQueryClient();
  const invalidate = useInvalidateDb();
  return useMutation({
    mutationFn: () => api.verify(),
    onSuccess: (r) => {
      if (r.ok) toast.success("Inspection passed", { description: `${r.frames.toLocaleString("en-US")} frames, ${r.keys.toLocaleString("en-US")} keys checked in ${formatMs(r.elapsed_ms)}` });
      else toast.error("Inspection FAILED", { description: r.error ? `${r.error.class}: ${r.error.message}` : "the backend reported a failed check" });
    },
    onError: (err) => toastError("Inspection", err),
    onSettled: () => {
      invalidate();
      qc.invalidateQueries({ queryKey: [api.mode, "health"] });
    },
  });
}
