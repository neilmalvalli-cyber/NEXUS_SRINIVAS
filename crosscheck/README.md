# crosscheck

Tests NEXUS against an independent implementation of the same file format
(the teammate recovery package, `kipdb-recovery` @ `f0d8b9c`).

```bash
sh crosscheck/run.sh
```

Expected: nine `PASS` lines and `crosscheck: 9 checks, 0 failed`.

This is a separate Go module. NEXUS does not depend on it, and the root
`go build ./...` / `go test ./...` ignore it. Details and the meaning of each
check: [docs/INDEPENDENT_VERIFICATION.md](../docs/INDEPENDENT_VERIFICATION.md).
