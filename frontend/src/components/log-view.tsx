"use client";

// Visualisations of a real NEXUS data file, drawn from GET /api/storage/frames
// (or the crash-demo equivalent): the growing log as a strip of frames, a
// frame inspector with the decoded header and entries, the index→log map,
// and the binary format reference. Nothing here is computed from guesses:
// every number comes from the engine's own decoders on the server.
import { useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import type { LogFrame, LogMap } from "@/lib/api/schemas";
import { FORMAT } from "@/lib/format-spec";
import { formatNumber } from "@/lib/format";
import { cn } from "@/lib/utils";
import { KV, StatusPill, type Tone } from "@/components/nexus";

const hex = (n: number, w = 8) => "0x" + (n >>> 0).toString(16).toUpperCase().padStart(w, "0");

function frameKind(f: LogFrame): "put" | "del" | "batch" | "snapshot" {
  if (f.in_snapshot) return "snapshot";
  if (f.op_count > 1) return "batch";
  return f.entries[0]?.op === "DEL" ? "del" : "put";
}

const KIND_STYLE: Record<ReturnType<typeof frameKind>, string> = {
  put: "bg-[#d8d8d8]",
  del: "bg-[#6a6a6a]",
  batch: "bg-[#a8a8a8]",
  snapshot: "bg-[repeating-linear-gradient(135deg,#505050_0_3px,#3a3a3a_3px_6px)]",
};

export const STOP_INFO: Record<string, { tone: Tone; label: string; meaning: string }> = {
  clean: { tone: "ok", label: "clean end", meaning: "The file ends exactly after the last valid frame. Nothing to repair." },
  damaged: {
    tone: "warn",
    label: "damaged tail",
    meaning:
      "Bytes after the last valid frame fail a checksum or end early: the shape a write interrupted by a crash leaves. Recovery truncates it only if it can prove it is the final frame; this map does not decide that.",
  },
  corrupt: { tone: "bad", label: "corruption", meaning: "Checksums pass but the structure is invalid. A torn write cannot produce this; recovery refuses to open the file and changes nothing." },
  sequence: { tone: "bad", label: "sequence break", meaning: "A valid frame carries the wrong sequence number. Recovery treats this as corruption." },
  io: { tone: "bad", label: "read error", meaning: "The file could not be read." },
  limit: { tone: "unknown", label: "scan limit", meaning: "The display scan stopped at its size limit; the rest of the file was not read here." },
};

export function LogLegend() {
  return (
    <div className="flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
      <span className="flex items-center gap-1.5"><span className="h-2.5 w-4 rounded-[2px] bg-[#3a3a3a] ring-1 ring-border" /> superblock (32 B)</span>
      <span className="flex items-center gap-1.5"><span className={cn("h-2.5 w-4 rounded-[2px]", KIND_STYLE.put)} /> PUT frame</span>
      <span className="flex items-center gap-1.5"><span className={cn("h-2.5 w-4 rounded-[2px]", KIND_STYLE.del)} /> DEL frame</span>
      <span className="flex items-center gap-1.5"><span className={cn("h-2.5 w-4 rounded-[2px]", KIND_STYLE.batch)} /> BATCH frame</span>
      <span className="flex items-center gap-1.5"><span className={cn("h-2.5 w-4 rounded-[2px]", KIND_STYLE.snapshot)} /> compaction snapshot</span>
      <span className="flex items-center gap-1.5"><span className="h-2.5 w-4 rounded-[2px] bg-bad/70" /> bytes after the valid end</span>
    </div>
  );
}

/** The log as a strip: one block per frame, width by size (with a floor so small frames stay visible). */
export function LogStrip({ map, selected, onSelect }: { map: LogMap; selected?: number; onSelect?: (seq: number) => void }) {
  const tail = map.stop.kind === "clean" ? 0 : map.stop.bytes_after;
  const items = map.frames;
  const shown = items.reduce((n, f) => n + f.size, 0) + 32 + tail;
  const w = (bytes: number) => Math.max(0.8, (bytes / Math.max(shown, 1)) * 100);
  return (
    <div className="space-y-2">
      <div className="flex h-10 w-full items-stretch gap-px overflow-hidden rounded-sm border border-border bg-background p-px" role="list" aria-label="Frames in the log">
        <div className="shrink-0 rounded-[2px] bg-[#3a3a3a]" style={{ width: `${w(32)}%` }} title="superblock, 32 bytes at offset 0" role="listitem" />
        {map.first_listed_frame > 0 && (
          <div className="grid shrink-0 place-items-center rounded-[2px] bg-elevated px-1 font-mono text-[9px] text-faint" title={`${map.first_listed_frame} earlier frames not listed`} role="listitem">
            +{formatNumber(map.first_listed_frame)}
          </div>
        )}
        {items.map((f) => (
          <button
            key={f.seq}
            type="button"
            role="listitem"
            onClick={() => onSelect?.(f.seq)}
            title={`frame seq ${f.seq} · offset ${f.offset} · ${f.size} B · ${f.op_count} op(s)`}
            className={cn(
              "shrink-0 rounded-[2px] transition-[opacity,outline] duration-150 hover:opacity-80",
              KIND_STYLE[frameKind(f)],
              selected === f.seq && "outline outline-2 outline-offset-1 outline-foreground",
            )}
            style={{ width: `${w(f.size)}%` }}
          />
        ))}
        {tail > 0 && <div className="shrink-0 rounded-[2px] bg-bad/70" style={{ width: `${w(tail)}%` }} title={`${tail} bytes after the last valid frame`} role="listitem" />}
      </div>
      <div className="flex flex-wrap justify-between gap-2 font-mono text-[11px] text-faint">
        <span>offset 0</span>
        <span>
          {formatNumber(map.total_frames)} frames · {formatNumber(map.total_entries)} entries · last seq {map.last_seq || "—"}
        </span>
        <span>
          valid end {formatNumber(map.valid_end)} / file {formatNumber(map.file_size)} B
        </span>
      </div>
    </div>
  );
}

export function StopBanner({ map }: { map: LogMap }) {
  const info = STOP_INFO[map.stop.kind] ?? STOP_INFO.io;
  return (
    <div className="flex flex-wrap items-start gap-3 rounded-sm border border-border bg-background px-3 py-2 text-xs">
      <StatusPill tone={info.tone}>{info.label}</StatusPill>
      <div className="min-w-0 flex-1 text-muted-foreground">
        <div className="text-foreground">{map.stop.message}</div>
        <div className="mt-0.5">
          {info.meaning} {map.stop.kind !== "clean" && `At offset ${formatNumber(map.stop.offset)}, ${formatNumber(map.stop.bytes_after)} bytes to the end of the file.`}
        </div>
      </div>
    </div>
  );
}

export function FrameInspector({ frame }: { frame: LogFrame }) {
  return (
    <div className="space-y-3">
      <div className="grid gap-3 md:grid-cols-2">
        <KV
          rows={[
            ["Offset", `${formatNumber(frame.offset)} (${hex(frame.offset, 6)})`],
            ["Frame size", `${formatNumber(frame.size)} B = 32 header + ${formatNumber(frame.payload_len)} payload`],
            ["seq", formatNumber(frame.seq)],
            ["op_count", frame.op_count],
            ["payload_crc", hex(frame.payload_crc)],
            ["Region", frame.in_snapshot ? "compaction snapshot (sealed)" : "appended after open/compaction"],
          ]}
        />
        <div className="space-y-1 text-xs text-muted-foreground">
          <div className="text-[11px] uppercase tracking-wider text-faint">Header bytes</div>
          <div className="grid grid-cols-8 gap-px overflow-hidden rounded-sm border border-border font-mono text-[10px]">
            {[
              ["magic", 4],
              ["t", 1],
              ["r", 1],
              ["ops", 2],
              ["seq", 8],
              ["payload_len", 8],
              ["payload_crc", 4],
              ["header_crc", 4],
            ].map(([n, s]) => (
              <div key={n as string} className="truncate bg-elevated px-1 py-1 text-center text-muted-foreground" style={{ gridColumn: `span ${Math.max(1, Math.round((s as number) / 4))}` }} title={`${n}: ${s} bytes`}>
                {n} <span className="text-faint">{s}</span>
              </div>
            ))}
          </div>
          <p>Magic, type and header CRC were checked by the server-side decoder; this frame passed all of them and its payload CRC.</p>
        </div>
      </div>
      <div className="overflow-x-auto rounded-sm border border-border">
        <table className="w-full text-left font-mono text-[11px]">
          <thead className="bg-elevated text-faint">
            <tr>
              <th className="px-2 py-1 font-normal">op</th>
              <th className="px-2 py-1 font-normal">entry offset</th>
              <th className="px-2 py-1 font-normal">size</th>
              <th className="px-2 py-1 font-normal">key_len</th>
              <th className="px-2 py-1 font-normal">val_len</th>
              <th className="px-2 py-1 font-normal">key</th>
              <th className="px-2 py-1 font-normal">value</th>
              <th className="px-2 py-1 font-normal">entry_crc</th>
            </tr>
          </thead>
          <tbody>
            {frame.entries.map((e) => (
              <tr key={e.offset} className="border-t border-border text-muted-foreground">
                <td className={cn("px-2 py-1", e.op === "DEL" ? "text-faint" : "text-foreground")}>{e.op}</td>
                <td className="px-2 py-1">{formatNumber(e.offset)}</td>
                <td className="px-2 py-1">{e.size}</td>
                <td className="px-2 py-1">{e.key_len}</td>
                <td className="px-2 py-1">{e.value_len}</td>
                <td className="max-w-40 truncate px-2 py-1 text-foreground">{e.key.encoding === "base64" ? `b64:${e.key.data}` : e.key.data}</td>
                <td className="max-w-48 truncate px-2 py-1">{e.op === "DEL" ? "—" : e.value_preview.encoding === "base64" ? "binary" : e.value_preview.data}{e.value_truncated && "…"}</td>
                <td className="px-2 py-1">{hex(e.entry_crc)}</td>
              </tr>
            ))}
            {frame.more_entries > 0 && (
              <tr className="border-t border-border text-faint">
                <td colSpan={8} className="px-2 py-1">… {formatNumber(frame.more_entries)} more entries in this frame</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

/** key → offset map the index holds, as derived from the scan. */
export function IndexMap({ map }: { map: LogMap }) {
  const rows = map.index_sample ?? [];
  if (rows.length === 0) return <p className="text-xs text-faint">No live keys: the index is empty.</p>;
  return (
    <div className="space-y-2">
      <div className="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-center gap-x-3 gap-y-1 font-mono text-[11px]">
        <div className="text-[10px] uppercase tracking-wider text-faint">in-memory index · key</div>
        <div />
        <div className="text-[10px] uppercase tracking-wider text-faint">log · latest entry</div>
        {rows.slice(0, 14).map((r) => (
          <div key={r.key.data} className="contents">
            <div className="truncate rounded-sm border border-border bg-elevated px-2 py-0.5 text-foreground" title={r.key.data}>{r.key.data}</div>
            <div className="text-faint">→</div>
            <div className="truncate text-muted-foreground">
              @{formatNumber(r.entry_offset)} · {r.entry_size} B · frame {r.frame_seq}
            </div>
          </div>
        ))}
      </div>
      <p className="text-[11px] text-faint">
        {formatNumber(map.index_keys)} live keys. Older versions of a key and deleted keys are still bytes in the log, but no index entry points at them; compaction removes them. Offsets here are derived by replaying the scanned frames the way recovery does.
      </p>
    </div>
  );
}

export function FormatReference({ highlight }: { highlight?: string }) {
  const [open, setOpen] = useState<string | null>(highlight ?? "frame");
  return (
    <div className="space-y-2">
      {FORMAT.map((s) => {
        const isOpen = open === s.id;
        return (
          <div key={s.id} className="rounded-sm border border-border">
            <button type="button" className="flex w-full flex-wrap items-center gap-2 px-3 py-2 text-left text-sm hover:bg-hover/50" onClick={() => setOpen(isOpen ? null : s.id)} aria-expanded={isOpen}>
              {isOpen ? <ChevronDown className="size-3.5 text-faint" /> : <ChevronRight className="size-3.5 text-faint" />}
              <span className="text-foreground">{s.title}</span>
              <span className="font-mono text-xs text-faint">{s.size}</span>
              <span className="ml-auto break-all font-mono text-[11px] text-faint">{s.source}</span>
            </button>
            {isOpen && (
              <div className="space-y-3 border-t border-border px-3 py-3">
                <p className="text-xs text-muted-foreground">{s.summary}</p>
                <div className="flex w-full overflow-hidden rounded-sm border border-border font-mono text-[10px]">
                  {s.fields.map((f) => (
                    <div key={f.name} className="min-w-0 border-r border-border bg-elevated px-1 py-1.5 text-center last:border-r-0" style={{ flex: f.size > 0 ? f.size : 6 }} title={`${f.name}: ${f.size > 0 ? f.size + " bytes" : "variable"}`}>
                      <div className="truncate text-foreground">{f.name}</div>
                      <div className="text-faint">{f.size > 0 ? `${f.size} B` : "var"}</div>
                    </div>
                  ))}
                </div>
                <div className="overflow-x-auto"><table className="w-full text-left text-xs">
                  <thead className="text-faint">
                    <tr>
                      <th className="py-1 pr-3 font-normal">offset</th>
                      <th className="py-1 pr-3 font-normal">size</th>
                      <th className="py-1 pr-3 font-normal">field</th>
                      <th className="py-1 pr-3 font-normal">value</th>
                      <th className="py-1 font-normal">meaning</th>
                    </tr>
                  </thead>
                  <tbody>
                    {s.fields.map((f) => (
                      <tr key={f.name} className="border-t border-border align-top">
                        <td className="py-1.5 pr-3 font-mono text-muted-foreground">{f.offset >= 0 ? f.offset : "—"}</td>
                        <td className="py-1.5 pr-3 font-mono text-muted-foreground">{f.size > 0 ? f.size : "var"}</td>
                        <td className="py-1.5 pr-3 font-mono text-foreground">{f.name}</td>
                        <td className="py-1.5 pr-3 font-mono text-muted-foreground">{f.value}</td>
                        <td className="py-1.5 text-muted-foreground">{f.detail}</td>
                      </tr>
                    ))}
                  </tbody>
                </table></div>
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

export function frameBySeq(map: LogMap | undefined, seq: number | undefined) {
  if (!map) return undefined;
  return map.frames.find((f) => f.seq === seq) ?? map.frames[map.frames.length - 1];
}
