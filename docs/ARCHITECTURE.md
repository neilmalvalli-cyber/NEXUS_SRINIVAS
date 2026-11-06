# Architecture

The full design is in [DESIGN.md](DESIGN.md) (sections 1–28; sections 8–27
were added from a separately supplied continuation; §28 is the change log).

## Summary

Append-only log + in-memory hash index. Every mutation (put, delete, batch) is
**one checksummed frame**; atomicity comes from the format, not from commit
markers. Commit order: validate → exclusive lock → admission → write frame →
`Sync` → update index → acknowledge. At most one uncommitted frame exists, and
it is always the last one (invariant I1, DESIGN §2.4).

Open order: **process lock** → authority table A1–A7 → superblock → frame scan →
tail rules C0–C8 → ready (DESIGN §8, §11.4; checklist in [RECOVERY.md](RECOVERY.md)).

## Package layout

```
src/
  cmd/kvdb/            CLI entry point (Phase 1: placeholder)
  kvdb/                public API: DB, Op, errors (Phase 1: Op + errors only)
  internal/
    format/            on-disk codec, CRC32C, limits, error classes   <- done
    platform/          OS-specific primitives: MoveNoReplace,
                       IsDiskFull, IsSharingViolation                 <- done
    dberr/             sentinel errors shared by all packages         <- done
    storage/           commit path, truncate-on-failure               (Phase 2)
    recovery/          authority resolution, scan, tail rules         (Phase 2/3)
    compaction/        snapshot + two non-overwriting moves           (Phase 3)
    index/             key -> {offset, len}                           (Phase 2)
    admission/         max_bytes policy [ASSUME]                      (Phase 3)
    locking/           process lock file                              (Phase 2)
    cli/               command implementations                        (Phase 4)
tests/                 cross-package / crash tests (later phases)
```

### Deviations from the suggested layout, and why

| Change | Reason |
|---|---|
| Added `src/kvdb` | The required API (`Open`, `*DB`, `Op`) must be importable by tests and tools. Anything under `internal/` cannot be the public surface. |
| Added `src/internal/dberr` | Every internal package must return the public errors (`ErrFull`, `ErrCorruption`, ...). Defining them in `kvdb` would create an import cycle (`kvdb` imports `storage`, which would import `kvdb`). A leaf package that `kvdb` re-exports breaks the cycle. |
| Added `src/internal/platform` | Keeps every OS-specific detail (Windows error numbers, `MoveFile`, Unix errnos) in `_windows.go` / `!windows` files of one package, so storage, recovery and compaction contain no OS-specific code (DESIGN invariant 22). |
| Go module at repo root, code under `src/` | Keeps the requested `src/` layout while `go test ./...` from the root covers everything. |

## Windows API decisions (verified on Go 1.27.3, windows/amd64)

Checked in the installed `GOROOT/src` and by tests on this machine, 2026-11-06:

| Need | Finding | Decision |
|---|---|---|
| Non-overwriting move (R1, R2, adoptions) | `os.Rename` = `MoveFileEx(..., MOVEFILE_REPLACE_EXISTING)`: **silently replaces** (pinned by `TestOsRenameReplacesOnWindows`). Public `syscall.MoveFile` (`MoveFileW`) fails with `fs.ErrExist` if the destination exists. | **`platform.MoveNoReplace` → `syscall.MoveFile`.** `os.Rename` is forbidden for these moves. |
| Moving an open file | Go opens files without `FILE_SHARE_DELETE`; moving a file while it is open fails with a sharing violation (pinned by `TestOpenFileBlocksMove`). | Close the data-file handle before R1 (DESIGN §10.2 step 10). Retry sharing violations with bounded backoff in the caller. |
| Process lock (`.lock`) | `LockFileEx` is only in `internal/syscall/windows` (not importable). `syscall.CreateFile` with share mode 0 gives an exclusive handle; a second open fails with `ERROR_SHARING_VIOLATION` (pinned by `TestExclusiveShareModeLock`). | Exclusive share mode, held until Close or process death; never delete `.lock`. Limitations in DESIGN §16.1. Implemented in Phase 2 (`locking`). |
| Flush | `(*os.File).Sync` calls `FlushFileBuffers`. | Use `Sync`. |
| Error codes | 32, 33, 39, 112 are not defined in the public `syscall` package. | Declared only in `platform/errors_windows.go`; exposed as `IsDiskFull` / `IsSharingViolation`. |

## Error mapping (format → public errors)

| Situation | Public error |
|---|---|
| Superblock of `path` / `.old` fails to decode (any class) | `ErrBadFormat` |
| `snapshot_end` > size; tail rules C0, C6, C7; any decode failure on the Get path | `ErrCorruption` |
| Reader I/O error anywhere | the OS error, wrapped (never `ErrBadFormat`/`ErrCorruption`) |
| `ErrInvalid` / `ErrShortBuffer` (Usage class) | internal bug → returned wrapped; never reached from valid API calls because `kvdb` validates first |

Full definitions: DESIGN §13.3.

## Phase 1 decisions

1. **Decoders classify their errors** (Damaged / Corrupt / IO / Usage, see
   [FILE_FORMAT.md](FILE_FORMAT.md)). The recovery rules depend on this, so it
   is built into the codec and tested there.
2. **CRC before fields.** A field violation is reported only for bytes the
   checksum vouches for, so "CRC-valid but malformed" really means corruption.
3. **The payload scanner drains on structural errors** to finish the CRC, so a
   torn batch is never misreported as corruption (and vice versa).
4. **Entries from `ScanPayload` are tentative** until it returns nil; the caller
   buffers them. This is what keeps a torn batch all-or-nothing.
5. **Streaming encoder** (`WriteFrame`): two passes over the caller's slices,
   1 MiB staging buffer, values ≥ 256 KiB written straight from the caller's
   slice. Bytes reach the writer in file order, so a kill leaves a prefix.
6. **Standard library only.** `hash/crc32` provides hardware CRC32C. No
   dependencies. `unsafe` appears only inside the standard library's own
   `syscall` wrappers; our code does not import it.
