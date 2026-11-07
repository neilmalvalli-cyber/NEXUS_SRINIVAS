# Judge clarifications

This file keeps **official answers** apart from **our assumptions**. Nothing
moves into "Official answers" unless a judge actually said it.

## Official answers

_None received yet._

## Assumptions pending clarification

| # | Assumption | Where it matters |
|---|---|---|
| A1 | "1 MB" values means up to 1,048,576 bytes inclusive. | `format.MaxValueLen` |
| A2 | `max_bytes` policy: 0 = unlimited; values 1–31 are invalid; Open succeeds on an existing readable database even if it is already over a newly supplied cap; a commit that contains a PUT must leave the file within the cap (using a compacting commit if needed); a delete-only commit may run on an over-cap file but must strictly reduce its size (DESIGN §12.3; the Phase 1 prompt said "must never increase", which is effectively the same because a delete of a present key always removes at least 12 bytes, and a delete of an absent key is a no-op with no I/O); standalone compaction never grows the file; anything that cannot be admitted returns `ErrFull` with the committed database unchanged. **This is our design assumption, not an official clarification.** | `internal/admission` (`Decide`), `internal/storage` (net effect, compacting commit) — implemented in Phase 3; the transient `.compact` is not counted (question 2) |
| A3 | An empty batch is a successful no-op with no I/O. | write path |
| A4 | `path` names the data file (not a directory); lock/temp files sit beside it (`.lock`, `.new`, `.compact`, `.old`). | storage, recovery |
| A5 | Power-loss durability on Windows is not claimed; the crash model is process kill (`TerminateProcess`). We still call `FlushFileBuffers` before every acknowledgement. | durability |
| A6 | An existing `path` that is empty or shorter than 32 bytes is not one of our databases (we only ever create `path` by moving a complete `.new` into place), so Open returns `ErrBadFormat` and does not initialise it. | authority rule A2 |
| A7 | Bit-rot is outside the crash model [REQ §5 G]. A damaged frame header is treated as the torn final frame and truncated (rule C2) unless a complete valid frame follows it; then open refuses (rule C3, amended 2026-11-07), so mid-file header damage is detected instead of truncated. | recovery |
| A8 | (implemented in Phase 3) A commit whose net effect on the live state is empty (e.g. a batch that only deletes absent keys, or `PUT X, DEL X` for an absent X) succeeds with no I/O. Without this, such a batch on an over-cap, already-compact database could not "strictly reduce" the file and would wrongly return `ErrFull`. | write path, admission |
| A9 | A second Open of a locked database, including from the same process, returns `ErrLocked` immediately after a short bounded retry (about 1 s); it does not wait for the lock. | locking |
| A10 | (Phase 4) The serve input format is `<writer-id> <op-id> <command ...>` (writer ID optional), batches as `batch <n>` followed by n op lines, plus `get` and `compact`; replies besides `ACK <op-id>` are `VALUE`, `NOTFOUND` and `ERR <op-id> <class> <message>`. The statement only fixes `ACK <op-id>`. | `internal/cli/serve.go`, torture tool |
| A11 | (Phase 4) `--max-bytes` applies to the invocation that receives it (open, put, del, batch, serve); it is not stored in the file. | CLI |
| A12 | (Phase 4) `kvdb check` runs normal recovery first (a torn, never-acknowledged tail is cut and reported on stderr, as `open` would), then verifies the whole file; it prints `OK` or a reason and never repairs corruption. | CLI |
| A13 | (Phase 4) `kvdb open` prints `keys=<n> recovery_ms=<ms> size=<bytes>`; recovery_ms is the in-process duration of Open (lock, authority, scan, truncation), excluding process start-up. | CLI |

## Questions to ask the judges

1. Is 1 MB = 1,000,000 or 1,048,576 bytes? (A1)
2. Does `max_bytes` cap the data file only, or also the transient `.compact` file during compaction?
3. Must Open succeed on a database already larger than a newly supplied `max_bytes`? (A2)
4. May a delete be rejected with "full", or must deletes always succeed when they do not grow the file? (A2)
5. Are the extra files (`.lock`, `.new`, `.compact`, `.old`) all acceptable under "exactly one authoritative data file"?
6. Is the CLI expected to keep the database open across commands (`serve`), or is each command a separate process?
7. If the judge creates an empty file and passes it to `open`, should it be initialised as a new database or rejected? (A6)
8. Is bit-rot (random corruption in the middle of the file) ever injected, or only process kills? (A7)
9. Should `open` on a locked database fail immediately (`ErrLocked`) or wait for the lock? (A9)
10. Will antivirus (Windows Defender real-time scanning) be enabled on the judging laptop? It affects lock and rename retries (DESIGN §16.1, §25).
