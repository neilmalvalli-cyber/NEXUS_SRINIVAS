"use client";

// Judge Readiness: the 8 judge-critical crash cases. Results are read from
// src/data/judge-critical-results.json, which docs/JUDGE_READINESS/judge-critical/run.sh
// writes from a real `go test ./src/scenarios` run (it never edits results by
// hand). A case is PASS only if every one of its scenarios ran in that run
// and passed; a scenario missing from the run makes the case NOT TESTED.
import { ShieldCheck } from "lucide-react";
import { CopyButton, PageHeader, Panel, StatCard, StatusPill, type Tone } from "@/components/nexus";
import { cn } from "@/lib/utils";
import recorded from "@/data/judge-critical-results.json";

type Result = { id: string; title: string; expected: string; method: string; pass: boolean; duration_ms: number; notes: string[] };
type Run = { stamp: string; command: string; mode: string; exit_code: number; elapsed_s: number; git_head: string; uncommitted_files: number; evidence_dir: string };
type Recorded = { generated: string; goos: string; goarch: string; cpus: number; go: string; mode: string; results: Result[]; run?: Run };

type Status = "PASS" | "FAIL" | "NOT TESTED" | "INCONCLUSIVE";
const TONE: Record<Status, Tone> = { PASS: "ok", FAIL: "bad", "NOT TESTED": "unknown", INCONCLUSIVE: "warn" };

// What kind of test produced the evidence. "Simulated crash" = the faultfs
// layer stops the real engine at an exact file operation in-process and
// drops its handles (a modeled kill); "Real process" = a real kvdb child
// killed with TerminateProcess; "Real files" = real files on disk, no crash.
type Kind = "Simulated crash (faultfs, in-process)" | "Real process kill" | "Real files, no crash" | "Injected OS error";

type Case = {
  n: number;
  name: string;
  covers: string;
  scenarios: string[];
  kinds: Kind[];
  real?: string; // supplementary real-process evidence recorded in docs (not re-run by this page)
  notTested?: string;
  demo: string[];
};

const CASES: Case[] = [
  {
    n: 1, name: "Torn writes", covers: "A crash leaves part of a record on disk; it must be discarded, never returned.",
    scenarios: ["A2"], kinds: ["Simulated crash (faultfs, in-process)"],
    real: "Real kills: torture default mode, 4 seeds × 200 iterations, 0 failures (CRASH_SCENARIO_RESULTS.md).",
    demo: ["go test -count=1 -v ./src/scenarios -run 'TestGroupA$/^A2$'"],
  },
  {
    n: 2, name: "Batch atomicity", covers: "A batch killed part-way (incl. 10,000 ops on a 1M-key file) is all or nothing.",
    scenarios: ["B2", "B6"], kinds: ["Simulated crash (faultfs, in-process)", "Real process kill"],
    real: "B6 also runs torture --mode large-batch (real kills). 200-iteration run: PASS 200/200.",
    demo: ["go test -count=1 -v ./src/scenarios -run 'TestGroupB$/^(B2|B6)$'", "./torture --iterations 50 --seed 1 --mode large-batch"],
  },
  {
    n: 3, name: "Compaction crashes", covers: "Crash before or between the two renames: one complete file is authoritative, nothing lost.",
    scenarios: ["C3", "C4"], kinds: ["Simulated crash (faultfs, in-process)"],
    real: "Real kills of `kvdb serve` during a 103 MB compaction at 20–1000 ms: 60,000/60,000 keys every time; torture --mode compaction PASS 200/200 × 2 seeds (COMPACTION_SHRINK_TEST.md).",
    demo: ["go test -count=1 -v ./src/scenarios -run 'TestGroupC$/^(C3|C4)$'", "./torture --iterations 50 --seed 1 --mode compaction"],
  },
  {
    n: 4, name: "Interrupted recovery", covers: "Kill during replay, during truncation, and repeatedly during recovery: the next open reaches the same state.",
    scenarios: ["C1", "C2", "F2"], kinds: ["Simulated crash (faultfs, in-process)", "Real process kill"],
    real: "F2 includes torture --mode recovery-crash (real kills of `kvdb open`). 200-iteration run: PASS, 341 of 409 kills landed during recovery.",
    demo: ["go test -count=1 -v ./src/scenarios -run 'TestGroup[CF]$/^(C1|C2|F2)$'", "./torture --iterations 20 --seed 1 --mode recovery-crash"],
  },
  {
    n: 5, name: "Crash after fsync, before ACK", covers: "Sync returned but the caller never heard: the key is present after restart.",
    scenarios: ["A4"], kinds: ["Simulated crash (faultfs, in-process)"],
    real: "Real kills cannot target this exact instant; the manual rehearsal saw exactly this state (one durable, unacknowledged op per kill) in 12 of 12 kills (MANUAL_KILL_REHEARSAL.md).",
    demo: ["go test -count=1 -v ./src/scenarios -run 'TestGroupA$/^A4$'"],
  },
  {
    n: 6, name: "Concurrent writers", covers: "Two writers on the same key, killed at random: the value is one a writer wrote, never a mix.",
    scenarios: ["D2"], kinds: ["Real process kill"],
    real: "torture --mode same-key PASS 200/200; concurrent-disjoint PASS 200/200.",
    demo: ["go test -count=1 -v ./src/scenarios -run 'TestGroupD$/^D2$'", "./torture --iterations 50 --seed 1 --mode same-key"],
  },
  {
    n: 7, name: "1M-key recovery < 2 s", covers: "1,000,000 acknowledged keys, process killed, open must finish in under 2 s with every key present.",
    scenarios: ["F3"], kinds: ["Real process kill"],
    real: "Worst case one frame per key: open 0.77–0.80 s; warm cache only, cold cache NOT TESTED (ONE_MILLION_KEY_RECOVERY.md).",
    demo: ["go test -count=1 -v ./src/scenarios -run 'TestGroupF$/^F3$'", "go build -o . ./src/cmd/kvdb-perf && ./kvdb-perf --only recovery"],
  },
  {
    n: 8, name: "Disk full", covers: "Cap reached on a put, mid-batch, then killed, during compaction; deletes make it usable again.",
    scenarios: ["E1", "E2", "E3", "E4", "E5"], kinds: ["Real files, no crash", "Injected OS error", "Simulated crash (faultfs, in-process)", "Real process kill"],
    notTested: "a physically full volume (it needs an admin-mounted small disk). Out-of-space here is the max_bytes cap on real files plus an injected ERROR_DISK_FULL.",
    demo: ["go test -count=1 -v ./src/scenarios -run 'TestGroupE$/^E[1-5]$'", "./kvdb put cap.db k v --max-bytes 200000"],
  },
];

