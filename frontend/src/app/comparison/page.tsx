"use client";

// How NEXUS relates to well-known storage engines. Statements about other
// systems describe their documented, default designs at a high level; NEXUS
// statements follow this repository's code. The evidence numbers at the bottom
// are read from the recorded validation files, not typed in.
import { useQuery } from "@tanstack/react-query";
import { useNexus } from "@/components/providers";
import { ErrorState, LoadingBlock, PageHeader, Panel, RecordedBadge } from "@/components/nexus";
import { RECORDED } from "@/components/torture-extra";
import { MutationReport, ScenarioResults, TortureSummary } from "@/lib/api/schemas";
import { formatNumber } from "@/lib/format";
import { cn } from "@/lib/utils";

const SYSTEMS = ["NEXUS", "Bitcask", "Redis", "LevelDB", "RocksDB"] as const;

const ROWS: { aspect: string; cells: Record<(typeof SYSTEMS)[number], string> }[] = [
  {
    aspect: "Storage architecture",
    cells: {
      NEXUS: "One append-only binary log file (superblock + checksummed frames).",
      Bitcask: "Log-structured hash table: one active append-only data file plus older immutable data files.",
      Redis: "In-memory data structures; optional persistence via RDB snapshots and/or an append-only file (AOF).",
      LevelDB: "Log-structured merge tree: memtable + write-ahead log, flushed to sorted, immutable SST files in levels.",
      RocksDB: "LSM tree like LevelDB, with many more options (column families, multiple compaction styles, tiered storage).",
    },
  },
  {
    aspect: "Index",
    cells: {
      NEXUS: "In-memory hash map: key → offset and size of its latest entry. Values stay on disk.",
      Bitcask: "In-memory hash map (keydir): key → file id, offset, size, timestamp. Same idea.",
      Redis: "The dataset itself is in memory (hash table of keys to values).",
      LevelDB: "Sorted memtable plus per-file block indexes and optional Bloom filters.",
      RocksDB: "Same family as LevelDB, plus partitioned indexes, Bloom/ribbon filters and a block cache.",
    },
  },
  {
    aspect: "Write path",
    cells: {
      NEXUS: "Encode one frame → append at end of file → fsync → update index → acknowledge. Commits that arrive during a sync are grouped into the next frame (one fsync for all; no timer).",
      Bitcask: "Append the entry to the active file → update keydir; syncing depends on the configured strategy.",
      Redis: "Update memory; if AOF is enabled, append the command to the AOF buffer and flush per fsync policy.",
      LevelDB: "Append to the WAL → insert into memtable; flush and compaction happen in the background.",
      RocksDB: "Same as LevelDB (WAL + memtable), with group commit and pipelined writes.",
    },
  },
  {
    aspect: "Concurrency",
    cells: {
      NEXUS: "One process per database (lock file). One writer at a time with group commit; readers run in parallel and do not wait for a writer's fsync or for compaction's copy phase.",
      Bitcask: "One process opens a directory for writing at a time; reads can proceed concurrently.",
      Redis: "Commands execute one at a time on the main thread (optional I/O threads handle sockets).",
      LevelDB: "Concurrent writers are queued and their batches merged into one WAL write; reads use snapshots.",
      RocksDB: "Group commit plus options for pipelined and concurrent memtable writes; reads use snapshots.",
    },
  },
  {
    aspect: "Read path",
    cells: {
      NEXUS: "One hash lookup, one positioned read, entry checksum verified.",
      Bitcask: "One keydir lookup, one positioned read.",
      Redis: "Memory lookup; no disk read.",
      LevelDB: "Memtable, then level files (filters skip most), possibly several reads.",
      RocksDB: "Like LevelDB, with block cache and filters to cut disk reads.",
    },
  },
  {
    aspect: "Recovery",
    cells: {
      NEXUS: "Scan the whole log; validate every frame; rebuild the index; truncate only a provably torn final frame; refuse mid-log corruption.",
      Bitcask: "Rebuild the keydir from hint files (if present) or by scanning the data files.",
      Redis: "Load the RDB snapshot and/or replay the AOF.",
      LevelDB: "Replay the WAL into a memtable; the MANIFEST lists the live SST files.",
      RocksDB: "Same as LevelDB, with configurable WAL recovery modes.",
    },
  },
  {
    aspect: "Compaction",
    cells: {
      NEXUS: "Rewrite live keys to a sealed .compact file, then two non-overwriting renames; manual or automatic (file ≥ 4 MiB and > 2× its compacted size).",
      Bitcask: "Merge: rewrite live entries from immutable files into new files (and hint files).",
      Redis: "AOF rewrite / new RDB snapshot.",
      LevelDB: "Background leveled compaction of SST files.",
      RocksDB: "Background leveled, universal or FIFO compaction.",
    },
  },
  {
    aspect: "Durability",
    cells: {
      NEXUS: "Every acknowledged write is fsynced first. Not configurable.",
      Bitcask: "Configurable sync strategy (none, every write, interval).",
      Redis: "Configurable: appendfsync always / everysec / no, or snapshots only.",
      LevelDB: "Per-write sync option, off by default.",
      RocksDB: "Per-write sync option (off by default), WAL can be disabled.",
    },
  },
  {
    aspect: "Damage diagnostics",
    cells: {
      NEXUS: "kvdb xray: read-only report of each problem with offset, frame, sequence, expected/actual, data impact and what recovery would do; repair only with a backup and post-repair verification.",
      Bitcask: "Not specified by the original design paper; behaviour on damaged files depends on the implementation.",
      Redis: "redis-check-aof and redis-check-rdb inspect (and for AOF, can truncate) persistence files.",
      LevelDB: "Paranoid checks option and a RepairDB function that salvages what it can.",
      RocksDB: "ldb and sst_dump tools, paranoid checks and RepairDB.",
    },
  },
  {
    aspect: "Typical use case",
    cells: {
      NEXUS: "A small embedded key-value store built to study and prove crash safety.",
      Bitcask: "Embedded storage where all keys fit in RAM and writes must be fast (e.g. Riak backend).",
      Redis: "Network cache and in-memory data-structure server.",
      LevelDB: "Embedded ordered key-value storage (e.g. browsers, local apps).",
      RocksDB: "High-throughput embedded storage for large datasets and other databases.",
    },
  },
];

