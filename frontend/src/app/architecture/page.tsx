"use client";

// Architecture of NEXUS as built (docs/DESIGN.md, docs/ARCHITECTURE.md, the
// package doc comments under src/internal). Every box is a package or program
// that exists in this repository; the flows follow the code paths in
// src/kvdb, src/internal/storage and src/internal/recovery. Static content:
// no backend calls.
import { useEffect, useState } from "react";
import { ChevronLeft, ChevronRight, Pause, Play } from "lucide-react";
import { Button } from "@/components/ui/button";
import { PageHeader, Panel } from "@/components/nexus";
import { cn } from "@/lib/utils";

type Comp = {
  id: string;
  name: string;
  layer: number;
  sub: string;
  responsibility: string;
  inputs: string;
  outputs: string;
  interacts: string[];
  files: string[];
};

const COMPONENTS: Comp[] = [
  {
    id: "cli", name: "kvdb CLI", layer: 0, sub: "open · put · get · del · batch · dump · check · serve",
    responsibility: "The command-line program. Each command opens the database (which runs recovery), does its work and closes it; serve keeps it open and reads commands from stdin.",
    inputs: "Command-line arguments, stdin (serve and batch).", outputs: "OK / value / NOTFOUND lines and exit codes per error class.",
    interacts: ["api"], files: ["src/cmd/kvdb/main.go", "src/internal/cli/cli.go", "src/internal/cli/parse.go", "src/internal/cli/serve.go"],
  },
  {
    id: "http", name: "nexus-api", layer: 0, sub: "HTTP adapter for this dashboard",
    responsibility: "Holds one open database and exposes the library over local HTTP/JSON. Adds validation, activity history, a disposable recovery demo and safe process management for the test tools. It does not change engine behaviour.",
    inputs: "HTTP requests from the dashboard (localhost).", outputs: "JSON responses; X-RAY reports for fixed targets (never paths); child processes nexus-torture, kvdb-bench, kvdb-mutant, kvdb-perf and nexus-powercut with validated arguments, one at a time.",
    interacts: ["api", "torture", "checks", "xray", "powercut"], files: ["src/cmd/nexus-api/server.go", "src/cmd/nexus-api/db.go", "src/cmd/nexus-api/dbs.go", "src/cmd/nexus-api/demo.go", "src/cmd/nexus-api/torture.go", "src/cmd/nexus-api/runs.go", "src/cmd/nexus-api/xray.go", "src/cmd/nexus-api/reliability.go"],
  },
  {
    id: "torture", name: "Torture & bench tools", layer: 0, sub: "nexus-torture · kvdb-bench · kvdb-perf · kvdb-mutant",
    responsibility: "External testers. nexus-torture drives kvdb serve under load, kills it, restarts it and checks every acknowledged write with an independent oracle; it also runs 32 fault scenarios and mutation testing. kvdb-bench measures single-thread throughput in-process; kvdb-perf measures concurrency, latency percentiles, memory, recovery and compaction blocking.",
    inputs: "Seeds, modes, iteration counts.", outputs: "summary.json, scenarios.json, mutation-report.json, bench.json and console logs.",
    interacts: ["cli", "api"], files: ["torture2/cmd/nexus-torture/main.go", "torture2/engine/engine.go", "torture2/oracle/check.go", "torture2/scenario/catalog.go", "torture2/mutation/mutation.go", "src/cmd/kvdb-bench/main.go", "src/cmd/kvdb-perf/main.go"],
  },
  {
    id: "api", name: "Database API", layer: 1, sub: "package kvdb",
    responsibility: "The public library: Open, Put, Get, Delete, Batch, Compact, Close, plus Stats, Recovery, Range and Verify. Thin: validates arguments and forwards to the storage handle.",
    inputs: "Keys, values, batches of operations.", outputs: "Values, ErrNotFound, and classified errors (usage, damaged, corrupt, I/O, full, locked).",
    interacts: ["storage", "recovery", "checks"], files: ["src/kvdb/kvdb.go", "src/kvdb/db.go", "src/kvdb/inspect.go"],
  },
  {
    id: "lock", name: "Locking", layer: 2, sub: "process lock + wmu / mu",
    responsibility: "Acquire is the first filesystem operation of Open: an exclusive lock file (share mode on Windows, flock on Unix) so only one process opens the database. Inside the process two locks, always taken in the order wmu then mu: wmu serializes everything that changes the database (commits, compaction, close); mu (an RWMutex) protects what readers see and is held exclusively only for the short in-memory publish after a sync. Readers therefore never wait for an fsync or for the copy phase of compaction.",
    inputs: "Database path.", outputs: "A *Lock that other packages demand as proof before renaming or truncating anything; ErrLocked if busy.",
    interacts: ["recovery", "storage"], files: ["src/internal/locking/locking.go", "src/internal/platform/lock_windows.go", "src/internal/platform/lock_unix.go"],
  },
  {
    id: "storage", name: "Commit path", layer: 2, sub: "package storage · the open handle",
    responsibility: "The single write path: size and admit the operations, encode one frame, append it strictly at the end of the log, fsync, then update the index. Group commit: commits that arrive while another is syncing wait in a queue; the next holder of wmu writes them as one frame with one fsync (no timer, so a lone writer never waits), and each caller is acknowledged only after that fsync. Reads use a small pool of extra read-only handles so parallel GETs do not serialize on one file handle. Also rollback, the FAILED state and Close. At most one unsynced frame exists, always the last one.",
    inputs: "Operations from the API.", outputs: "Acknowledgement only after the frame covering the operation is durable; reads verified against the entry checksum.",
    interacts: ["lock", "admission", "codec", "index", "log", "sync", "compaction"], files: ["src/internal/storage/log.go", "src/internal/storage/readpool.go", "src/internal/storage/inspect.go"],
  },
  {
    id: "admission", name: "Admission (max_bytes)", layer: 2, sub: "package admission",
    responsibility: "Pure arithmetic before any I/O: decides whether a commit is appended, turned into a compacting commit, or refused with ErrFull under --max-bytes; also the automatic-compaction trigger.",
    inputs: "Current file size, live bytes, size of the new frame.", outputs: "append | compact-and-commit | ErrFull; trigger yes/no.",
    interacts: ["storage", "compaction"], files: ["src/internal/admission/admission.go"],
  },
  {
    id: "index", name: "Hash index", layer: 3, sub: "in memory · rebuilt at open",
    responsibility: "Maps each live key to the location (offset, size) of its latest PUT entry in the log, plus live-key and live-byte counters. Values are never held in memory. Not durable: recovery rebuilds it from the log.",
    inputs: "Applied entries (PUT sets, DEL removes).", outputs: "Location for a GET; live counts for Stats and admission.",
    interacts: ["storage", "recovery", "compaction"], files: ["src/internal/index/index.go"],
  },
  {
    id: "codec", name: "Frame encoder / decoder", layer: 3, sub: "package format",
    responsibility: "Owns every byte-level decision: 32-byte superblock, 32-byte frame header, 11-byte entry header, length limits and overflow-safe arithmetic. Does no file I/O of its own, so it is tested exhaustively in memory and fuzzed.",
    inputs: "Operations to encode; byte streams to decode.", outputs: "Frames; decoded headers and entries; classified errors (damaged vs corrupt).",
    interacts: ["storage", "recovery", "checksums", "compaction"], files: ["src/internal/format/frame.go", "src/internal/format/entry.go", "src/internal/format/superblock.go", "src/internal/format/stream.go", "src/internal/format/errors.go"],
  },
  {
    id: "checksums", name: "Checksums", layer: 3, sub: "CRC32C ×4 levels",
    responsibility: "CRC32C (Castagnoli) over the superblock, each frame header, each frame payload and each entry. Header CRC catches torn headers; payload CRC catches torn or flipped payloads; entry CRC lets a single GET verify its own bytes.",
    inputs: "Bytes being written or read.", outputs: "Stored CRCs; mismatch → damaged tail or corruption.",
    interacts: ["codec"], files: ["src/internal/format/checksum.go"],
  },
  {
    id: "sync", name: "File I/O & sync", layer: 3, sub: "fileio · platform",
    responsibility: "The engine's only door to the filesystem. File.Sync after every commit; directory sync after creation and renames; non-overwriting moves (MoveNoReplace) for compaction and authority resolution; OS error classification (disk full, sharing violation).",
    inputs: "Writes, syncs, moves from storage, recovery, compaction.", outputs: "Durable bytes on disk or a classified error. Tests swap in faultfs to inject failures.",
    interacts: ["storage", "recovery", "compaction", "log"], files: ["src/internal/fileio/fileio.go", "src/internal/fileio/retry.go", "src/internal/platform/syncdir_windows.go", "src/internal/platform/move_windows.go", "src/internal/fileio/faultfs/faultfs.go"],
  },
  {
    id: "log", name: "Append-only log", layer: 4, sub: "the data file · authoritative",
    responsibility: "The only durable state: superblock, then an optional sealed snapshot region written by compaction, then frames appended in sequence order. Never modified in place; only truncated (proven torn tail) or replaced whole (compaction).",
    inputs: "Appended frames.", outputs: "Everything recovery needs to rebuild the index.",
    interacts: ["storage", "recovery", "compaction", "checks"], files: ["docs/FILE_FORMAT.md", "src/internal/format/constants.go"],
  },
  {
    id: "recovery", name: "Recovery engine", layer: 4, sub: "package recovery · every Open",
    responsibility: "Lock; resolve which file is authoritative among path, .new, .compact and .old (rules A1–A7); validate the superblock; scan frames sequentially checking magic, header CRC, bounds, payload CRC, entry CRCs and sequence; apply each valid frame whole; rebuild the index; truncate only a proven torn final frame (tail rules C0–C8). Mid-log corruption makes open fail without touching the file.",
    inputs: "The files on disk.", outputs: "An open State (lock, file, index, next sequence) or a classified error; structured ScanError (rule, offset) and RefusalError (rule, file) that X-RAY reads with errors.As instead of parsing text. recovery.Plan computes the same decisions read-only, without the lock.",
    interacts: ["lock", "codec", "checksums", "index", "log", "sync", "xray"], files: ["src/internal/recovery/open.go", "src/internal/recovery/authority.go", "src/internal/recovery/scan.go", "src/internal/recovery/inspect.go", "src/internal/recovery/errors.go"],
  },
  {
    id: "compaction", name: "Compaction engine", layer: 4, sub: "storage/compact.go",
    responsibility: "Rewrites only the live keys into path.compact, syncs the frames, writes the superblock last to seal it, then two non-overwriting moves: path → .old (R1) and .compact → path (R2). Reopens and validates the new file before swapping the index; removes .old. A crash at any point is resolved by the authority rules.",
    inputs: "Index (live keys) and the current log.", outputs: "A smaller file; never a larger one.",
    interacts: ["admission", "index", "codec", "sync", "log"], files: ["src/internal/storage/compact.go", "src/internal/compaction/doc.go"],
  },
  {
    id: "checks", name: "Checking tools", layer: 4, sub: "Verify · CheckReadOnly · kvdb check",
    responsibility: "Verify re-reads every live entry and checks it against its checksums through the open handle. CheckReadOnly / kvdb check inspect a file without opening it for writing. The dashboard's log map reuses the same decoders read-only.",
    inputs: "The open handle or a path.", outputs: "A report: frames, entries, live keys, errors.",
    interacts: ["api", "codec", "log", "xray"], files: ["src/kvdb/inspect.go", "src/internal/storage/inspect.go", "src/internal/recovery/inspect.go"],
  },
  {
    id: "xray", name: "X-RAY engine", layer: 4, sub: "package xray · read-only diagnostics",
    responsibility: "Diagnoses a database without changing it. Two independent views: the engine's own verdict (recovery.Plan, the same rules Open applies) and a byte walk with the format decoders that locates each problem by offset, frame and sequence and decides whether a later CRC-valid frame exists. Disagreement between the two is itself reported. Findings carry a severity, a plain and a technical explanation, expected vs. actual values, data impact and a recovery status that is only ever 'repaired' after a backup, a real open, Verify and a fresh scan.",
    inputs: "A database path (CLI) or a fixed target ID (nexus-api); typed errors from recovery and the adapter.", outputs: "A structured report (JSON) with before/after file fingerprints; kvdb xray exit codes 0/4/1.",
    interacts: ["recovery", "codec", "checksums", "log", "checks", "http"], files: ["src/internal/xray/model.go", "src/internal/xray/walk.go", "src/internal/xray/scan.go", "src/internal/xray/repair.go", "src/internal/xray/operational.go", "src/internal/xray/lab.go", "src/internal/cli/xray.go"],
  },
  {
    id: "powercut", name: "Power-Cut Explorer", layer: 0, sub: "nexus-powercut · modeled power loss",
    responsibility: "Records every create, write, sync, move and directory sync the real engine issues during a concurrent workload (a fileio wrapper), then builds disk images a storage device could leave if power failed at a chosen point, under a stated model (synced data durable, 512-byte sectors atomic, unsynced writes kept/lost/partial). Each image is opened with the real recovery and checked against an oracle; failures are diagnosed by X-RAY. A finite, seeded sample, not a proof.",
    inputs: "Seed, writers, operations, crash-point and variant limits.", outputs: "report.json: outcomes per recovery rule, checks, failures with X-RAY summaries, negative controls.",
    interacts: ["sync", "recovery", "xray", "http"], files: ["src/internal/powercut/record.go", "src/internal/powercut/model.go", "src/internal/powercut/explore.go", "src/internal/powercut/controls.go", "src/cmd/nexus-powercut/main.go"],
  },
];

