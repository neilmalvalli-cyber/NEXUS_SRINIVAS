# Known limitations (judge-readiness audit)

Status: **updated for the final readiness pass (2026-11-07).** Each entry states what happens, why, and how it was verified. L1–L8 come from the Prompt 20 audit; L9–L14 were added in the final pass.

## L1. Amended rule C3 can refuse, never truncate, one crafted power-loss state

**What.** Since 2026-11-07, a damaged frame header followed by a complete, fully valid frame with seq ≥ expected makes open refuse with `ErrCorruption` (rule C3). This replaced the old behaviour, which silently truncated committed frames behind mid-log header damage (attack P7).

**Residual case.** A user value may contain bytes that form a valid frame. Suppose a power loss persisted the last frame's sectors out of order: the header sector was lost and later sectors were kept. If that frame's value also embedded a complete valid frame with a future sequence number, open now refuses a file NEXUS itself wrote.

**Why it is acceptable.**
* The outcome is a refusal with the file byte-for-byte unchanged. No data is lost, and X-RAY explains the refusal.
* A process kill cannot produce this state. It leaves a prefix of the last frame, so the header is intact and C1, C4 or C5 apply.
* Old behaviour versus new: silent loss of acknowledged data on any mid-log header damage, versus a possible availability loss in a crafted, out-of-order power-loss state.

**Evidence.**
* `recovery.TestTailC3CraftedValueBehindLostHeaderRefusesSafely` pins this behaviour.
* Power-Cut seeds 1–3 (3,937 modeled images) produced no false refusals.
* Torture `run --seed 7 --iterations 200` (real kills) produced no false refusals.
* Physical out-of-order sector loss was **NOT TESTED**; it needs hardware.

## L2. No read-only open mode

**What.** `kvdb.Open` always opens the data file and its `.lock` file for writing, because recovery may need to truncate a torn tail. So a read-only file, or a folder you cannot write to, refuses **every** command, `get` and `dump` included. The error is clear and nothing is changed (S3 tests).

**Workaround.** `kvdb xray <path>` and `kvdb check <path> --no-repair` read without writing. Both were verified on a read-only file: X-RAY reported HEALTHY, and `check --no-repair` printed `OK … file not modified`, exit 0. Otherwise, copy the file to a writable location.

**Why not changed now.** A read-only mode is a new feature (recovery without repair, with no lock file in a read-only folder), not a bug fix. Prompt 20 restricts this audit to fixing genuine bugs.

## L3. The missing-folder error names the lock file

A missing parent folder is reported as `locking: acquire <path>.lock: … The system cannot find the path specified.`, because the lock is taken before anything else. It is accurate and contains the path, but it names `<path>.lock` rather than saying "folder does not exist". This is cosmetic only.

## L4. Go race detector not run (C2, NOT TESTED)

`go test -race` needs cgo and a C toolchain (gcc), and none is installed here. The concurrency evidence is functional instead:
* `storage/concurrency_test.go` and scenarios D1–D4;
* torture runs with concurrent writers;
* O8, 12 concurrent HTTP writers across 150 kills.

That evidence does not prove the absence of data races. To close the gap, install a gcc toolchain (for example MSYS2 MinGW-w64) and run `CGO_ENABLED=1 go test -race ./...`.

## L5. Real disk-full not tested (S2, NOT TESTED)

`ErrFull` handling is tested with the `max_bytes` cap and with fault injection (`faultfs`), not with a physically full volume. Reproducing it needs a small VHD or RAM disk, and mounting one needs administrator rights, which the agent does not use.

## L6. No physical power-cut test (PC4, NOT TESTED)

Power loss is covered only by the simulated sector/flush model of the Power-Cut Explorer. See POWER_FAILURE_MODEL.md. Rename durability across a power cut and disks that lie about flushes are explicitly not claimed.

## L7. Power-Cut replay is exact only with one writer (PC3)

With several writers, the seed fixes the workload and the sampling rules, but not the thread interleaving. So the trace and the image count vary between runs, though all runs pass. Use `--writers 1` for an identical replay: every recorded figure matched between two runs (event, byte, frame, crash-point and image counts, plus outcomes and variants). The disk images themselves were not compared byte for byte.

## L8. Smaller untested corners

* **Wildcard bind next to a loopback bind** (`--addr :8080` beside `127.0.0.1:8080`) was not tested. Listening beyond loopback triggers a Windows Firewall prompt, and nexus-api defaults to loopback only.
* **Next.js dev telemetry** is enabled on this machine. The dev server process sends anonymous events to nextjs.org, sent and forgotten; no page depends on them. Start the dashboard with `NEXT_TELEMETRY_DISABLED=1` for a demo.
* **Browser coverage**: only the built-in Chromium pane was used.

## L9. `--mode recovery-crash` is slow at 200 iterations

The mode preloads 200,000 keys once. Every iteration then does a serve kill, then 1–3 kills *during* `kvdb open`, then open, check and a full dump compared key by key. That costs several seconds per iteration, so 200 iterations take far longer than the 2-minute judging slot (measured times are in CRASH_SCENARIO_RESULTS.md). The default mode takes about 70 s and compaction about 160 s. The test was **not** weakened to look faster. Mitigation: start it in a second terminal early, or run `--iterations 50`.

## L10. 1M-key recovery measured warm only

Cold-cache timing needs a cache flush (administrator tools or a reboot). Every figure in ONE_MILLION_KEY_RECOVERY.md is warm-cache, on one NVMe laptop: 0.55–0.80 s against the 2 s limit.

## L11. PowerShell not verified by the agent

PowerShell hangs inside the agent sandbox. Every command was verified in Git Bash and `cmd`. Check `./torture --seed abc` and one short run in your own PowerShell before judging (OFFLINE_DEMO_CHECKLIST.md).

## L12. The dashboard needs `node_modules`

A clean clone has no `frontend/node_modules`, and `npm install` needs the network. The Go engine, CLI and torture tools need nothing beyond the Go toolchain. Demo the dashboard from the development laptop, which already has its dependencies installed.

## L13. Design-phase prompts are missing from PROMPTS.md

The v1→v5 design-review prompts that produced DESIGN.md sections 1–7 were sent in another tool before the repository existed. No copy is on this machine. Only the person who sent them can paste them. They are not reconstructed, because the log must be verbatim.

## L14. Large amount of uncommitted work

The C3 fix, the `src/cmd/torture` move, the seed fix, the dashboard outcome-unknown fix, the new tests and all of `docs/JUDGE_READINESS/` exist only in the working tree until you commit and push. A clone of GitHub today (451b7df) fails the judge's first command: `./torture` is a folder there.
