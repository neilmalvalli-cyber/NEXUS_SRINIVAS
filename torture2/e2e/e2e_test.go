// Package e2e runs NEXUS Torture Engine 2.0 end to end against freshly built
// binaries: the real kvdb (which must pass) and kvdb-mutant (whose mutations
// must be caught). Skipped with -short.
package e2e

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/engine"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/mutation"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/proc"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/scenario"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/workload"
)

func build(t *testing.T) (kvdb, mutant string) {
	t.Helper()
	if testing.Short() {
		t.Skip("end-to-end test builds and runs real binaries")
	}
	dir := t.TempDir()
	cmd := exec.Command("go", "build", "-o", dir+string(os.PathSeparator), "github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/cmd/kvdb", "github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/mutants/kvdb-mutant")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	return filepath.Join(dir, "kvdb"+exe), filepath.Join(dir, "kvdb-mutant"+exe)
}

func TestRealKvdbPasses(t *testing.T) {
	kv, _ := build(t)
	b, err := proc.Locate(kv)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"default", "batch10", "same-key"} {
		m, _ := workload.ByName(mode)
		sum, err := engine.Run(engine.Config{Mode: m, Seed: 4242, Iterations: 3, Kvdb: b,
			Dir: filepath.Join(t.TempDir(), "run"), Quiet: true, StopOnFail: true, Out: io.Discard})
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if sum.Result != "PASS" || sum.Completed != 3 {
			t.Fatalf("%s: result %s after %d iterations; first failure %+v", mode, sum.Result, sum.Completed, sum.FirstFailure)
		}
	}
}

func TestMutationsAreDetected(t *testing.T) {
	_, mut := build(t)
	rep, err := mutation.Run(mutation.Options{Mutant: mut, Dir: filepath.Join(t.TempDir(), "mut"), Iterations: 20, Seed: 777, Out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range rep.Outcomes {
		if !o.AsExpected {
			t.Errorf("mutation %s: result %s kind %q (expected %v)", o.Name, o.Result, o.Kind, o.Expect)
		}
	}
	if len(rep.Outcomes) != len(mutation.Cases) {
		t.Fatalf("ran %d of %d mutations", len(rep.Outcomes), len(mutation.Cases))
	}
}

func TestScenarioSampleOnRealKvdb(t *testing.T) {
	kv, _ := build(t)
	b, err := proc.Locate(kv)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := scenario.RunAll(scenario.Options{Kvdb: b, Dir: filepath.Join(t.TempDir(), "sc"), Only: []string{"A1", "B1", "C4", "E2", "F4"}, Seed: 1, Out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Result != "PASS" {
		t.Fatalf("scenarios: %s: %+v", rep.Result, rep)
	}
}
