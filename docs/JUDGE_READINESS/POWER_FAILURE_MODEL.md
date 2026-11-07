# Power failure model: what is claimed, what is simulated, what is not tested

**Bottom line for judges.** NEXUS has **not** been tested with a physical power cut (attack PC4, **NOT TESTED**). Every power-loss statement below is either:
* a consequence of the sync-before-ACK design, or
* evidence from a **simulation** (the Power-Cut Explorer).

Process kills, by contrast, are **real**: thousands of `TerminateProcess` kills, including 150 kills of nexus-api mid-HTTP-write in this audit. A process kill does not lose the OS write cache, so it is a weaker event than power loss.

## 1. What the engine relies on

| # | Assumption | Where it comes from |
|---|---|---|
| A | `FlushFileBuffers` returns only after the data is on stable storage | Windows API contract; true only if the disk and controller honour flushes (no lying write cache) |
| B | A write is acknowledged only after its frame has been synced (sync before ACK) | NEXUS design (DESIGN §2.4, I1); verified by torture, scenarios and O8 |
| C | At most one unsynced frame, always the last one | invariant I1 |
| D | Renames are atomic, and `MoveFileExW(MOVEFILE_WRITE_THROUGH)` makes them durable | NTFS behaviour. Windows has no directory fsync, so durability of a finished rename after a power cut is **assumed, not tested** (FAILURE_GUARANTEES.md) |

## 2. The simulated model (Power-Cut Explorer)

The Explorer records every write, sync, create, rename, truncate and directory operation of a real workload. It then builds disk images of what a power cut at each point could leave, and opens each image with the real recovery code. Its model, quoted from its report:

* **M1:** synced data is durable: writes to a file issued before a completed fsync (FlushFileBuffers) survive.
* **M2:** 512-byte sectors are atomic: each aligned sector is entirely old or entirely new.
* **M3:** unsynced writes may be lost, kept, or kept partially (whole sectors), in any combination and order.
* **M4:** directory operations are atomic and ordered; durable after SyncDir, otherwise any ordered prefix survives.
* **M5:** file data and directory entries survive independently of each other.

**Not modelled** (from the same report):
* disks that acknowledge a flush they did not perform (exercised only as a negative control);
* torn or garbage sectors, bit rot, misdirected or phantom writes (one bit flip is exercised as a negative control);
* filesystem metadata corruption or journaling bugs; M4 is an assumption about NTFS;
* crashes during recovery itself (covered by the torture framework's recovery-kill modes);
* every possible interleaving of concurrent writers (one recorded interleaving per run).

**Evidence in this audit.** All of it is simulated.

| Run | Images | Failures | Controls detected |
|---|---|---|---|
| Recorded default, seed 1 (before the audit) | 1,278 | 0 | 6/6 |
| After the P7 fix: seeds 1, 2, 3 | 1,253 + 1,351 + 1,333 = 3,937 | 0 | 18/18 |
| PC3 replay: seed 11 ×3 with 4 writers, ×2 with 1 writer | 1,312 / 1,325 / 1,249 / 623 / 623 | 0 | 30/30 |

The negative controls are an fsync that lies and a single bit flip. They violate the model and must be detected. All were detected, so the oracle can fail.

## 3. How the P7 fix interacts with power loss

* Rule C3 (amended 2026-11-07) refuses, and never truncates, when a complete valid frame follows a damaged header.
* Under M2/M3, a power cut can leave the *last* frame with its header sector lost and later sectors kept.
* If that frame's value also embeds a complete valid frame with a future sequence number, open refuses a file NEXUS wrote. That is a loss of availability with the file untouched, never data loss.
* This is documented as KNOWN_LIMITATIONS L1 and pinned by `TestTailC3CraftedValueBehindLostHeaderRefusesSafely`. Physical reproduction is **NOT TESTED**.

## 4. What a judge can and cannot conclude

**Can conclude:**
* Every acknowledged write is synced first.
* Real process kills at any moment lose no acknowledged write and produce no torn or phantom value (torture, the 32 scenarios, O8).
* Within the simulated sector/flush model, sampled power-cut states recover correctly.

**Cannot conclude:**
* survival of a physical power cut on any particular hardware;
* behaviour with disks that lie about flushes;
* durability of a completed rename across a power cut;
* protection against bit rot (detected by checksums and refused, never repaired; see FAILURE_GUARANTEES.md).

**To test physically (PC4):** you need a machine you can cut power to (ideally with a disk whose write cache is disabled or power-loss protected). Run `kvdb serve` with the torture workload, pull the plug, then verify with `nexus-torture`'s oracle. This has not been done and is not claimed.
