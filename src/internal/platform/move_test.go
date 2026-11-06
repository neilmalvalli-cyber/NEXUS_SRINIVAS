package platform

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMoveNoReplaceMovesWhenDestinationAbsent(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "db.kv.compact"), filepath.Join(dir, "db.kv")
	writeFile(t, src, "new contents")

	if err := MoveNoReplace(src, dst); err != nil {
		t.Fatalf("MoveNoReplace: %v", err)
	}
	if got := readFile(t, dst); got != "new contents" {
		t.Fatalf("destination = %q", got)
	}
	if _, err := os.Stat(src); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("source still exists: %v", err)
	}
}

// The property R1/R2 depend on: an existing destination is never replaced,
// and neither file is changed.
func TestMoveNoReplaceRefusesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "db.kv.compact"), filepath.Join(dir, "db.kv")
	writeFile(t, src, "compacted")
	writeFile(t, dst, "authoritative")

	err := MoveNoReplace(src, dst)
	if err == nil {
		t.Fatal("MoveNoReplace replaced an existing destination")
	}
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	if got := readFile(t, dst); got != "authoritative" {
		t.Fatalf("destination changed to %q", got)
	}
	if got := readFile(t, src); got != "compacted" {
		t.Fatalf("source changed to %q", got)
	}
}

func TestMoveNoReplaceMissingSource(t *testing.T) {
	dir := t.TempDir()
	err := MoveNoReplace(filepath.Join(dir, "absent"), filepath.Join(dir, "dst"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dst")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("destination was created")
	}
}

// R1 then R2, as compaction does them, ending with exactly the expected names.
func TestMoveNoReplaceTwoRenameSequence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.kv")
	writeFile(t, path, "old")
	writeFile(t, path+".compact", "new")

	if err := MoveNoReplace(path, path+".old"); err != nil { // R1
		t.Fatalf("R1: %v", err)
	}
	if err := MoveNoReplace(path+".compact", path); err != nil { // R2
		t.Fatalf("R2: %v", err)
	}
	if readFile(t, path) != "new" || readFile(t, path+".old") != "old" {
		t.Fatal("wrong contents after R1+R2")
	}
	if _, err := os.Stat(path + ".compact"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(".compact still present after R2")
	}
	// R1 again with a stale .old present must fail: that is why §11.2 deletes
	// stale .old before compaction starts.
	writeFile(t, path+".compact", "newer")
	if err := MoveNoReplace(path, path+".old"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("R1 over stale .old: err = %v, want fs.ErrExist", err)
	}
}