const DECISIONS: { title: string; why: string; cost: string }[] = [
  { title: "Append-only log", why: "Nothing already written is ever modified, so a crash can only damage the last frame being appended. That reduces crash analysis to one well-defined case.", cost: "Overwritten and deleted data stays in the file until compaction." },
  { title: "In-memory hash index", why: "A GET is one lookup and one read, and the index needs no on-disk format or crash handling of its own: it is rebuilt from the log.", cost: "All keys must fit in memory; no ordered range scans; open time grows with the log." },
  { title: "Checksums at four levels", why: "Superblock, frame header, payload and entry CRC32C let recovery tell a torn write (expected after a crash) from corruption (must not be hidden), and let each GET verify its own bytes.", cost: "4 bytes per level and the CPU to compute them." },
  { title: "Sequence numbers", why: "Each frame carries the next sequence number, so a stale or duplicated frame that happens to have valid checksums is still detected.", cost: "8 bytes per frame." },
  { title: "Sync before acknowledgement", why: "An acknowledged write must survive power loss. Without the fsync the OS could still lose it.", cost: "A single writer is bounded by disk sync latency (about 2,600 synced writes/s in the recorded measurements on the development machine)." },
  { title: "Group commit without a timer", why: "Concurrent commits that queue up during an fsync share the next frame and its single fsync, so throughput grows with the number of writers while every caller still returns only after the sync that covers it. Recorded on the development machine: 2,442 → 12,426 PUT/s with 16 writers.", cost: "Commits merged into one frame are durable together or not at all; a lone writer gains nothing." },
  { title: "Separate writer and reader locks", why: "Readers see only published state, so they need not wait for an fsync or for compaction's copy. Recorded: the longest reader stall during compaction fell from about 502 ms to about 37 ms.", cost: "Two locks with a fixed order (wmu before mu) to reason about; compaction under read load took longer (502 → 848 ms in the recorded run)." },
  { title: "A batch is one frame", why: "One payload checksum covers every operation of the batch and recovery applies frames whole, so a batch is atomic by construction, with one fsync.", cost: "A batch must fit in one frame (bounded op count and payload)." },
  { title: "Sequential recovery", why: "Scanning from the start with strict rules gives one simple, testable answer to 'what is in this file'. Only a provably torn tail is truncated.", cost: "Open time is linear in file size (no hint files or checkpoints)." },
  { title: "Compaction with sealed file and non-overwriting renames", why: "The old file stays authoritative until the new one is complete, synced and sealed; an authority table resolves any crash in between.", cost: "Needs free space for a second copy of the live data while it runs." },
  { title: "X-RAY: structured, read-only diagnostics", why: "Recovery's decisions are exposed as typed errors (rule, offset, file), and an independent byte walk cross-checks them, so a report says exactly what is wrong and what the next open would do, without guessing from message text. It found one real limitation: damage to a mid-log frame header looks like a torn tail, so a normal open would drop later committed frames; X-RAY flags it CRITICAL and refuses repair.", cost: "A second reader of the format to keep in step with the engine (the cross-check reports any disagreement)." },
  { title: "Power-Cut Explorer: a written failure model", why: "Modeled disk images built from the real engine's recorded writes and syncs test what a power loss could leave, not only what a killed process leaves, and the assumptions (synced data durable, 512-byte sectors atomic) are stated.", cost: "A finite sample of one model: lying disks, torn sectors and filesystem bugs are outside it; it is evidence, not proof." },
];

