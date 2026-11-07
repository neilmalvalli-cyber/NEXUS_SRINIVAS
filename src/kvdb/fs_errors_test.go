package kvdb

// Judge attacks S3 (read-only file or directory, access denied) and S4
// (missing parent directory). Every test works only inside t.TempDir().
// Expected: Open fails with an error that names the path and the OS reason,
// creates nothing anywhere, leaves an existing database byte-for-byte
// unchanged, and leaves no stale lock behind, so the database opens
// normally once the cause is removed.

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func listTree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && p != root {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	slices.Sort(out)
	return out
}

// sys returns the Windows system tool (not a same-named Unix tool that Git
// for Windows may put first on PATH).
func sys(name string) string {
	return filepath.Join(os.Getenv("SystemRoot"), "System32", name)
}

func clearError(t *testing.T, err error, path string, allowed ...error) {
	t.Helper()
	if err == nil {
		t.Fatal("Open succeeded, want an error")
	}
	if !strings.Contains(err.Error(), filepath.Base(path)) {
		t.Errorf("error does not name the database path: %v", err)
	}
	for _, sentinel := range []error{ErrCorruption, ErrBadFormat, ErrLocked} {
		if errors.Is(err, sentinel) && !slices.ContainsFunc(allowed, func(a error) bool { return a == sentinel }) {
			t.Errorf("an OS access problem must not be reported as %v: %v", sentinel, err)
		}
	}
	t.Logf("error shown to the user: %v", err)
}

func makeDB(t *testing.T, path string) []byte {
	t.Helper()
	db := openT(t, path)
	if err := db.Put([]byte("acked"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func reopenHasAcked(t *testing.T, path string) {
	t.Helper()
	db, err := Open(path, 0)
	if err != nil {
		t.Fatalf("database does not open after the cause was removed (stale lock or damage?): %v", err)
	}
	defer db.Close()
	if v, err := db.Get([]byte("acked")); err != nil || string(v) != "value" {
		t.Fatalf("acknowledged key lost: %q %v", v, err)
	}
}

// S4: a missing parent directory is an error; Open never creates directories.
func TestOpenMissingParentDirectory(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"missing/db.kv", "missing/deeper/db.kv"} {
		path := filepath.Join(root, rel)
		_, err := Open(path, 0)
		clearError(t, err, path)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: want fs.ErrNotExist in the chain, got %v", rel, err)
		}
		if got := listTree(t, root); len(got) != 0 {
			t.Fatalf("%s: Open created %v", rel, got)
		}
	}
	// Nothing was created in the working directory either.
	wd, _ := os.Getwd()
	for _, name := range []string{"db.kv", "db.kv.lock"} {
		if _, err := os.Stat(filepath.Join(wd, name)); err == nil {
			t.Fatalf("Open created %s in the working directory", name)
		}
	}
}

// S3: the database file is read-only (FILE_ATTRIBUTE_READONLY on Windows).
func TestOpenReadOnlyFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "db.kv")
	orig := makeDB(t, path)
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o644) })
	_, err := Open(path, 0)
	clearError(t, err, path)
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("want fs.ErrPermission in the chain, got %v", err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, orig) {
		t.Fatal("refused open changed the database file")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	reopenHasAcked(t, path)
}

// S3: the path names a directory, not a file. Rule A2 refuses it as "not a
// regular file" (ErrBadFormat). The lock file <path>.lock is the one file
// NEXUS may create next to the database (DESIGN §1); it is never deleted.
func TestOpenPathIsDirectory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "db.kv")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path, 0)
	clearError(t, err, path, ErrBadFormat)
	if !errors.Is(err, ErrBadFormat) || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("want ErrBadFormat \"not a regular file\", got %v", err)
	}
	if got := listTree(t, root); !slices.Equal(got, []string{"db.kv", "db.kv.lock"}) {
		t.Fatalf("Open created %v", got)
	}
}

// denyWrite removes the current user's right to create files in dir and to
// write existing files in it (an explicit Deny ACE, inherited by files).
// It is removed again before t.TempDir's own cleanup runs.
func denyWrite(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("ACL deny test is Windows-specific")
	}
	out, err := exec.Command(sys("whoami.exe"), "/user", "/fo", "csv", "/nh").Output()
	if err != nil {
		t.Skipf("whoami unavailable: %v", err)
	}
	fields := strings.Split(strings.TrimSpace(string(out)), ",")
	sid := strings.Trim(fields[len(fields)-1], `"`)
	if !strings.HasPrefix(sid, "S-1-") {
		t.Skipf("could not read the current user's SID from %q", out)
	}
	grant := "*" + sid
	if out, err := exec.Command(sys("icacls.exe"), dir, "/deny", grant+":(OI)(CI)(WD,AD,WA,WEA)").CombinedOutput(); err != nil {
		t.Skipf("icacls deny failed: %v %s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command(sys("icacls.exe"), dir, "/remove:d", grant, "/t", "/c").CombinedOutput(); err != nil {
			t.Errorf("icacls cleanup failed: %v %s", err, out)
		}
	})
}

// S3: creating a new database in a directory the user may not write to.
func TestOpenNewDatabaseInDeniedDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "locked-down")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	denyWrite(t, dir)
	path := filepath.Join(dir, "db.kv")
	_, err := Open(path, 0)
	clearError(t, err, path)
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("want fs.ErrPermission in the chain, got %v", err)
	}
	if got := listTree(t, dir); len(got) != 0 {
		t.Fatalf("files appeared in the denied directory: %v", got)
	}
}

// S3: an existing database in a directory that became write-denied (files
// inherit the deny, so the data file cannot be opened for writing).
func TestOpenExistingDatabaseInDeniedDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "was-writable")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "db.kv")
	orig := makeDB(t, path)
	before := listTree(t, dir)
	denyWrite(t, dir)
	// icacls /deny on the directory with (OI) only applies to new children;
	// apply it to the existing file too, as an administrator locking down the
	// folder would.
	if out, err := exec.Command(sys("icacls.exe"), path, "/deny", denySID(t)+":(WD,AD,WA,WEA)").CombinedOutput(); err != nil {
		t.Skipf("icacls deny on file failed: %v %s", err, out)
	}
	_, err := Open(path, 0)
	clearError(t, err, path)
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("want fs.ErrPermission in the chain, got %v", err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, orig) {
		t.Fatal("refused open changed the database file")
	}
	if got := listTree(t, dir); !slices.Equal(got, before) {
		t.Fatalf("directory contents changed: %v -> %v", before, got)
	}
	exec.Command(sys("icacls.exe"), path, "/remove:d", denySID(t)).Run()
	exec.Command(sys("icacls.exe"), dir, "/remove:d", denySID(t), "/t", "/c").Run()
	reopenHasAcked(t, path)
}

func denySID(t *testing.T) string {
	t.Helper()
	out, err := exec.Command(sys("whoami.exe"), "/user", "/fo", "csv", "/nh").Output()
	if err != nil {
		t.Skip(err)
	}
	fields := strings.Split(strings.TrimSpace(string(out)), ",")
	return "*" + strings.Trim(fields[len(fields)-1], `"`)
}
