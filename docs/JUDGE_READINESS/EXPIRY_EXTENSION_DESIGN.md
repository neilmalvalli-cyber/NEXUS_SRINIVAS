# Design extension: keys that expire (TTL)

For judging minutes 12–15. **This is a design answer only. Nothing here is implemented**, and the binary format is unchanged. The aim is to show that expiry fits the existing crash-safety rules without weakening any of them.

## 30-second version (say this first)

> "Store an **absolute expiry time** next to the value, inside the same checksummed entry, so the value and its expiry are durable and atomic together. Reads treat an expired key as not found. Compaction physically drops expired keys. Recovery replays expiry like any other field. No background deleter is needed, and no new crash state appears, because nothing new is written outside the normal commit and compaction paths. It is a new entry type, so it is a format-version bump. Old binaries refuse new files with a clear 'unsupported version' error instead of misreading them, and existing files are upgraded by a compaction, which is already crash-safe."

## API

* `PutWithTTL(key, value, ttl)`. `ttl > 0` is required: zero or negative is `ErrInvalidArgument`, which avoids writing data that is born dead.
* Batch line: `putx <key> <value> <ttl-ms>`. A plain `put` on a key *clears* its expiry, and `del` works as today.
* `kvdb put` gains an optional `--ttl 30s`. In `serve`: `<w> <id> putx k v <ttl-ms>`.

## On disk

**A new entry op `3 = PUT_EXPIRING`.** Its data is `expires_at` (u64, Unix milliseconds, UTC), then key, then value. `val_len` counts the 8 extra bytes, and `entry_crc` covers them as it covers everything else (DESIGN §5.4). Frames, sequence numbers and the frame CRCs are unchanged.

**Why absolute time, not a TTL.** Recovery may replay a frame days later. With an absolute deadline, the replay result does not depend on when recovery runs.

**Version.**
* `FormatVersion` becomes 2.
* A v1 binary opening a v2 file stops at the superblock check (`format/superblock.go:122`) with `ErrUnsupportedVersion`, and changes nothing. It never misreads op 3.
* A v2 binary reads v1 files unchanged.
* The superblock is written once and never edited in place, so the **first expiring write to a v1 file is a compacting commit**. That existing path writes `.compact` with a v2 superblock, seals it last, and swaps it in under rules R1, R2 and A1–A7. A crash at any step leaves either the old v1 file or the complete v2 file. This reuses the mechanism behind crash-here answer 3, with no new crash states.

## Engine behaviour

| Area | Change |
|---|---|
| **Index** | Add `ExpiresAt int64` (0 = never) to `index.Loc`, or keep a side map of only the expiring keys to save 8 bytes per ordinary key. |
| **Get** | If `ExpiresAt != 0 && now ≥ ExpiresAt`, return NOTFOUND (lazy expiry). No write happens on a read. |
| **Recovery replay** | Replay op 3 like PUT, plus the deadline. Replay may skip inserting keys that are already expired; recovery stays read-and-index only and writes nothing new. |
| **Compaction snapshot** | Copy only keys with `ExpiresAt == 0 || ExpiresAt > now`, keeping their **original** absolute deadline. Expired keys disappear from disk, and space is reclaimed exactly as for deleted keys. |
| **Live-bytes / auto-compaction / maxBytes** | Count expired entries as reclaimable garbage, so a database full of expired keys auto-compacts and a capped database makes room (DISK_FULL_VALIDATION.md). |
| **dump / check / X-RAY** | `dump` skips expired keys. `check` verifies them (the checksums still apply) and reports "N expired". X-RAY decodes op 3. |

**Why no background expiry thread.** A background deleter would write DEL records: extra I/O, extra fsyncs, and another writer to reason about at every crash point. Lazy reads plus compaction give the same visible result with no new write path.

## Correctness questions a judge may ask

* **"Crash right after an expiring put?"** It is the same frame mechanics as any put (answers 1 and 2): ACKed means durable, together with its deadline, because both sit under one entry CRC.
* **"Can an expired key come back?"** It is never readable after its deadline by this machine's clock. Compaction drops only the *latest* version, and older versions were already superseded, so no older value can resurface.
* **"What if the clock jumps backwards?"** Before compaction, a key could be readable again until the clock passes its deadline once more. After compaction has dropped it, it is gone for good. We document expiry as "not readable after T by the host's wall clock", recommend NTP, and never use expiry for correctness-critical ordering. A monotonic clock cannot be used, because it does not survive a restart.
* **"Expiry during a batch?"** Every op in a batch uses the same `now`, taken once at commit, so a batch is never half-expired.
* **"Does it change ACK semantics or `ErrOutcomeUnknown`?"** No. Expiry is data, not a new durability path.

## Testing plan (if implemented)

* Inject a clock (`func() time.Time`) into the engine, so tests and torture are deterministic.
* Unit tests: Get before and after the deadline; overwrite with a plain put clears expiry; compaction drops expired keys and keeps the original deadlines; a v1 binary refuses a v2 file; the first `putx` on a v1 file upgrades it.
* Existing crash sweeps (`TestCommitCrashSweep`, `TestCompactCrashSweep`, the authority sweeps) run unchanged, plus one sweep over the v1→v2 upgrading compaction.
* Torture: a `--mode expiry`, where the model holds deadlines and the verifier uses the same fixed clock.
* Compatibility test: every existing v1 test file still opens, and the X-RAY lab fixtures stay readable.

## Cost

8 bytes per expiring entry on disk, and 8 bytes per key in memory (or only for expiring keys, with the side map). One time comparison per Get. No extra fsync. About 300–400 lines of engine code, plus tests (an estimate).
