# Clean clone verification

**Method.** On 2026-11-07, every file Git would commit (`git ls-files -co --exclude-standard`, without `node_modules`) was copied into an empty folder: 371 files, 0 executables. That is what a clone will contain **after the working tree is committed**. It was built with empty `GOCACHE` and `GOMODCACHE`, plus `GOPROXY=off GOTOOLCHAIN=local` (no network possible):

```bash
go build -o . ./src/cmd/kvdb ./src/cmd/torture     # rc 0, 6 s
./torture --seed abc                                # refused, exit 2
./torture --iterations 10 --seed 4242               # RESULT: PASS 10/10
./torture --iterations 5 --seed 4242 --mode compaction       # PASS 5/5
./torture --iterations 3 --seed 4242 --mode recovery-crash   # PASS 3/3
```

**The current GitHub HEAD (451b7df) fails this.** There, `./torture` is a folder (`./torture: Is a directory`) and the README build line does not build it. It passes only once the working tree is committed and pushed.

**Judging OS.** Not stated by the organisers. Verified on Windows 11 (Git Bash and `cmd`). The code is standard-library Go with Windows and Unix file-system paths. Linux and macOS were not run here.

**Requirements.** Go ≥ 1.22. No modules, no network, no C toolchain. Executables: `kvdb` and `torture` (`.exe` on Windows), in the repository root.

**After you push**, do this once:

```bash
git clone https://github.com/neilmalvalli-cyber/NEXUS_SRINIVAS x && cd x && go build -o . ./src/cmd/kvdb ./src/cmd/torture && ./torture --iterations 5 --seed 1
```
