# Recovery

Status: implemented (Phase 2–3) in `src/internal/recovery`; this page is the checklist it follows.

The normative rules live in [DESIGN.md](DESIGN.md); this page is the
implementer's checklist, with the reasoning behind each rule. Section references
are to DESIGN.md. Test IDs refer to [TEST_PLAN.md](TEST_PLAN.md).

## Open sequence

| Step | Action | Rule source |
|---|---|---|
| 0 | Acquire `path.lock` (exclusive share mode). Failing → `ErrLocked`, no file touched. | §8 step 0, §16.1 |
| 1 | Resolve authority with table A1–A7. | §11.4 |
| 2 | Decode the superblock; check `snapshot_end ≤ size`. | §5.2, §13.3 |
| 3 | Scan frames from offset 32 with a 4 MiB buffered reader; expected seq starts at `first_seq`. | §8, §9 |
| 4 | Buffer each frame's entries from `format.ScanPayload`; apply them only if it returns nil. | §8.1 |
| 5 | At the first failure, apply tail rules C0–C8. | §8.2 |
| 6 | Truncate only under C1/C2/C4/C5, to exactly `pos`, then `Sync`. | §8.2 |
| 7 | Recompute counters; `next_seq` = last seq + 1 via `format.NextSeq`. | §9 |

## Why the lock comes first

Authority resolution renames and deletes files (A1 cleanup, A3–A5 adoptions,
A7 creation), and tail repair truncates. If two processes open at the same time
and both do this, one could delete a `.compact` that the other is about to
adopt, or truncate while the other scans. The lock is therefore step 0, and an
Open that cannot get it changes nothing.

## Invariant I1 (§2.4)

At most one uncommitted frame exists, and it is always the final frame. Every
tail rule below depends on it:

* damage that ends exactly at EOF is consistent with the single in-flight frame,
  so it may be truncated;
* damage followed by more bytes contradicts I1, so it cannot be a killed write
  and must be refused.

## Tail rules (summary of §8.2)

| Rule | Condition at frame start `pos` | Decision |
|---|---|---|
| C0 | `pos < snapshot_end` | Refuse, `ErrCorruption` |
| C1 | `size − pos < 32` | Truncate to `pos` |
| C2 | Bad magic or bad header CRC, and no complete valid frame after it | Truncate to `pos` |
| C3 | Bad magic or bad header CRC, and a complete valid frame (seq ≥ expected) follows | Refuse, `ErrCorruption`; never apply or truncate past a damaged header |
| C4 | Valid header, payload runs past EOF | Truncate to `pos` |
| C5 | Valid header, bad payload CRC, declared end == `size` | Truncate to `pos` |
| C6 | Valid header, bad payload CRC, declared end < `size` | Refuse, `ErrCorruption` |
| C7 | Wrong seq, or Corrupt class (CRC-valid malformed / entry CRC) | Refuse, `ErrCorruption` |
| C8 | I/O error | Fail Open, file untouched |

Format-layer support already in place (Phase 1): `format.HeaderFits`,
`format.PayloadFits`, `format.Classify` (Damaged / Corrupt / IO / Usage),
`format.ScanPayload` (drains a structurally bad payload to decide Damaged vs
Corrupt by its CRC), `format.NextSeq`.

### Rule C3: refuse when a valid frame follows a damaged header (amended 2026-11-07)

Before the amendment C3 said "never scan forward": every damaged header was
treated as the torn final frame and truncated (C2). That silently dropped
committed frames when a header in the *middle* of the log was damaged (judge
attack P7: one flipped bit in frame 3's header made a normal open delete
frames 3–8).

Amended rule. When a header is damaged at `pos` (outside the snapshot),
recovery looks for a **complete, fully valid frame** after it: header CRC
valid, `seq >= expected`, payload inside the file, payload CRC valid
(`recovery.LaterCommittedFrame`). If one exists, open **refuses** with
`ErrCorruption` (rule C3) and changes nothing. Otherwise the damaged header
is the torn final frame and C2 truncates as before. Nothing past a damaged
header is ever applied; the forward check is evidence only.

Why this cannot refuse an ordinary crash. Frames are written one at a time
and each is synced before the next starts (I1), so a torn write only ever
affects the last frame, and a process kill leaves a *prefix* of it. A prefix
with 32 or more bytes has an intact header, so it is handled by C4/C5, not
C2; a shorter one is C1. The forward check therefore runs only when all 32
header bytes are present but wrong.

Remaining trade-off (KNOWN_LIMITATIONS.md). The old C3 rationale still holds
in one narrow case: a user value can contain bytes forming a valid frame. If a
power loss persisted the last frame's sectors out of order, losing the header
sector but keeping later ones, *and* that frame's value embedded a complete
valid frame with a future sequence number, open now refuses a file our own
engine wrote. That outcome is a refusal with the file untouched (X-RAY
explains it), never data loss; we prefer it to silently truncating committed
data on any mid-log header damage. Pinned by
`TestTailC3CraftedValueBehindLostHeaderRefusesSafely`.
`format.TestTornFrameContainingEmbeddedFrameHeader` still holds: a value full of
valid-looking headers never changes how a *prefix-torn* frame is classified.

### Why C5 requires "exactly EOF"

A killed write leaves a prefix of the last frame. If the header is intact and
the payload length fits inside the file, the only way the CRC can fail on a torn
write is if the file ends exactly where the frame was meant to end, with
unwritten or zero-filled bytes inside it (for example when the file size was
extended before the data landed). If bytes follow the frame, I1 says it was not
the in-flight frame, so it is refused (C6).

## Authority table (summary of §11.4)

Precondition: lock held. "Valid"/"sealed" = superblock decodes and
`snapshot_end ≤ size`.

| Rule | State | Action |
|---|---|---|
| A1 | `path` valid | Use it; delete `.compact`, `.old`, `.new` best-effort |
| A2 | `path` exists, invalid | `ErrBadFormat`; touch nothing |
| A3 | `path` missing, `.compact` sealed | Move `.compact → path`; restart |
| A4 | `path` missing, `.old` valid | Move `.old → path`; restart |
| A5 | `path` missing, `.new` sealed | Move `.new → path`; restart |
| A6 | `path` missing, `.old` exists but invalid | `ErrBadFormat`; touch nothing |
| A7 | `path`, `.old` missing; temps missing/unsealed | Delete unsealed temps; create fresh via `.new`; restart |

All moves use `platform.MoveNoReplace` (`MoveFileExW` with `MOVEFILE_WRITE_THROUGH`, never `MOVEFILE_REPLACE_EXISTING`), never `os.Rename`.

`kvdb check --no-repair` (`recovery.Inspect`) walks the same table read-only:
it reports which rule `Open` would apply and scans the file that is or would
become the database, but moves, creates, truncates and deletes nothing.
Restarts are capped at 3.

## Crash during recovery

Recovery changes files only by (a) non-overwriting moves, (b) deleting files
the table has just shown are not authoritative, (c) creating a fresh database
via `.new`, and (d) one tail truncation followed by `Sync`. A kill at any point
leaves a state from which re-running Open reaches the same database (T-AUTH-11,
T-REC-12).