const LAYERS = ["Clients", "Library", "Coordination", "Core", "Disk & lifecycle"];

type Step = { at: string[]; text: string };
const FLOWS: Record<string, { label: string; steps: Step[] }> = {
  put: {
    label: "PUT",
    steps: [
      { at: ["cli", "http"], text: "A client calls Put(key, value) (kvdb put, kvdb serve, or nexus-api)." },
      { at: ["api"], text: "kvdb.Put validates the key and value lengths and forwards a one-operation commit." },
      { at: ["storage", "admission"], text: "The commit path sizes the entry and frame; admission decides append, compacting commit, or ErrFull, before any I/O." },
      { at: ["codec", "checksums"], text: "The entry (11-byte header + key + value) and its 32-byte frame header are encoded with CRC32C over entry, payload and header." },
      { at: ["lock", "storage"], text: "The commit joins the queue and takes the writer lock wmu. If another commit was syncing meanwhile, the new holder takes every queued commit with it (group commit)." },
      { at: ["storage", "log"], text: "One frame holding the queued operations is written at the end of the log. Nothing earlier in the file is touched." },
      { at: ["sync", "log"], text: "File.Sync: the frame, and every operation in it, is durable. Readers were not blocked during the sync." },
      { at: ["lock", "index"], text: "mu is taken exclusively for a moment and the index points each key at its new entry's offset." },
      { at: ["api", "cli", "http"], text: "Only now is the write acknowledged. A crash before this point leaves at most a torn final frame, which recovery discards." },
    ],
  },
  get: {
    label: "GET",
    steps: [
      { at: ["cli", "http"], text: "A client calls Get(key)." },
      { at: ["api", "lock"], text: "kvdb.Get takes mu shared (many readers run together, and never wait for a writer's fsync)." },
      { at: ["index"], text: "One hash-map lookup gives the offset and size of the key's latest PUT entry. Missing → ErrNotFound with no disk read." },
      { at: ["log", "sync"], text: "One positioned read of exactly that entry, through an idle handle from the read pool." },
      { at: ["codec", "checksums"], text: "The entry header, key match and entry CRC are verified; a mismatch is reported as corruption, never returned as data." },
      { at: ["api", "cli", "http"], text: "The value is returned." },
    ],
  },
  del: {
    label: "DELETE",
    steps: [
      { at: ["cli", "http"], text: "A client calls Delete(key)." },
      { at: ["api", "index"], text: "If the key is not in the index, Delete succeeds immediately: nothing is written." },
      { at: ["codec", "checksums"], text: "Otherwise a DEL entry (key, empty value) is framed and checksummed like a PUT." },
      { at: ["lock", "storage", "log"], text: "wmu (possibly grouped with other commits); the frame is appended. Old PUT entries stay in the file as dead bytes until compaction." },
      { at: ["sync"], text: "File.Sync." },
      { at: ["index"], text: "The key is removed from the index." },
      { at: ["api", "cli", "http"], text: "Acknowledged after the sync." },
    ],
  },
  batch: {
    label: "BATCH",
    steps: [
      { at: ["cli", "http"], text: "A client sends several PUT/DEL operations as one batch." },
      { at: ["api", "storage", "admission"], text: "All operations are validated and sized together and admitted as one commit." },
      { at: ["codec", "checksums"], text: "They are encoded into ONE frame (op_count = number of operations) with one payload CRC over all entries." },
      { at: ["lock", "log"], text: "One append." },
      { at: ["sync"], text: "One fsync for the whole batch." },
      { at: ["index"], text: "All operations are applied to the index in order." },
      { at: ["recovery"], text: "On recovery a frame is applied whole or not at all, so a crash can never expose half a batch." },
    ],
  },
  recovery: {
    label: "Recovery",
    steps: [
      { at: ["api"], text: "kvdb.Open is called (after a clean close or a crash: the path is the same)." },
      { at: ["lock"], text: "The process lock is acquired first." },
      { at: ["recovery", "sync"], text: "Authority resolution: leftover .new, .compact or .old files from an interrupted creation or compaction are adopted or removed (rules A1–A7)." },
      { at: ["codec", "checksums", "log"], text: "The superblock is read and its CRC checked." },
      { at: ["recovery", "log"], text: "Frames are scanned sequentially from the start." },
      { at: ["codec", "checksums"], text: "Each frame: magic, header CRC, bounds, payload CRC, entry CRCs, and contiguous sequence numbers." },
      { at: ["index"], text: "Each valid frame's entries are applied whole; the index is rebuilt." },
      { at: ["recovery", "log", "sync"], text: "Tail: a damaged final frame that a killed write explains is truncated and synced. Anything else (corruption mid-log, checksum-valid but invalid content) fails the open and leaves the file untouched." },
    ],
  },
  compaction: {
    label: "Compaction",
    steps: [
      { at: ["admission", "api"], text: "Triggered by Compact(), or automatically after a commit when the file is at least 4 MiB and more than twice its estimated compacted size." },
      { at: ["lock", "compaction"], text: "wmu (writers wait; readers keep running); the snapshot is planned from the index: live keys only, ordered by key." },
      { at: ["compaction", "codec", "log"], text: "Live entries are copied (re-verified) into snapshot frames in path.compact." },
      { at: ["sync"], text: "The frames are synced, then the superblock is written LAST, sealing the file." },
      { at: ["sync", "log"], text: "R1: path → path.old. R2: path.compact → path. Both moves refuse to overwrite." },
      { at: ["compaction", "recovery"], text: "The new file is reopened and validated before it becomes authoritative in memory." },
      { at: ["lock", "index"], text: "mu is taken exclusively only now: the index is swapped to the new offsets; .old is removed. A crash anywhere above is resolved at the next Open." },
    ],
  },
  xray: {
    label: "X-RAY",
    steps: [
      { at: ["cli", "http"], text: "kvdb xray <path>, or POST /api/xray/scan with a target ID (main, demo, lab:<case>): the browser can never name a path." },
      { at: ["xray", "sync"], text: "The file's size, modification time and SHA-256 are recorded. The lock is only probed if the .lock file already exists, so nothing is created." },
      { at: ["recovery", "xray"], text: "Storage validation: recovery.Plan applies the authority rules A1–A7 and the scan read-only and returns the engine's verdict, with structured ScanError / RefusalError values." },
      { at: ["xray", "codec", "checksums", "log"], text: "Independent byte walk: superblock, every frame header, payload CRC and entry; the stop point is classified C0–C7 and the rest of the file is searched for later CRC-valid frames." },
      { at: ["xray"], text: "Structured errors → findings: each gets a code, severity, offset, frame, sequence, expected/actual, data impact and recovery status. If the walk and the engine disagree, that is a finding too." },
      { at: ["xray", "sync"], text: "The fingerprint is taken again; the report states whether the file changed during the run." },
      { at: ["http", "cli"], text: "Report → frontend (X-RAY page, Overview counts) or terminal / JSON. Repair, only for test copies and with --yes or a confirmation, backs up first and is marked repaired only after Open, Verify and a fresh scan pass." },
    ],
  },
  powercut: {
    label: "Power cut",
    steps: [
      { at: ["powercut", "api"], text: "nexus-powercut runs a seeded concurrent workload (puts, overwrites, batches, deletes, compactions) through the real engine." },
      { at: ["powercut", "sync"], text: "A recording fileio layer logs every create, write (with its bytes), sync, move, remove and directory sync, in the order the engine issued them." },
      { at: ["powercut"], text: "At each chosen crash point, images are built: synced data kept; unsynced writes kept, lost or kept by whole 512-byte sectors; directory operations as ordered prefixes." },
      { at: ["recovery", "index", "log"], text: "Each image is opened with the real recovery: authority rules, scan, tail truncation, index rebuild." },
      { at: ["powercut", "checks"], text: "Verify, then an oracle: every acknowledged write present, no value that was never written, batches all-or-nothing, overwrites in order." },
      { at: ["xray", "powercut"], text: "A failing image is kept as it was before recovery and diagnosed by X-RAY; negative controls (fsync lie, bit flip) must be caught." },
    ],
  },
};

