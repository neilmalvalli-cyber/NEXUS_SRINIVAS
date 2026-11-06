"use client";

import { toast } from "sonner";
import { describeError, isApiError } from "@/lib/api/client";

/** Toast for a failed action, with the right wording for each failure kind. */
export function toastError(action: string, err: unknown) {
  if (isApiError(err) && err.outcomeUnknown) {
    toast.warning(`${action}: outcome unknown`, {
      description: `${describeError(err)}. The write may or may not be durable; read the key back to find out.`,
    });
    return;
  }
  if (isApiError(err) && err.isNotFound) {
    toast.info(`${action}: key not found`);
    return;
  }
  toast.error(`${action} failed`, { description: describeError(err) });
}
