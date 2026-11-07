# Tests

Go requires tests to sit next to the code they test, so the tests live in the
source tree:

| What | Where | Run |
|---|---|---|
| Unit, fault-injection and crash-sweep tests | `src/internal/*/*_test.go`, `src/kvdb/*_test.go` | `go test ./...` |
| CLI and serve protocol | `src/internal/cli/cli_test.go` | `go test ./src/internal/cli` |
| The 32 official scenarios (A1–F4) | `src/scenarios` | `go test ./src/scenarios -v` (quick) or `KVDB_SCENARIOS=full ...` |
| 1M PUT / 1M DELETE acceptance workload | `src/scenarios/acceptance_test.go` | `KVDB_ACCEPTANCE=1 go test ./src/scenarios -run TestAcceptanceMillionPutDelete -v -timeout 90m` |
| Torture oracle unit tests | `src/cmd/torture/model_test.go`, `seed_test.go` | `go test ./src/cmd/torture` |
| Torture runs (process kills) | `src/cmd/torture/` binary | `torture --iterations 500 --seed 12345` |

Requirement-to-test map: [docs/TEST_PLAN.md](../docs/TEST_PLAN.md). Recorded
results: [docs/results](../docs/results).