function caseStatus(c: Case, byId: Map<string, Result>): Status {
  const rs = c.scenarios.map((id) => byId.get(id));
  if (rs.some((r) => !r)) return "NOT TESTED";
  if (rs.some((r) => !r!.pass)) return "FAIL";
  return "PASS";
}

function fmtMs(ms: number) {
  return ms >= 1000 ? `${(ms / 1000).toFixed(2)} s` : `${ms.toFixed(0)} ms`;
}

export default function JudgeReadinessPage() {
  const data = recorded as Recorded;
  const byId = new Map(data.results.map((r) => [r.id, r]));
  const statuses = CASES.map((c) => caseStatus(c, byId));
  const count = (s: Status) => statuses.filter((x) => x === s).length;
  const run = data.run;
  const all = `bash docs/JUDGE_READINESS/judge-critical/run.sh`;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Judge Readiness"
        description="The 8 judge-critical crash cases, from the last recorded run of the official scenario suite. Nothing on this page is typed in: a case is PASS only if all of its scenarios ran and passed in that run."
        actions={<CopyButton text={all} label="Copy run command" />}
      />

      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard label="PASS" value={count("PASS")} tone="ok" icon={ShieldCheck} hint={`of ${CASES.length} cases`} />
        <StatCard label="FAIL" value={count("FAIL")} tone={count("FAIL") ? "bad" : "neutral"} />
        <StatCard label="NOT TESTED" value={count("NOT TESTED")} tone="unknown" />
        <StatCard label="Scenarios run" value={data.results.length} hint={`${data.results.filter((r) => r.pass).length} passed`} />
      </div>

      <Panel title="Recorded run" description="Where these results come from. Re-run the command to refresh them.">
        <dl className="grid grid-cols-1 gap-x-6 gap-y-1.5 text-xs sm:grid-cols-2">
          <Row k="Recorded" v={data.generated} />
          <Row k="Platform" v={`${data.goos}/${data.goarch}, ${data.cpus} CPUs, ${data.go}`} />
          <Row k="Mode" v={data.mode === "full" ? "full sizes" : "quick sizes (process scenarios use fewer iterations)"} />
          {run && <Row k="Exit code / time" v={`${run.exit_code} / ${run.elapsed_s} s`} />}
          {run && <Row k="Git HEAD" v={`${run.git_head}${run.uncommitted_files ? ` (+${run.uncommitted_files} uncommitted files)` : ""}`} />}
          {run && <Row k="Evidence folder" v={run.evidence_dir} mono />}
        </dl>
        {run && (
          <div className="mt-3 flex items-start gap-2">
            <code className="min-w-0 flex-1 rounded-sm border border-border bg-background px-2 py-1.5 font-mono text-[11px] [overflow-wrap:anywhere]">{run.command}</code>
            <CopyButton text={run.command} />
          </div>
        )}
        <p className="mt-3 text-xs text-muted-foreground">
          <strong className="text-foreground">Simulated vs real.</strong> &ldquo;Simulated crash&rdquo; stops the real engine at one exact file operation inside the test process and drops its handles without cleanup (faultfs). &ldquo;Real process kill&rdquo; kills a real <code>kvdb</code> child with TerminateProcess. Neither is a power cut: power loss is covered only by the modeled Power-Cut Explorer (Reliability Lab), and physical power-cut testing was not done.
        </p>
      </Panel>

      <div className="space-y-4">
        {CASES.map((c, i) => (
          <CaseCard key={c.n} c={c} status={statuses[i]} byId={byId} />
        ))}
      </div>
    </div>
  );
}