function Evidence() {
  const { api } = useNexus();
  const sum = useQuery({ queryKey: [api.mode, "evidence-json", RECORDED.summary], queryFn: () => api.evidenceJson(RECORDED.summary, TortureSummary), staleTime: Infinity, retry: false });
  const scen = useQuery({ queryKey: [api.mode, "evidence-json", RECORDED.scenarios], queryFn: () => api.evidenceJson(RECORDED.scenarios, ScenarioResults), staleTime: Infinity, retry: false });
  const mut = useQuery({ queryKey: [api.mode, "evidence-json", RECORDED.mutation], queryFn: () => api.evidenceJson(RECORDED.mutation, MutationReport), staleTime: Infinity, retry: false });
  if (sum.isPending || scen.isPending || mut.isPending) return <LoadingBlock rows={3} />;
  const err = sum.error ?? scen.error ?? mut.error;
  if (err) return <ErrorState error={err} compact />;
  const s = sum.data!, sc = scen.data!, m = mut.data!;
  const scPass = sc.results.filter((r) => r.status === "PASS").length;
  const muts = m.outcomes.filter((o) => o.mutation !== "none");
  const caught = muts.filter((o) => o.detected).length;
  const control = m.outcomes.find((o) => o.mutation === "none");
  const items = [
    [`${formatNumber(s.passed)} / ${formatNumber(s.iterations_planned)}`, `crash-and-restart iterations passed (mode ${s.mode}, seed ${s.seed})`],
    [formatNumber(s.verification.acknowledged_writes_verified_after_restart), `acknowledged writes verified after restart (of ${formatNumber(s.verification.acknowledged_writes)} acknowledged)`],
    [String(s.verification.batch_atomicity_violations + s.verification.data_integrity_violations), "batch-atomicity and data-integrity violations"],
    [`${scPass} / ${sc.results.length}`, "deterministic fault scenarios passed (quick set)"],
    [`${caught} / ${muts.length}`, `deliberate bugs detected by the tester; control ${control?.engine_result === "PASS" ? "passed (no false alarm)" : "did not pass"}`],
  ];
  return (
    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
      {items.map(([v, l]) => (
        <div key={l} className="rounded-sm border border-border bg-background p-3">
          <div className="font-mono text-lg text-foreground">{v}</div>
          <div className="mt-1 text-[11px] leading-snug text-muted-foreground">{l}</div>
        </div>
      ))}
    </div>
  );
}

