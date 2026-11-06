# NEXUS_SRINIVAS — Kill It and Prove It

A crash-safe embedded key-value database written from scratch in Go (standard
library only) for the "Kill It and Prove It" challenge.

> **Status: Phase 1 — storage format only. The database is not functional yet.**
> The on-disk codec (superblock, frame header, entries, CRC32C, limits) is
> implemented and tested, as are the Windows platform primitives (non-overwriting
> `MoveFile`, error classification). `Open`/`Put`/`Get`/... and the CLI are not.

## Requirements

* Go 1.22 or newer (developed with 1.27.3), 64-bit Windows (primary) or Linux.

## Build and test

```powershell
go test ./...
go vet ./...
go build -o .\kvdb.exe .\src\cmd\kvdb
```

`kvdb.exe` currently prints "not implemented" for every command.

Run a fuzzer:

```powershell
go test ./src/internal/format -run=^$ -fuzz=FuzzScanFrame -fuzztime=60s
```

## Documentation

* [docs/DESIGN.md](docs/DESIGN.md) — approved design (v5)
* [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — layout and decisions
* [docs/FILE_FORMAT.md](docs/FILE_FORMAT.md) — byte-level format
* [docs/TEST_PLAN.md](docs/TEST_PLAN.md)
* [docs/JUDGE_CLARIFICATIONS.md](docs/JUDGE_CLARIFICATIONS.md) — assumptions vs official answers
* [docs/RECOVERY.md](docs/RECOVERY.md), [BREAKING.md](BREAKING.md) — to be written in later phases
* [PROMPTS.md](PROMPTS.md) — AI prompt log (required)
