package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The judge chooses the seed. A seed that is not exactly an integer must be
// refused (exit 2) before anything runs, never silently replaced by a
// different seed: "12abc" used to run, and report, seed 12.
func TestSeedMustBeExactInteger(t *testing.T) {
	dir := t.TempDir()
	// A plain text file, never an executable: if seed parsing ever regressed
	// and a run started, it must not launch this test binary as "kvdb"
	// (that relaunches the tests, recursively).
	fake := filepath.Join(dir, "not-kvdb.txt")
	if err := os.WriteFile(fake, []byte("not an executable"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "abc", "12abc", "1.5", "0x10", "99999999999999999999", "1 2"} {
		args := []string{"--iterations", "1", "--seed", bad, "--dir", filepath.Join(dir, "run"), "--kvdb", fake}
		if got := run(args); got != 2 {
			t.Errorf("--seed %q: exit %d, want 2 (usage error)", bad, got)
		}
		if _, err := os.Stat(filepath.Join(dir, "run")); err == nil {
			t.Fatalf("--seed %q: a work directory was created before the seed was validated", bad)
		}
	}
}
