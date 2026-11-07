# Torture tools: which one is official, and why

> **Superseded (historical record).** Since Prompt 11 the official tester is
> NEXUS Torture Engine 2.0 (`torture2/`, `nexus-torture`):
> [TORTURE_ARCHITECTURE.md](TORTURE_ARCHITECTURE.md). This page records the
> decision as it stood before that, and is kept unchanged below.

## Official tool: `src/cmd/torture/` (Go, this repository)

The competition tool is the Go program in [`src/cmd/torture/`](../src/cmd/torture) (in repository-root `torture/` until 2026-11-07). It is built
with the engine (`go build -o . ./src/cmd/kvdb ./src/cmd/torture`), launches the real
`kvdb serve` binary, kills it with `TerminateProcess` at a random millisecond,
runs `kvdb open`, `kvdb check` and `kvdb dump`, and checks the recovered state
against its own oracle (`src/cmd/torture/model.go`). It has been run end to end
against NEXUS, and the results are recorded in
[results/validation/](results/validation/) and [results/](results/).

How it works: [ARCHITECTURE.md § Torture tool](../ARCHITECTURE.md#torture-tool).

## Second tool: Rust crash-torture harness (teammate repository)

* **Repository:** <https://github.com/rohithakumar/Torture-Tool-Two-Pointer->
  (owner `rohithakumar`, author Rohith A Kumar). Audited at commit `30941e9`
  ("Complete Phase 3.1 oracle hardening").
* **What it contributes:** a well-designed independent oracle. It builds a
  ledger from requests sent and replies received (never from the database). It
  uses self-describing values (magic, run seed, writer, sequence, key, CRC32),
  and it searches for an order of operations that is consistent with each
  writer's own order and with the replies (`src/oracle/verifier.rs`). It also
  has a real process supervisor (`TerminateProcess` / `SIGKILL`) and an output
  reader that counts only complete lines.

### Status: not a deliverable yet

**It has not been built or run on the judging machine.** No Rust toolchain
(`rustc`/`cargo`) is installed there, and installing one was out of scope for
this work. None of its 42 tests have been run by us. We therefore make **no
claim** that it works against NEXUS.

Reading the source (not running it) shows that it cannot test NEXUS in its
current form:

| # | Finding | Location | Consequence | Fix needed |
|---|---|---|---|---|
| R1 | `main` parses the options, prints a banner and exits 0. There is no iteration loop, and `--mode` is only printed | `src/main.rs` | running it "passes" without testing anything | call `execute_recovery_lifecycle` once per iteration and exit non-zero on a violation |
| R2 | The only adapter drives its own mock (`mock_child`), which expects `open` to print `OPEN OK` | `src/adapter/test_adapter.rs` | it never launches `kvdb` | add an adapter for `kvdb open/check/dump/serve` (output contract: README) |
| R3 | Every `ERR` reply is treated as a rejected request with no effect | `src/oracle/ledger.rs` (status mapping) | NEXUS `ERR <id> unknown …` / `io` mean *outcome unknown*, so a correct engine would be reported as violating the rules | map classes `unknown` and `io` to in-flight (either outcome legal) |
| R4 | The writer, kill-delay and value streams depend only on `master_seed`, not on the iteration number | `src/runner/writers.rs`, `src/runner/killer.rs` | all 500 iterations would replay the same schedule | derive a per-iteration seed (e.g. splitmix of seed ⊕ iteration) |
| R5 | The solver compares against the dump only when a whole history has been applied. It costs O(n²) per step and gives up as "Inconclusive" after 100,000 steps | `src/oracle/verifier.rs` | realistic histories (hundreds of operations, all linked by each writer's order) are likely to end Inconclusive, and nothing defines what Inconclusive means | prune per key as each operation is applied, and treat Inconclusive as a failure that is reported, never as a pass |
| R6 | Values are sent as hex | `src/runner/writers.rs` | hex doubles the size, so a 1 MiB value becomes 2 MiB and exceeds the engine's 1 MiB limit (largest testable raw value: 512 KiB) | send printable values ≤ 1 MiB, or base64 with a raw size ≤ 768 KiB |
| R7 | There is no `--max-bytes` option, although the config struct has the field | `src/config.rs` | disk-full mode cannot work | parse the option and pass it to `serve`/`open` |
| R8 | A writer stops silently after 500 ms without a reply | `src/runner/writers.rs` | coverage drops quietly on a slow sync | log it and count the request as in-flight |
| R9 | `ensure_clean_db_path` ignores removal errors (`let _ =`) | `src/runner/recovery.rs` | an iteration can start on stale state | propagate the error and fail the iteration |
| R10 | No GET operations, batches of only 2–5 operations, no recovery-crash or compaction mode logic (the compaction trigger is a no-op) | `src/runner/writers.rs`, `src/adapter/test_adapter.rs` | scenarios B1–B6, C1–C5, D3, D4, E1–E5 and F2 are not exercised | add the official 10-op and 10,000-op batches, gets, kills during `open`, and `compact` requests |

Until R1–R7 are fixed **and** the tool has been run against `kvdb` with
reproducible seeds, the Go tool remains the only official tester. The Rust
oracle is worth keeping as a second, independent checker. The cheapest route
is to have the Go tool export its ledger (`oracle.jsonl` already records every
request and reply) and have the Rust verifier check that file.

## Protocol compatibility (tested)

NEXUS `serve` accepts the Rust tool's exact request lines, as shown in
`crosscheck/main.go` (`nexusDB`) and in the audit:

* `<op-id> put <key> <hex>` (with the writer ID omitted)
* `<op-id> batch <n>` followed by `put k v` / `del k` lines

`check` prints exactly `OK`, and `dump` prints `key value` lines that the Rust
tool's parser accepts for plain values. The incompatibilities are R2 (`open`
output) and R3 (error classes) above.
