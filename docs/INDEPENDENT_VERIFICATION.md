# Independent verification of the file format

## What the independent implementation is

* **Repository:** <https://github.com/sharanyashetty013-droid/kipdb-recovery>
  (author: GitHub user `sharanyashetty013-droid`, team member responsible for
  recovery). Go module `kipdb`, package `recovery`. Pinned commit: `f0d8b9c`.
* It is a **separate implementation** of the same on-disk format (the
  superblock, frames, entries, CRC32C rules and torn-tail rules). It was
  written from the shared specification, not from NEXUS code. It contains
  recovery (scan, torn-tail handling, read-only `Verify`), an
  authority/lock layer and compaction. It has no write path, size cap or CLI.
* It is **not part of NEXUS** and is **not needed to build or run NEXUS**. We
  do not merge it. NEXUS's own recovery handles more crash states (authority
  rules A1–A7, including adoption of a sealed `.compact`/`.new`) and is the
  implementation the competition runs.
* We use it as an independent second opinion. If two implementations, written
  separately, agree byte for byte on encoding and on where the valid log ends,
  that is strong evidence that both follow the format exactly.

Its own test suite (30 tests) was run on the Windows judging machine during
the audit: `go test -count=1 -v ./...`, **30/30 pass** (its documentation said
it had only been run on Linux).

## The cross-check (`crosscheck/`)

`crosscheck/` is a separate Go module in this repository. The root
`go build ./...` and `go test ./...` do not include it, and NEXUS does not
import it. To run it (Git Bash on Windows, or any POSIX shell):

```bash
sh crosscheck/run.sh
```

The script clones the independent implementation at commit `f0d8b9c` into
`crosscheck/.recovery` (git-ignored). It builds `kvdb` from this repository
and runs eight groups of checks (nine lines) in a fresh temporary directory.
Expected output: nine `PASS` lines and `crosscheck: 9 checks, 0 failed`.

| # | Check | What it proves |
|---|---|---|
| 1 | `kvdb put` on a new file equals `EncodeSuperblock(1, 32)` + `EncodeFrame(1, put)`, byte for byte; a 4-op `kvdb batch` (PUT, PUT, DEL, PUT of the same key) equals `EncodeFrame(1, ops)` | identical constants (magic `KIPDBLOG`, version 1, frame magic `0xD8C3B1A5`, header sizes 32/32/11), little-endian layout, CRC32C coverage of superblock, header, payload and entry; same batch encoding and last-write-wins inside a batch |
| 2 | a database written through `kvdb serve` (torture-tool request format, two writers, batches, a 5,000-byte value) is accepted by the independent `Verify`, with the same frames, next sequence number and key/values as `kvdb dump` | NEXUS writes → independent recovery |
| 3 | the independent `Compact` on that file, then `kvdb check --no-repair`, `kvdb check` and `kvdb dump` | independent compaction → NEXUS reads it as a valid, sealed snapshot |
| 4 | NEXUS `put`/`del` after the independent compaction, then the independent `Verify` | NEXUS appends correctly after a foreign snapshot; the independent code reads NEXUS frames after its own snapshot |
| 5 | file created by the independent `Open(create)`, three frames written with its `EncodeFrame`; NEXUS `check --no-repair`, `dump`, `put`; independent `Verify` again | files created elsewhere → NEXUS reads and writes |
| 6 | a NEXUS file with its last frame torn at **every** byte: the independent `Verify` reports the torn tail at the same offset that `kvdb check --no-repair` names (exit 4, file unchanged); `kvdb check` then repairs to exactly the previous file | identical torn-tail rules; `--no-repair` is read-only |
| 7 | one byte flipped in the first frame's payload with valid frames after it: the independent code returns `ErrCorruption`, and NEXUS `check --no-repair` and `check` print `CORRUPT` and leave the file unchanged | identical "damage followed by data is corruption" rule |
| 8 | the same database compacted by NEXUS (`serve` … `compact`) and by the independent code: the files are byte-identical | deterministic, sorted compaction (DESIGN change #35) and identical snapshot encoding |

Results of the validation run: [results/validation/02-crosscheck.log](results/validation/02-crosscheck.log).

## Known differences (both safe)

| Area | NEXUS | Independent implementation |
|---|---|---|
| Interrupted compaction with `path` missing | adopts a sealed `.compact`, else `.old`, else a sealed `.new` (A3/A4/A5) | always rolls back to `.old` |
| Rename on Windows | `MoveFileExW(MOVEFILE_WRITE_THROUGH)`, never replaces | `MoveFileExW(REPLACE_EXISTING \| WRITE_THROUGH)`, only onto a name it just vacated |
| Lock | `<path>.lock` opened with share mode 0 | `<path>.lock` with `LockFileEx`; the two exclude each other |
| `check` | default: repair, then verify; `--no-repair`: read-only | `Verify` is read-only |
| Snapshot frames | up to 65,535 entries / 4 MiB payload per frame | up to 1,024 entries / 4 MiB per frame. Compacted files are byte-identical only below 1,024 live keys; above that both are valid but split frames differently |
