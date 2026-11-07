# Live judge demo: rehearsed script (Phase 6)

Every command and output below was **run for real on 2026-11-07** with the current build, on throwaway copies. Outputs are quoted from that rehearsal (paths shortened). Run them in a scratch folder, never on a database you care about.

## Setup (2 minutes)

```bash
go build -o bin/ ./src/cmd/...
```

```bash
bin/nexus-api --db demo-run/nexus.kv
```

```bash
NEXT_TELEMETRY_DISABLED=1 npm --prefix frontend run dev
```

Open `http://localhost:3000`. The sidebar must show **Backend: Connected**. The dashboard and API work with the network adapter off (N7, verified).

Create a 6-frame database for the attacks:

```bash
for i in 1 2 3 4 5 6; do bin/kvdb put demo.kv user:$i value-$i; done
```

The result is 368 bytes: a 32-byte superblock plus six 56-byte frames, with frame 3 starting at offset 144.

## Attack 1: flip one bit in the middle of the file (the P7 attack, fixed in this audit)

```bash
cp demo.kv flip.kv
node -e "const f=require('fs');const b=f.readFileSync('flip.kv');b[160]^=1;f.writeFileSync('flip.kv',b)"
bin/kvdb get flip.kv user:6
```

Rehearsed output (exit 1):

```
kvdb get: ERROR (corruption): … flip.kv: frame at offset 144 (rule C3): format: header checksum mismatch …;
a complete valid frame (seq 4) follows at offset 200, so this is not a torn final write and truncating would drop committed data
```

* The file's SHA-256 was identical before and after. NEXUS refuses and changes nothing.
* `bin/kvdb xray flip.kv` showed: `health: CRITICAL  engine: refused`, `[CRITICAL] Damaged frame header with committed frames after it`.
* **Talking point:** before this audit, this exact flip made a normal open truncate frames 3–6 silently. Now it is refused, the committed frames stay on disk, and X-RAY and the engine agree.

## Attack 2: torn final write (what a crash really leaves)

```bash
cp demo.kv torn.kv
node -e "const f=require('fs');const b=f.readFileSync('torn.kv');f.writeFileSync('torn.kv',b.subarray(0,b.length-10))"
bin/kvdb check torn.kv --no-repair
bin/kvdb get torn.kv user:5
bin/kvdb get torn.kv user:6
```

Rehearsed output:

```
NEEDS REPAIR: open would discard a torn 46-byte tail at offset 312 (rule C4); it was never acknowledged      (exit 4, file not modified)
recovery: discarded a torn 46-byte tail (rule C4); it was never acknowledged
value-5
NOTFOUND
```

The file is 312 bytes after open. **Talking point:** a real crash can only tear the last, unacknowledged write; everything before it survives. In this demo we cut an acknowledged frame by hand to *create* the torn state, which a real crash cannot do to an acknowledged write (sync before ACK).

## Attack 3: open the database from two processes

```bash
(sleep 3 | bin/kvdb serve demo.kv > /dev/null &)
sleep 1
bin/kvdb get demo.kv user:1
```

Rehearsed output (exit 1):

```
kvdb get: ERROR (locked): kvdb: database is locked by another process: demo.kv.lock (still busy after 15 attempts) …
```

After the first process exits, the same command printed `value-1`. With nexus-api it behaves the same way: a second instance exits "locked" after about 1 s, and the first keeps serving (O7).

## Attack 4: kill the server mid-write (in the dashboard)

* In the Database Explorer, start a PUT, then kill nexus-api (Ctrl+C, or `taskkill /F /IM nexus-api.exe`).
* The UI shows **Backend disconnected** and disables the actions.
* Restart nexus-api: the UI reconnects by itself within a few seconds.
* Any write whose answer was lost is shown as **outcome unknown**, never "failed". Read the key back.
* Evidence at scale: 150 real kills during 94,773 HTTP writes, 0 lost acknowledged writes (O8):

  ```bash
  node frontend/scripts/api-kill-test.mjs --api bin/nexus-api.exe --dir NEW_DIR --iterations 10
  ```

## Attack 5: power cut (simulated)

Open the **Reliability Lab** page, or run:

```bash
bin/nexus-powercut --seed 1
```

* Say clearly: *this is a simulation of a sector/flush model; a physical power cut has not been tested* (POWER_FAILURE_MODEL.md).
* For an exactly repeatable run, use `--writers 1` (PC3).

## Questions judges may ask

| Question | Short answer | Evidence |
|---|---|---|
| Does a 200 / ACK mean durable? | Yes: the frame is fsynced before the answer. | O8: 93,104 acknowledged HTTP writes, all present after a kill |
| What if the network drops after the write? | The UI says "outcome unknown" and never retries automatically. | N2/N3/N4 |
| Physical power loss? | Not tested; simulated only. | PC4, POWER_FAILURE_MODEL.md |
| Read-only database? | No read-only open mode; use `xray` or `check --no-repair`. | KNOWN_LIMITATIONS L2 |
| Race detector? | Not run: no C toolchain on this machine. | C2, KNOWN_LIMITATIONS |
