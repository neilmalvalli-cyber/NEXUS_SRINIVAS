"use client";

// Greyscale charts of numbers the adapter measured (GET /api/metrics). The
// buckets cover only the time this nexus-api process has been running; empty
// buckets are real zeros, and nothing before the process started is drawn.
import { Bar, BarChart, CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { Metrics } from "@/lib/api/schemas";
import { formatMs, formatTime } from "@/lib/format";

const SERIES = [
  { op: "put", color: "#e0e0e0" },
  { op: "get", color: "#a0a0a0" },
  { op: "delete", color: "#707070" },
  { op: "other", color: "#454545" },
] as const;
const MAIN = new Set(["put", "get", "delete"]);

const axis = { stroke: "#707070", fontSize: 10, tickLine: false, axisLine: { stroke: "#303030" } } as const;
const tooltipStyle = {
  contentStyle: { background: "#222222", border: "1px solid #303030", borderRadius: 4, fontSize: 12, color: "#f2f2f2" },
  labelStyle: { color: "#a0a0a0" },
  itemStyle: { color: "#f2f2f2" },
  cursor: { fill: "#ffffff08" },
} as const;

function rows(m: Metrics) {
  return m.buckets.map((b) => {
    const r: Record<string, number | string | null> = { t: formatTime(b.start), put: 0, get: 0, delete: 0, other: 0 };
    let n = 0;
    let ms = 0;
    for (const [op, v] of Object.entries(b.ops)) {
      const k = MAIN.has(op) ? op : "other";
      r[k] = (r[k] as number) + v.count;
      n += v.count;
      ms += v.total_ms;
    }
    r.avg = n > 0 ? ms / n : null;
    return r;
  });
}

export function OpsChart({ metrics }: { metrics: Metrics }) {
  const data = rows(metrics);
  return (
    <div className="h-48 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={data} margin={{ top: 4, right: 4, bottom: 0, left: -18 }}>
          <CartesianGrid stroke="#262626" vertical={false} />
          <XAxis dataKey="t" {...axis} interval="preserveStartEnd" minTickGap={40} />
          <YAxis {...axis} allowDecimals={false} />
          <Tooltip {...tooltipStyle} />
          {SERIES.map((s) => (
            <Bar key={s.op} dataKey={s.op} stackId="ops" fill={s.color} isAnimationActive={false} />
          ))}
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

export function LatencyChart({ metrics }: { metrics: Metrics }) {
  const data = rows(metrics);
  return (
    <div className="h-48 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={data} margin={{ top: 4, right: 4, bottom: 0, left: -10 }}>
          <CartesianGrid stroke="#262626" vertical={false} />
          <XAxis dataKey="t" {...axis} interval="preserveStartEnd" minTickGap={40} />
          <YAxis {...axis} tickFormatter={(v: number) => formatMs(v)} width={56} />
          <Tooltip {...tooltipStyle} formatter={(v) => (typeof v === "number" ? formatMs(v) : "no requests")} />
          <Line type="monotone" dataKey="avg" name="mean latency" stroke="#d0d0d0" strokeWidth={1.5} dot={false} connectNulls={false} isAnimationActive={false} />
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
}

export function ChartLegend() {
  return (
    <div className="flex flex-wrap gap-3 text-[11px] text-muted-foreground">
      {SERIES.map((s) => (
        <span key={s.op} className="flex items-center gap-1.5">
          <span className="size-2 rounded-[2px]" style={{ background: s.color }} />
          {s.op}
        </span>
      ))}
    </div>
  );
}

/** Horizontal bars of a recorded benchmark's median throughput per phase. */
export function BenchChart({ phases }: { phases: { phase: string; ops_per_sec: number }[] }) {
  return (
    <div className="h-56 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={phases} layout="vertical" margin={{ top: 0, right: 16, bottom: 0, left: 0 }}>
          <CartesianGrid stroke="#262626" horizontal={false} />
          <XAxis type="number" scale="log" domain={[1000, "auto"]} {...axis} tickFormatter={(v: number) => (v >= 1e6 ? `${v / 1e6}M` : v >= 1e3 ? `${v / 1e3}k` : String(v))} allowDataOverflow />
          <YAxis type="category" dataKey="phase" {...axis} width={150} />
          <Tooltip {...tooltipStyle} formatter={(v) => (typeof v === "number" ? `${Math.round(v).toLocaleString("en-US")} ops/s` : String(v))} />
          <Bar dataKey="ops_per_sec" name="median ops/s" fill="#a0a0a0" isAnimationActive={false} barSize={12} />
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}
