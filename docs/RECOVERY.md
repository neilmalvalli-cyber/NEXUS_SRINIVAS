# Recovery

Status: **specification only. Recovery is implemented in Phase 2.**

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
| C2 | Bad magic or bad header CRC | Truncate to `pos` |
| C3 | — | Never scan forward past a damaged header |
| C4 | Valid header, payload runs past EOF | Truncate to `pos` |
| C5 | Valid header, bad payload CRC, declared end == `size` | Truncate to `pos` |
| C6 | Valid header, bad payload CRC, declared end < `size` | Refuse, `ErrCorruption` |
| C7 | Wrong seq, or Corrupt class (CRC-valid malformed / entry CRC) | Refuse, `ErrCorruption` |
| C8 | I/O error | Fail Open, file untouched |

Format-layer support already in place (Phase 1): `format.HeaderFits`,
`format.PayloadFits`, `format.Classify` (Damaged / Corrupt / IO / Usage),
`format.ScanPayload` (drains a structurally bad payload to decide Damaged vs
Corrupt by its CRC), `format.NextSeq`.

### Why no forward scan (C3)

A user value can contain bytes that form a perfectly valid frame header, even
one with the next expected sequence number. If a frame carrying such a value is
killed partway through and recovery searched forward for "another valid frame",
it would find the fake one and refuse to open a file our own engine wrote.
`format.TestTornFrameContainingEmbeddedFrameHeader` pins down the format-level
half of this. The cost is accepted risk: real bit-rot behind a damaged header is
not detected (outside the official crash model).

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

All moves use `platform.MoveNoReplace` (`syscall.MoveFile`), never `os.Rename`.
Restarts are capped at 3.

## Crash during recovery

Recovery changes files only by (a) non-overwriting moves, (b) deleting files
the table has just shown are not authoritative, (c) creating a fresh database
via `.new`, and (d) one tail truncation followed by `Sync`. A kill at any point
leaves a state from which re-running Open reaches the same database (T-AUTH-11,
T-REC-12).
