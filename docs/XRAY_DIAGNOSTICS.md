# X-RAY diagnostics: findings reference

X-RAY (`src/internal/xray`, `kvdb xray`) explains what is wrong with a NEXUS
database: **what** (a code and title), **where** (file, byte offset, frame
number, sequence), **why** (expected vs. actual values and the rule
involved), **what recovery would do** (recovery status), and **whether data
is at risk** (data impact). Architecture: [XRAY_ARCHITECTURE.md](XRAY_ARCHITECTURE.md).
HTTP API: [XRAY_API.md](XRAY_API.md).

Ground rules, enforced by the code and its tests:

* **Read-only by default.** Size, modification time and SHA-256 of the database
  are recorded before and after every scan, and the report says whether the
  file changed (`modified`). The lock is only probed if the `.lock` file
  already exists, so a scan creates nothing.
* **No invented findings.** Every finding comes from a decoded byte, a typed
  error (`errors.Is` / `errors.As` on `recovery.ScanError`,
  `recovery.RefusalError`, `format.FieldError`, `dberr` sentinels) or a
  computed checksum. No error message text is parsed.
* **Two independent views.** The engine's own verdict (`recovery.Plan`) and an
  independent byte walk. If they disagree, that is reported as
  `XRAY_ENGINE_DISAGREEMENT` instead of being hidden.