export default function ComparisonPage() {
  return (
    <div className="space-y-6">
      <PageHeader
        title="How NEXUS Differs From Existing Database Architectures"
        description="NEXUS uses well-known ideas: an append-only log and an in-memory hash index are the design of Bitcask, and write-ahead logs and checksums are standard. It did not invent them. What it adds is a small, fully specified, heavily tested implementation of those ideas."
      />

      <Panel
        title="NEXUS does not claim to outperform or replace these systems"
        description="Redis, LevelDB, RocksDB and Bitcask are mature, production systems with years of optimisation, features and operational experience. NEXUS is a compact engine built to make crash safety explicit and verifiable. No benchmark against them was run; none is shown."
      >
        <p className="text-xs text-muted-foreground">Closest relative: <span className="text-foreground">Bitcask</span>. Both keep every key in an in-memory hash map pointing into append-only data on disk, read with one seek, and reclaim space by rewriting live data. NEXUS differs in using one file, framing writes with sequence numbers so a batch is atomic, always syncing before acknowledging, and recovering with explicit rules that separate a torn tail from corruption.</p>
        <p className="mt-2 text-xs text-muted-foreground">
          <span className="text-foreground">Where Bitcask is ahead.</span> Hint files let Bitcask rebuild its keydir without reading every value, so it starts faster than NEXUS, which scans the whole log at every open. Its merge works on immutable older files while the active file keeps taking writes, and production implementations add features NEXUS does not have (key expiry, multiple data files, years of operational use). NEXUS chose a single file and a full scan because they make the crash rules simpler to state and test, not because they are faster.
        </p>
      </Panel>

      <Panel title="Side by side" description="Default, documented designs at a high level. Each of the other systems has options beyond what fits in a cell." bodyClassName="p-0">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[56rem] text-xs">
            <thead>
              <tr className="border-b border-border text-left">
                <th className="w-36 px-3 py-2 font-normal text-faint" />
                {SYSTEMS.map((s) => (
                  <th key={s} className={cn("px-3 py-2 font-medium", s === "NEXUS" ? "bg-elevated text-foreground" : "text-muted-foreground")}>
                    {s}{s === "Bitcask" && <span className="ml-1 font-normal text-faint">(closest)</span>}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {ROWS.map((r) => (
                <tr key={r.aspect} className="border-b border-border align-top last:border-0">
                  <td className="px-3 py-2 text-faint">{r.aspect}</td>
                  {SYSTEMS.map((s) => (
                    <td key={s} className={cn("px-3 py-2 leading-relaxed", s === "NEXUS" ? "bg-elevated text-foreground" : "text-muted-foreground")}>{r.cells[s]}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Panel>

      <Panel title="Why We Made These Decisions" description="Each choice, what it buys, and what it costs.">
        <div className="grid gap-3 md:grid-cols-2">
          {DECISIONS.map((d) => (
            <div key={d.title} className="rounded-sm border border-border bg-background p-3">
              <div className="text-sm text-foreground">{d.title}</div>
              <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{d.why}</p>
              <p className="mt-1.5 text-[11px] text-faint">Cost: {d.cost}</p>
            </div>
          ))}
        </div>
      </Panel>

      <Panel
        title={<span className="flex items-center gap-2">What Makes NEXUS&apos;s Implementation Worth Studying? <RecordedBadge /></span>}
        description="Not novelty of the ideas, but how completely they are specified and checked. The numbers below are read from the recorded validation files in docs/results/final."
      >
        <ul className="mb-4 grid gap-2 text-xs text-muted-foreground md:grid-cols-2">
          <li><span className="text-foreground">Every byte is specified.</span> docs/FILE_FORMAT.md and package format define each field, limit and checksum; the codec is fuzzed.</li>
          <li><span className="text-foreground">Crash rules are written down.</span> Recovery tail rules C0–C8 and authority rules A1–A7 say exactly what is truncated, adopted or refused.</li>
          <li><span className="text-foreground">An independent oracle checks it.</span> nexus-torture kills real processes mid-write and checks every acknowledged write after restart.</li>
          <li><span className="text-foreground">The tester is itself tested.</span> Mutation testing plants known bugs (ACK before sync, dropped writes, partial batches…) and requires the tester to catch each one, with an unmodified control that must pass.</li>
          <li><span className="text-foreground">Faults are injected below the engine.</span> fileio/faultfs simulates short writes, failed syncs and operations that succeed but report failure.</li>
          <li><span className="text-foreground">Damage is explained, not just detected.</span> X-RAY turns recovery&apos;s rule decisions into findings with offsets and data impact, and proves on 16 deliberately damaged databases that each is diagnosed as designed.</li>
          <li><span className="text-foreground">Power loss is modeled explicitly.</span> The Power-Cut Explorer opens modeled disk images with the real recovery; the recorded default run validated 1,278 images from 291 crash points with 0 failures and caught all 6 negative controls (a sample, not a proof).</li>
          <li><span className="text-foreground">Small enough to read.</span> The whole engine is a few Go packages using only the standard library.</li>
        </ul>
        <Evidence />
      </Panel>
    </div>
  );
}
