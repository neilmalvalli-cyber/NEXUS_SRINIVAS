// Low-level HTTP client for nexus-api: base URL from the environment, a
// timeout on every request, and one error type that tells the UI exactly
// what went wrong (network down, timeout, an engine error class, or a
// response that did not match the schema).
import type { z } from "zod";
import { ErrorEnvelope } from "./schemas";

export const API_BASE_URL = (process.env.NEXT_PUBLIC_NEXUS_API_URL || "http://127.0.0.1:8080").replace(/\/+$/, "");

export type ApiErrorKind = "network" | "timeout" | "http" | "malformed" | "unavailable-in-demo";

export class ApiError extends Error {
  readonly kind: ApiErrorKind;
  readonly status?: number;
  /** Engine/adapter error class: invalid, too-large, notfound, full, locked, closed, busy, ... */
  readonly errorClass?: string;
  /** A write whose outcome is unknown (I/O error mid-write): it may or may not be durable. */
  readonly outcomeUnknown: boolean;

  constructor(
    kind: ApiErrorKind,
    message: string,
    opts: { status?: number; errorClass?: string; outcomeUnknown?: boolean } = {},
  ) {
    super(message);
    this.name = "ApiError";
    this.kind = kind;
    this.status = opts.status;
    this.errorClass = opts.errorClass;
    this.outcomeUnknown = opts.outcomeUnknown ?? false;
  }

  get isNotFound() {
    return this.kind === "http" && this.errorClass === "notfound";
  }
  /** The adapter could not be reached at all (as opposed to answering with an error). */
  get isDisconnected() {
    return this.kind === "network" || this.kind === "timeout";
  }
}

export function isApiError(e: unknown): e is ApiError {
  return e instanceof ApiError;
}

/** One-line description for toasts and error panels. */
export function describeError(e: unknown): string {
  if (!isApiError(e)) return e instanceof Error ? e.message : String(e);
  switch (e.kind) {
    case "network":
      return `Cannot reach nexus-api at ${API_BASE_URL}`;
    case "timeout":
      return `nexus-api did not answer in time (${e.message})`;
    case "malformed":
      return `Unexpected response from nexus-api: ${e.message}`;
    case "unavailable-in-demo":
      return e.message;
    case "http":
      return `${e.errorClass ?? `HTTP ${e.status}`}: ${e.message}${e.outcomeUnknown ? " (outcome unknown)" : ""}`;
  }
}

export interface RequestOptions<S extends z.ZodType> {
  method?: "GET" | "POST" | "PUT" | "DELETE";
  body?: unknown;
  schema: S;
  timeoutMs?: number;
  signal?: AbortSignal;
}

export const DEFAULT_TIMEOUT_MS = 10_000;

export async function request<S extends z.ZodType>(path: string, opts: RequestOptions<S>): Promise<z.infer<S>> {
  const timeoutMs = opts.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const ctrl = new AbortController();
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    ctrl.abort();
  }, timeoutMs);
  const onAbort = () => ctrl.abort();
  opts.signal?.addEventListener("abort", onAbort);

  let res: Response;
  try {
    res = await fetch(API_BASE_URL + path, {
      method: opts.method ?? "GET",
      headers: opts.body !== undefined ? { "Content-Type": "application/json" } : undefined,
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
      signal: ctrl.signal,
      cache: "no-store",
    });
  } catch (err) {
    if (timedOut) throw new ApiError("timeout", `${timeoutMs / 1000}s`);
    if (opts.signal?.aborted) throw err; // cancelled by TanStack Query; not an error to show
    throw new ApiError("network", err instanceof Error ? err.message : "fetch failed");
  } finally {
    clearTimeout(timer);
    opts.signal?.removeEventListener("abort", onAbort);
  }

  let text: string;
  try {
    text = await res.text();
  } catch {
    throw new ApiError("network", "connection closed while reading the response");
  }
  let json: unknown;
  try {
    json = text === "" ? undefined : JSON.parse(text);
  } catch {
    throw new ApiError("malformed", `HTTP ${res.status}: body is not JSON`, { status: res.status });
  }

  if (!res.ok) {
    const env = ErrorEnvelope.safeParse(json);
    if (env.success) {
      throw new ApiError("http", env.data.error.message, {
        status: res.status,
        errorClass: env.data.error.class,
        outcomeUnknown: env.data.error.outcome_unknown,
      });
    }
    throw new ApiError("http", `HTTP ${res.status} without an error body`, { status: res.status });
  }

  const parsed = opts.schema.safeParse(json);
  if (!parsed.success) {
    const issue = parsed.error.issues[0];
    throw new ApiError("malformed", `${path}: ${issue ? `${issue.path.join(".") || "(root)"} ${issue.message}` : "schema mismatch"}`, {
      status: res.status,
    });
  }
  return parsed.data;
}

/** Path segment for a key: every byte escaped, so "/", "..", "?" and "#" stay inside the key. */
export function keyPath(key: string): string {
  return encodeURIComponent(key);
}