* **"Repaired" only after verification.** See [Repair](#repair).

## Finding fields

| Field | Meaning |
|---|---|
| `id` | Stable identifier: `CODE@offset` or `CODE@file`. |
| `code` | Machine code (tables below). |
| `severity` | `INFO`, `WARNING`, `ERROR` or `CRITICAL`. |
| `category` | `superblock`, `frame`, `sequence`, `operation`, `recovery`, `compaction`, `operational`, `summary`. |
| `title`, `simple`, `technical` | Short title, plain-language explanation, exact rule/field/value explanation. |
| `file`, `offset`, `frame`, `seq` | Location. `frame` is 1-based in walk order. |
| `expected`, `actual` | The value the format requires and the value found (for example computed vs. stored CRC). |
| `rule` | Recovery rule involved: tail rules C0–C8, authority rules A1–A7 ([RECOVERY.md](RECOVERY.md)). |
| `timestamp`, `component` | When it was detected; the engine component it concerns (for example `format/frame`, `recovery/authority`). |
| `data_impact` | What happens to data: none, an unacknowledged tail, or committed data at risk. |
| `recovery_status` | See below. |
| `recommended_action` | What to do. |
| `original_error` | The engine's error text, verbatim, when there was one. |

### Recovery status

| Status | Meaning |
|---|---|
| `not_applicable` | Nothing to recover. |
| `recoverable` | A normal `kvdb.Open` would fix this automatically under the documented rules (truncate a proven torn tail, finish or roll back an interrupted compaction/creation). Nothing has been changed yet. |
| `detected` | Reported, but no automatic action is safe or applicable (for example a held lock, or a damaged header with committed frames behind it). |
| `refused` | The engine refuses to open the file and leaves it untouched (mid-log corruption, invalid content, unsupported version). |
| `unrecoverable` | Nothing can be read from the file (superblock unreadable). |
| `repaired` | X-RAY repair ran *and* verification passed (backup made, `kvdb.Open` succeeded, `Verify` passed, fresh scan clean). |
| `recovered` | Reserved for the same verified outcome when an open was performed outside X-RAY's repair. |

There is deliberately **no "partially recovered"** status. NEXUS never
salvages part of a corrupt log: a frame is applied whole or not at all, and
mid-log corruption makes the open fail. A partial state would therefore be
something X-RAY invented, not something the engine does.

## Codes

### Superblock (offset 0; category `superblock`)

| Code | Severity | Recovery | Cause |
|---|---|---|---|
| `SB_TOO_SHORT` | CRITICAL | unrecoverable | File shorter than the 32-byte superblock. |
| `SB_BAD_MAGIC` | CRITICAL | unrecoverable | First 8 bytes are not the NEXUS signature (rule A2 on the database path). Expected/actual signature bytes. |
| `SB_CHECKSUM` | CRITICAL | unrecoverable | CRC32C over bytes [0:28) differs from the stored CRC at offset 28. Computed vs. stored. |
| `SB_UNSUPPORTED_VERSION` | CRITICAL | refused | CRC-valid header with a version other than 1. The data may be intact; open it with the version that wrote it. |
| `SB_MALFORMED` | CRITICAL | unrecoverable | CRC-valid header with a field value NEXUS never writes (`format.FieldError`). |
| `SB_SNAPSHOT_BEYOND_EOF` | CRITICAL | refused | `snapshot_end` points past the end of the file. |
| `SB_INVALID` | CRITICAL | unrecoverable | Any other superblock decode error. |

### Frames, sequence and operations

The walk classifies the first frame it cannot accept with the engine's own
tail rules. A problem inside the compacted snapshot region is always C0
(CRITICAL, refused). The severities below are for the log region.

| Code | Rule | Severity | Recovery | Cause |
|---|---|---|---|---|
| `FRAME_TORN_HEADER` | C1 | WARNING | recoverable | Fewer than 32 header bytes at the end of the file. |
| `FRAME_HEADER_DAMAGED` | C2 | WARNING, or **CRITICAL** | recoverable, or **detected** | Header magic or CRC fails. If a **later CRC-valid frame** exists (forward check), committed frames would be lost by a normal open (C3 forbids scanning past it), so the finding is raised to CRITICAL / detected and repair refuses. |
| `FRAME_PAYLOAD_TRUNCATED` | C4 | WARNING | recoverable | Header valid, payload cut by end of file. |
| `FRAME_PAYLOAD_CHECKSUM` | C5 | WARNING | recoverable | Payload CRC fails on the final frame, which ends exactly at EOF (a torn final write). |
| `FRAME_PAYLOAD_CHECKSUM` | C6 / C0 | CRITICAL | refused | Payload CRC fails on a frame with intact frames after it, or in the snapshot: corruption of committed data. |
| `FRAME_HEADER_MALFORMED` | C7 | CRITICAL | refused | CRC-valid header with impossible fields (lengths, counts). |
| `SEQ_MISMATCH` | C7 | CRITICAL | refused | CRC-valid frame whose sequence number is not the next one. |
| `SNAPSHOT_BOUNDARY` | C0 | CRITICAL | refused | A frame crosses the `snapshot_end` boundary. |
| `OP_MALFORMED` | C7 | CRITICAL | refused | Entry with an unknown op code (only PUT=1, DEL=2). |
| `OP_LENGTH` | C7 | CRITICAL | refused | Entry key/value lengths exceed limits or the payload. |
| `OP_ENTRY_CHECKSUM` | C7 | CRITICAL | refused | Entry CRC fails although the frame's payload CRC is valid. |
| `OP_TRAILING_BYTES` | C7 | CRITICAL | refused | Bytes left in a payload after the last entry. |
| `OP_COUNT_MISMATCH` | C7 | CRITICAL | refused | `op_count` differs from the entries decoded. |
| `OP_INVALID` | C7 | CRITICAL | refused | Any other entry decode error. |

### Recovery, compaction and creation (from `recovery.Plan`; category `compaction` / `recovery`)

| Code | Rule | Severity | Recovery | Cause |
|---|---|---|---|---|
| `COMPACTION_INTERRUPTED_SWAP` | A3 | WARNING | recoverable | Crash between rename R1 and R2: sealed `.compact` and `.old` present, path missing. Open adopts `.compact`. |
| `COMPACTION_RESTORE_OLD` | A4 | WARNING | recoverable | Path missing, `.old` intact. Open restores it. |
| `CREATION_INTERRUPTED` | A5 | WARNING | recoverable | Database creation interrupted (`.new` present). |
| `COMPACTION_LEFTOVER_COMPACT` / `_OLD` | A1 | WARNING | recoverable | Leftover temporary file next to a valid database; open removes it. |
| `CREATION_LEFTOVER` | A1 | WARNING | recoverable | Leftover `.new` next to a valid database. |
| `RECOVERY_TEMP_REFUSED` | rule from `RefusalError` (A2/A3/A5/A6) | CRITICAL | refused | The engine refuses the combination of files (`RefusalError`). Do not delete any temp file. |
| `ENGINE_IO_ERROR` | — | CRITICAL | — | I/O error while the engine planned recovery. |

### Operational and summary

| Code | Severity | Cause |
|---|---|---|
| `SCAN_CLEAN` | INFO | Every frame validated; nothing to do. |
| `DB_MISSING` | INFO | No database at the path (open would create one). |
| `SPACE_DEAD_BYTES` | WARNING | Dead bytes are more than 50% of the file and at least the auto-compaction minimum size (4 MiB). Tiny files are exempt because frame overhead alone exceeds 50%. |
| `LOCK_HELD` | WARNING / detected | The database is open in another process. The scan still runs read-only. |
| `XRAY_ENGINE_DISAGREEMENT` | ERROR | The byte walk and `recovery.Plan` reached different conclusions. |
| `XRAY_FILE_CHANGED` | WARNING | The file's fingerprint changed during the scan (someone else wrote it). |
| `XRAY_READ_ERROR` | WARNING | X-RAY could not read part of the file. |
| `OP_OUTCOME_UNKNOWN`, `OP_HANDLE_FAILED`, `OP_FULL`, `OP_ERROR` | ERROR | Typed errors returned to API callers (`xray.FromError`), recorded by nexus-api. |
| `OP_LOCKED` | WARNING | Lock conflict returned to a caller. |
| `OP_CLOSED` | INFO | Operation on a closed database. |
| `OP_CORRUPTION`, `OP_BAD_FORMAT` | CRITICAL | Corruption or bad format returned to a caller. |

NotFound, InvalidArgument and TooLarge errors are not findings: they are
normal answers, not database health.

### Health

`healthy` (no WARNING or worse), `attention` (warnings only, for example a
recoverable torn tail), `degraded` (ERROR), `critical` (CRITICAL). The
report-level `recovery.status` follows the worst finding. For example, a CRC-valid
unsupported version is `refused`, not `unrecoverable`.

## Repair

`kvdb xray <path> --repair --yes [--backup-dir DIR]` or `POST /api/xray/repair`
(demo and lab targets only, with `"confirm": true`):

1. Scan. Refuse unless every finding is `recoverable` (or clean), there is no
   later-frame evidence, and the database is not held open.
2. Copy the database and any `.compact` / `.old` / `.new` to the backup
   directory (exclusive create, fsync). If any copy fails, nothing is changed.
3. `kvdb.Open`, which applies the documented recovery. Then `Verify` and `Close`.
4. A fresh read-only scan. Findings are marked `repaired` only if the open and
   Verify succeeded and the rescan is clean. Otherwise the report says
   `verified: false` with the error.

Corruption the engine refuses is **never** repaired: X-RAY does not salvage,
rewrite or drop committed frames.

## Example (lab case `header-damaged-mid-log`)

One bit of frame 3's `payload_len` was flipped, so its header CRC fails. The
engine classifies this as C2 and, by C3, would truncate from frame 3 onward,
silently dropping five committed frames. X-RAY's forward check finds the
CRC-valid frame 4 behind it:

```
CRITICAL FRAME_HEADER_DAMAGED @ <offset of frame 3>   rule C2   recovery: detected
  data impact: a normal open would truncate N bytes, including at least one committed frame
  action: do NOT open normally; back up, then restore or keep for expert recovery (repair refuses)
```

`kvdb.Open` on this file does lose `user:6`, as shown by
`TestMidLogHeaderDamageWouldLoseCommittedFrames`. This is a documented engine
limitation that X-RAY makes visible. The engine was not changed in Prompt 19.

## Test coverage

`src/internal/xray/xray_test.go` builds 16 lab databases with the real API
and exact byte edits, and checks each diagnosis:

`clean`, `bad-magic`, `unsupported-version`, `superblock-checksum`,
`torn-tail`, `incomplete-header`, `final-payload-checksum`,
`committed-frame-corrupted`, `header-damaged-mid-log`, `invalid-seq`,
`invalid-op`, `entry-checksum`, `snapshot-corrupted`,
`interrupted-compaction`, `compaction-leftover`, `lock-conflict`.

The tests also check:

* every lab directory is byte-identical before and after the scan;
* refused cases also fail a real `kvdb.Open`;
* torn-tail and interrupted-compaction repair is verified;
* repair of corruption is refused;
* error mapping and the dead-bytes threshold.

`src/internal/cli/xray_test.go` covers the CLI. `src/cmd/nexus-api/xray_test.go`
covers the HTTP layer.