export default function ArchitecturePage() {
  const [sel, setSel] = useState<string>("storage");
  const [flow, setFlow] = useState<string>("put");
  const [step, setStep] = useState(0);
  const [playing, setPlaying] = useState(false);
  const f = FLOWS[flow];
  const active = new Set(f.steps[step]?.at ?? []);
  const comp = COMPONENTS.find((c) => c.id === sel)!;

  useEffect(() => {
    if (!playing) return;
    const t = setTimeout(() => {
      if (step < f.steps.length - 1) setStep(step + 1);
      else setPlaying(false);
    }, 1800);
    return () => clearTimeout(t);
  }, [playing, step, f.steps.length]);

  const pickFlow = (k: string) => {
    setFlow(k);
    setStep(0);
    setPlaying(true);
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Architecture"
        description="The components that exist in this repository and how a request moves through them. Click a component for its responsibility, inputs, outputs and source files. Pick a flow to animate it step by step; X-RAY and Power cut show how diagnostics and modeled power-loss testing use the real recovery."
      />

      <Panel
        title="Flows"
        actions={
          <div className="flex flex-wrap gap-1">
            {Object.entries(FLOWS).map(([k, v]) => (
              <Button key={k} size="xs" variant={flow === k ? "secondary" : "ghost"} onClick={() => pickFlow(k)}>{v.label}</Button>
            ))}
          </div>
        }
      >
        <div className="flex flex-wrap items-center gap-2">
          <Button size="icon-sm" variant="ghost" aria-label="Previous step" disabled={step === 0} onClick={() => { setPlaying(false); setStep(step - 1); }}><ChevronLeft /></Button>
          <Button size="icon-sm" variant="ghost" aria-label={playing ? "Pause" : "Play"} onClick={() => { if (!playing && step === f.steps.length - 1) setStep(0); setPlaying(!playing); }}>{playing ? <Pause /> : <Play />}</Button>
          <Button size="icon-sm" variant="ghost" aria-label="Next step" disabled={step === f.steps.length - 1} onClick={() => { setPlaying(false); setStep(step + 1); }}><ChevronRight /></Button>
          <span className="font-mono text-xs text-faint">{f.label} · step {step + 1} / {f.steps.length}</span>
          <div className="flex gap-1">
            {f.steps.map((_, i) => (
              <button key={i} type="button" aria-label={`Step ${i + 1}`} onClick={() => { setPlaying(false); setStep(i); }} className={cn("h-1.5 w-5 rounded-full transition-colors", i < step ? "bg-foreground/40" : i === step ? "bg-foreground" : "bg-elevated")} />
            ))}
          </div>
        </div>
        <p key={flow + step} className="mt-2 min-h-10 animate-in fade-in text-sm text-foreground duration-300">{f.steps[step]?.text}</p>
      </Panel>

      <div className="grid gap-4 xl:grid-cols-[1.6fr_1fr]">
        <div className="space-y-2">
          {LAYERS.map((layer, li) => (
            <div key={layer} className="grid gap-2 md:grid-cols-[7rem_1fr]">
              <div className="pt-2 text-[11px] uppercase tracking-wider text-faint">{layer}</div>
              <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
                {COMPONENTS.filter((c) => c.layer === li).map((c) => {
                  const on = active.has(c.id);
                  const linked = comp.interacts.includes(c.id);
                  return (
                    <button
                      key={c.id}
                      type="button"
                      onClick={() => setSel(c.id)}
                      className={cn(
                        "relative rounded-sm border bg-card px-3 py-2 text-left transition-all duration-300",
                        on ? "border-foreground bg-elevated shadow-[0_0_0_1px_var(--color-foreground)]" : sel === c.id ? "border-foreground/50" : linked ? "border-border border-dashed" : "border-border",
                        "hover:border-foreground/60",
                      )}
                    >
                      {on && <span className="absolute top-2 right-2 size-1.5 animate-pulse rounded-full bg-foreground" />}
                      <div className="text-sm text-foreground">{c.name}</div>
                      <div className="mt-0.5 text-[11px] text-faint">{c.sub}</div>
                    </button>
                  );
                })}
              </div>
            </div>
          ))}
          <p className="text-[11px] text-faint">Solid outline: the selected component. Dashed: components it interacts with. Bright: active in the current flow step.</p>
        </div>

        <Panel title={comp.name} description={comp.sub}>
          <dl className="space-y-3 text-xs">
            <div><dt className="text-faint">Responsibility</dt><dd className="mt-0.5 leading-relaxed text-foreground">{comp.responsibility}</dd></div>
            <div><dt className="text-faint">Inputs</dt><dd className="mt-0.5 text-muted-foreground">{comp.inputs}</dd></div>
            <div><dt className="text-faint">Outputs</dt><dd className="mt-0.5 text-muted-foreground">{comp.outputs}</dd></div>
            <div>
              <dt className="text-faint">Interacts with</dt>
              <dd className="mt-1 flex flex-wrap gap-1">
                {comp.interacts.map((id) => (
                  <button key={id} type="button" onClick={() => setSel(id)} className="rounded-sm border border-border px-1.5 py-0.5 text-muted-foreground hover:text-foreground">
                    {COMPONENTS.find((c) => c.id === id)?.name}
                  </button>
                ))}
              </dd>
            </div>
            <div>
              <dt className="text-faint">Source files</dt>
              <dd className="mt-1 space-y-0.5 font-mono text-[11px] text-muted-foreground">{comp.files.map((p) => <div key={p}>{p}</div>)}</dd>
            </div>
          </dl>
        </Panel>
      </div>
    </div>
  );
}