function Row({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <div className="flex min-w-0 gap-2">
      <dt className="w-32 shrink-0 text-muted-foreground">{k}</dt>
      <dd className={cn("min-w-0 text-foreground [overflow-wrap:anywhere]", mono && "font-mono")}>{v}</dd>
    </div>
  );
}

function CaseCard({ c, status, byId }: { c: Case; status: Status; byId: Map<string, Result> }) {
  return (
    <Panel
      title={
        <span className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-faint">{c.n}.</span> {c.name}
          <span className="font-mono text-xs text-muted-foreground">{c.scenarios.join(" · ")}</span>
        </span>
      }
      description={c.covers}
      actions={<StatusPill tone={TONE[status]}>{status}</StatusPill>}
    >
      <div className="mb-3 flex flex-wrap gap-1.5">
        {c.kinds.map((k) => (
          <span key={k} className={cn("rounded-sm border px-1.5 py-0.5 text-[11px]", k.startsWith("Real") ? "border-ok/40 text-ok" : "border-warn/40 text-warn")}>
            {k}
          </span>
        ))}
      </div>

      <div className="space-y-3">
        {c.scenarios.map((id) => {
          const r = byId.get(id);
          if (!r)
            return (
              <div key={id} className="rounded-sm border border-border p-2 text-xs">
                <span className="font-mono">{id}</span> <StatusPill tone="unknown">not in the recorded run</StatusPill>
              </div>
            );
          return (
            <div key={id} className="rounded-sm border border-border p-3 text-xs">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="min-w-0 font-medium text-foreground">
                  <span className="font-mono">{r.id}</span> · {r.title}
                </div>
                <div className="flex items-center gap-2">
                  <span className="font-mono text-faint">{fmtMs(r.duration_ms)}</span>
                  <StatusPill tone={r.pass ? "ok" : "bad"}>{r.pass ? "PASS" : "FAIL"}</StatusPill>
                </div>
              </div>
              <dl className="mt-2 space-y-1 text-muted-foreground">
                <div><dt className="inline text-foreground">Expected: </dt><dd className="inline">{r.expected}</dd></div>
                <div><dt className="inline text-foreground">Executed: </dt><dd className="inline [overflow-wrap:anywhere]">{r.method}</dd></div>
                {r.notes.length > 0 && (
                  <div><dt className="inline text-foreground">Measured: </dt><dd className="inline font-mono text-[11px] [overflow-wrap:anywhere]">{r.notes.join("; ")}</dd></div>
                )}
              </dl>
            </div>
          );
        })}
      </div>

      {c.real && (
        <p className="mt-3 text-xs text-muted-foreground">
          <span className="text-foreground">Other recorded evidence (docs/JUDGE_READINESS, 2026-11-07; not re-run here): </span>
          {c.real}
        </p>
      )}
      {c.notTested && (
        <p className="mt-2 text-xs text-warn">
          <span className="font-medium">NOT TESTED: </span>
          {c.notTested}
        </p>
      )}

      <div className="mt-3 space-y-1.5">
        <div className="text-[11px] uppercase tracking-wider text-faint">Reproduce (repository root)</div>
        {c.demo.map((cmd) => (
          <div key={cmd} className="flex items-start gap-2">
            <code className="min-w-0 flex-1 rounded-sm border border-border bg-background px-2 py-1 font-mono text-[11px] [overflow-wrap:anywhere]">{cmd}</code>
            <CopyButton text={cmd} />
          </div>
        ))}
      </div>
    </Panel>
  );
}
