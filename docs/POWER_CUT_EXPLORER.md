# Power-Cut Explorer

`src/internal/powercut`, `src/cmd/nexus-powercut`, and the dashboard page
Reliability Lab. It is a separate module: the engine and the torture framework
are unchanged.

## How it works
1. **Record.** A `fileio.FS` wrapper records every create, write (with its
   bytes), truncate, sync, move, remove and directory sync that the real engine
   issues during a seeded concurrent workload (puts, overwrites, batches,
   deletes, compactions). Order is exact because all mutations happen under
   the engine's `wmu`.
2. **Generate.** For each chosen crash point (a prefix of the trace), build
   disk images under the model below. Variant families: all-pending-persisted,
   only-synced-state, sector prefixes (torn writes), random subsets,
   directory-op prefixes, dir-synced-only plus all data.
3. **Validate.** Open each image with the real `kvdb.Open`, run `Verify`, and
   apply an oracle: no phantom values, every acknowledged put/batch/delete
   durable, batches all-or-nothing, overwrites of hot keys in order.
4. **Diagnose.** A failing image is kept before recovery and scanned by X-RAY
   (`xray.json`). Negative controls (fsync lie, bit flip), which lie outside
   the model, must be detected.

## Storage failure model (assumptions)
* M1. Synced data is durable.
* M2. Aligned 512-byte sectors are atomic.
* M3. Unsynced writes may be lost, kept, or partially kept (whole sectors), in any combination.
* M4. Directory operations are atomic and ordered. They are durable after SyncDir; otherwise an ordered prefix survives.
* M5. File data and directory entries survive independently.

## Not modeled (coverage limits)
* Disks that acknowledge flushes they did not perform (only as a negative control).
* Torn or garbage sectors, bit rot, misdirected writes (one bit flip as a control).
* Filesystem metadata bugs. On Windows SyncDir is a no-op, so M4 is an assumption about NTFS.
* Crashes during recovery (covered by the torture framework).
* Interleavings other than the one recorded per run.

**A run is a finite, seeded sample of modeled states. Passing it is evidence,
not an exhaustive proof that NEXUS survives every possible power loss.**

## Reproducibility
`nexus-powercut --seed 1 --writers 4 --ops 60 --max-points 400 --max-variants 8`
regenerates the same workload choices, crash-point sample and variants. Thread
scheduling can change the recorded interleaving, so the trace itself can
differ. `report.json` records config, environment and the exact command.
Recorded default run ([results/optimization/powercut-default-seed1.json](results/optimization/powercut-default-seed1.json)):
* trace: 290 events, 240/240 operations acknowledged;
* 291/291 crash points, 1,278 images, 0 failures;
* 6/6 controls detected; 9.3 s.

Exit codes: 0 pass, 1 failure or undetected control, 2 usage error.
