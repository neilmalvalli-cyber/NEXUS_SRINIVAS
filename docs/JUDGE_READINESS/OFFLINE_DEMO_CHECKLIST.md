# Offline demo checklist

Use this the evening before and again 10 minutes before judging. Everything listed works with no network: the Go build was verified with `GOPROXY=off` and empty caches, and the dashboard was verified with the network adapter off (N7, `offline-check-adapter-off.json`).

## The evening before

- [ ] **Commit and push all work** (you do this; the agent never commits). Until then a clone of GitHub lacks the C3 fix, the `src/cmd/torture` move, the seed fix and these docs. See FINAL_READINESS_REPORT.md, "Git history".
- [ ] After pushing, do a fresh clone **into a new folder** and run the build line plus one short torture run:
  ```bash
  git clone https://github.com/neilmalvalli-cyber/NEXUS_SRINIVAS nexus-check && cd nexus-check
  go build -o . ./src/cmd/kvdb ./src/cmd/torture
  ./torture --iterations 20 --seed 1
  ```
- [ ] `go version` shows 1.22 or newer.
- [ ] In **PowerShell**, run `./torture --seed abc`: it must print `torture: --seed "abc" is not an integer …` (exit 2). Then run `./torture --iterations 3 --seed 1`. The agent could not test PowerShell.
- [ ] Laptop on mains power, sleep disabled for the session, antivirus scan not scheduled.
- [ ] Keep `frontend/node_modules` on the laptop (do not delete it), so the dashboard works offline. A clean clone has no `node_modules`, and `npm install` needs the network.

## 10 minutes before

- [ ] Wi-Fi off / Airplane mode on (optional; nothing needs the network).
- [ ] Open two terminals in the repository root (Git Bash or PowerShell).
- [ ] Terminal 1: `go build -o . ./src/cmd/kvdb ./src/cmd/torture`. It works with no network, and the build cache is warm, so it takes a couple of seconds.
- [ ] Delete old demo files if you want a clean start: `judge.db`, `judge.db.lock`. Never delete `demo-data/` or the tracked `judge-demo.nexus`.
- [ ] Have FINAL_COMMAND_REFERENCE.md open, plus CRASH_HERE_ANSWERS.md and EXPIRY_EXTENSION_DESIGN.md.
- [ ] Have `src/internal/storage/log.go` open at line 374 and `src/internal/storage/compact.go` at line 263 for the crash-here questions.

## Optional dashboard

- [ ] `go build -o bin/ ./src/cmd/...`
- [ ] `bin/nexus-api --db demo-run/nexus.kv` (listens on 127.0.0.1:8080 only, so no firewall prompt)
- [ ] `NEXT_TELEMETRY_DISABLED=1 npm --prefix frontend run dev`, then open http://localhost:3000 and check the sidebar shows **Backend: Connected**.
- [ ] The torture page needs `nexus-torture` built next to `nexus-api` (the `./src/cmd/...` build line does not include it): `go build -o bin/ ./torture2/cmd/nexus-torture`.

## Timing reality check

| Step | Measured here |
|---|---|
| Build (cold cache, offline) | 8 s |
| `./torture --iterations 200 --seed <any>` | about 70 s |
| `--mode compaction`, 200 iterations | about 160 s |
| `--mode recovery-crash`, 200 iterations | see CRASH_SCENARIO_RESULTS.md: several minutes, because each iteration preloads and verifies 200,000 keys |

If the slot is tight, start the two mode runs in **two terminals at the same time**, or offer `--iterations 50`. Never cut a run short with Ctrl+C and then call it PASS.
