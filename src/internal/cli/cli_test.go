package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/kvdb"
)

func run(t *testing.T, stdin string, argv ...string) (code int, stdout, stderr string) {
	t.Helper()
	var o, e bytes.Buffer
	code = Run(argv, Env{Stdin: strings.NewReader(stdin), Stdout: &o, Stderr: &e})
	return code, o.String(), e.String()
}

func mustRun(t *testing.T, want string, argv ...string) {
	t.Helper()
	code, out, errs := run(t, "", argv...)
	if code != 0 || out != want {
		t.Fatalf("%v: code %d stdout %q (want %q) stderr %q", argv, code, out, want, errs)
	}
}

func TestCommandsRoundTrip(t *testing.T) {
	db := filepath.Join(t.TempDir(), "t.db")
	code, out, errs := run(t, "", "open", db)
	if code != 0 || !strings.HasPrefix(out, "keys=0 recovery_ms=") || !strings.Contains(errs, "created") {
		t.Fatalf("open: %d %q %q", code, out, errs)
	}
	mustRun(t, "OK\n", "put", db, "apple", "red fruit")
	mustRun(t, "red fruit\n", "get", db, "apple")
	mustRun(t, "NOTFOUND\n", "get", db, "pear")
	mustRun(t, "OK\n", "del", db, "pear") // deleting a missing key is not an error
	mustRun(t, "OK\n", "put", db, "empty", "")
	mustRun(t, "\n", "get", db, "empty")
	mustRun(t, "OK\n", "del", db, "apple")
	mustRun(t, "NOTFOUND\n", "get", db, "apple")
	code, out, _ = run(t, "", "open", db)
	if code != 0 || !strings.HasPrefix(out, "keys=1 ") {
		t.Fatalf("open after writes: %d %q", code, out)
	}
	mustRun(t, "OK\n", "check", db)
}

func TestUsageErrors(t *testing.T) {
	db := filepath.Join(t.TempDir(), "t.db")
	for _, argv := range [][]string{
		{}, {"nope", db}, {"put", db, "k"}, {"get", db}, {"open"}, {"open", db, "--max-bytes"},
		{"open", db, "--max-bytes", "-5"}, {"open", db, "--frobnicate"}, {"dump", db, "--max-bytes", "100"},
	} {
		if code, _, errs := run(t, "", argv...); code != ExitUsage || !strings.Contains(errs, "usage:") {
			t.Errorf("%v: code %d stderr %q", argv, code, errs)
		}
	}
	// Invalid --max-bytes value for the engine (1..31) is a definite error.
	if code, _, errs := run(t, "", "open", db, "--max-bytes=5"); code != ExitError || !strings.Contains(errs, "invalid") {
		t.Errorf("max-bytes 5: %d %q", code, errs)
	}
	// "--" lets a key start with "--".
	mustRun(t, "OK\n", "put", db, "--", "--k", "v")
	mustRun(t, "v\n", "get", db, "--", "--k")
}

func TestBatchFileOrderingAndAtomicity(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "t.db")
	bf := filepath.Join(dir, "b.txt")
	// B5: put X then del X -> absent; del Y then put Y -> present; last put wins.
	content := "put x 1\r\ndel x\n\ndel y\nput y has  two spaces\nput z 1\nput z 2\nput e\n"
	if err := os.WriteFile(bf, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "OK 7 ops\n", "batch", db, bf)
	code, out, _ := run(t, "", "dump", db)
	want := "e \ny has  two spaces\nz 2\n"
	if code != 0 || out != want {
		t.Fatalf("dump %d %q want %q", code, out, want)
	}
	// A bad line rejects the whole batch: nothing applied.
	os.WriteFile(bf, []byte("put a 1\nfrob b\n"), 0o644)
	if code, _, errs := run(t, "", "batch", db, bf); code != ExitError || !strings.Contains(errs, "line 2") {
		t.Fatalf("bad batch: %d %q", code, errs)
	}
	mustRun(t, "NOTFOUND\n", "get", db, "a")
	// More than MaxBatchOps operations is rejected before touching the database.
	var sb strings.Builder
	for i := 0; i <= kvdb.MaxBatchOps; i++ {
		fmt.Fprintf(&sb, "put k%d v\n", i)
	}
	os.WriteFile(bf, []byte(sb.String()), 0o644)
	if code, _, errs := run(t, "", "batch", db, bf); code != ExitError || !strings.Contains(errs, "too-large") {
		t.Fatalf("oversized batch: %d %q", code, errs)
	}
	mustRun(t, "NOTFOUND\n", "get", db, "k0")
	// Exactly MaxBatchOps is accepted.
	sb.Reset()
	for i := 0; i < kvdb.MaxBatchOps; i++ {
		fmt.Fprintf(&sb, "put k%05d v%d\n", i, i)
	}
	os.WriteFile(bf, []byte(sb.String()), 0o644)
	mustRun(t, fmt.Sprintf("OK %d ops\n", kvdb.MaxBatchOps), "batch", db, bf)
	mustRun(t, "v9999\n", "get", db, "k09999")
}

func TestDumpSortedAndQuoted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	db, err := kvdb.Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	db.Put([]byte("b"), []byte("2"))
	db.Put([]byte("a"), []byte("1"))
	db.Put([]byte("sp ace"), []byte("x"))
	db.Put([]byte("nl"), []byte("line1\nline2"))
	db.Put([]byte("C"), []byte(`"quoted"`))
	db.Close()
	code, out, _ := run(t, "", "dump", path)
	want := `"C" "\"quoted\""` + "\na 1\nb 2\n" + `"nl" "line1\nline2"` + "\n" + `"sp ace" "x"` + "\n"
	if code != 0 || out != want {
		t.Fatalf("dump:\n%s\nwant:\n%s", out, want)
	}
}

func TestCheckReportsCorruptionAndTornTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.db")
	for i := 0; i < 5; i++ {
		mustRun(t, "OK\n", "put", path, fmt.Sprintf("k%d", i), strings.Repeat("v", 100))
	}
	// F4: truncate one byte; check repairs the unacknowledged tail and says OK.
	fi, _ := os.Stat(path)
	if err := os.Truncate(path, fi.Size()-1); err != nil {
		t.Fatal(err)
	}
	code, out, errs := run(t, "", "check", path)
	if code != 0 || out != "OK\n" || !strings.Contains(errs, "torn") {
		t.Fatalf("check after truncate: %d %q %q", code, out, errs)
	}
	mustRun(t, "NOTFOUND\n", "get", path, "k4")
	mustRun(t, strings.Repeat("v", 100)+"\n", "get", path, "k3")
	// Flip a byte inside the second frame's value: a CRC-valid frame follows,
	// so this is not a torn tail but corruption.
	b, _ := os.ReadFile(path)
	b[32+32+11+5] ^= 0xff
	os.WriteFile(path, b, 0o644)
	code, out, _ = run(t, "", "check", path)
	if code != ExitError || !strings.HasPrefix(out, "CORRUPT") {
		t.Fatalf("check on corrupt file: %d %q", code, out)
	}
	// Not a database at all.
	os.WriteFile(path, []byte("hello, this is not a database file at all......"), 0o644)
	code, out, _ = run(t, "", "check", path)
	if code != ExitError || !strings.HasPrefix(out, "BAD FORMAT") {
		t.Fatalf("check on garbage: %d %q", code, out)
	}
}

func TestMaxBytesFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	mustRun(t, "OK\n", "put", path, "a", "1", "--max-bytes", "4096")
	code, _, errs := run(t, "", "put", path, "big", strings.Repeat("x", 5000), "--max-bytes=4096")
	if code != ExitError || !strings.Contains(errs, "(full)") {
		t.Fatalf("over-cap put: %d %q", code, errs)
	}
	mustRun(t, "1\n", "get", path, "a")
	mustRun(t, "NOTFOUND\n", "get", path, "big")
	mustRun(t, "OK\n", "check", path)
}

func TestLockedWhileServing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	pr, pw := io.Pipe()
	done := make(chan int)
	var out bytes.Buffer
	var errb syncBuf
	go func() { done <- Run([]string{"serve", path}, Env{Stdin: pr, Stdout: &out, Stderr: &errb}) }()
	// Wait for serve to hold the lock.
	for !strings.Contains(errb.String(), "ready") {
		select {
		case c := <-done:
			t.Fatalf("serve exited %d: %s", c, errb.String())
		default:
		}
	}
	code, _, errs := run(t, "", "get", path, "k")
	if code != ExitError || !strings.Contains(errs, "locked") {
		t.Fatalf("second process: %d %q", code, errs)
	}
	pw.Close()
	if c := <-done; c != 0 {
		t.Fatalf("serve exit %d", c)
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func serveLines(t *testing.T, path, input string, extra ...string) []string {
	t.Helper()
	code, out, errs := run(t, input, append([]string{"serve", path}, extra...)...)
	if code != 0 {
		t.Fatalf("serve exit %d: %s", code, errs)
	}
	return strings.Split(strings.TrimRight(out, "\n"), "\n")
}

func TestServeProtocol(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	in := strings.Join([]string{
		"w1 op1 put apple 100",
		"w1 op2 get apple",
		"w1 op3 batch 3",
		"put x 1",
		"del x",
		"put y two words",
		"w1 op4 get x",
		"w1 op5 get y",
		"w1 op6 del nothing",
		"w1 op7 compact",
		"w1 op8 batch 2",
		"put a 1",
		"bogus line",
		"w1 op9 get a",
		"op10 put short form\r",
		"w1 op11 batch 10001",
		"total garbage",
		"w1 op12 put",
	}, "\n") + "\n"
	got := serveLines(t, path, in)
	byID := map[string]string{}
	for _, l := range got {
		f := strings.Fields(l)
		if len(f) < 2 {
			t.Fatalf("bad reply line %q", l)
		}
		byID[f[1]] = l
	}
	want := map[string]string{
		"op1": "ACK op1", "op2": "VALUE op2 100", "op3": "ACK op3", "op4": "NOTFOUND op4",
		"op5": "VALUE op5 two words", "op6": "ACK op6", "op7": "ACK op7", "op9": "NOTFOUND op9",
		"op10": "ACK op10",
	}
	for id, w := range want {
		if byID[id] != w {
			t.Errorf("%s: got %q want %q", id, byID[id], w)
		}
	}
	for _, id := range []string{"op8", "op11", "op12"} {
		if !strings.HasPrefix(byID[id], "ERR "+id+" ") {
			t.Errorf("%s: got %q, want ERR", id, byID[id])
		}
	}
	if !strings.HasPrefix(byID["garbage"], "ERR garbage protocol") {
		t.Errorf("garbage line: %q (all: %q)", byID["garbage"], got)
	}
	// Per-writer order: w1's replies appear in submission order.
	var w1 []string
	for _, l := range got {
		id := strings.Fields(l)[1]
		if strings.HasPrefix(id, "op") && id != "op10" {
			w1 = append(w1, id)
		}
	}
	if !sort.SliceIsSorted(w1, func(i, j int) bool { return opNum(w1[i]) < opNum(w1[j]) }) {
		t.Errorf("w1 replies out of order: %v", w1)
	}
	mustRun(t, "form\n", "get", path, "short")
}

func opNum(id string) int {
	var n int
	fmt.Sscanf(id, "op%d", &n)
	return n
}

func TestServeConcurrentWritersKeepOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	var sb strings.Builder
	const writers, perWriter = 4, 60
	for i := 0; i < perWriter; i++ {
		for w := 0; w < writers; w++ {
			fmt.Fprintf(&sb, "w%d w%d-%03d put k%d %d\n", w, w, i, w, i)
		}
	}
	got := serveLines(t, path, sb.String())
	if len(got) != writers*perWriter {
		t.Fatalf("%d replies, want %d", len(got), writers*perWriter)
	}
	last := map[string]int{}
	for _, l := range got {
		var w, i int
		if _, err := fmt.Sscanf(l, "ACK w%d-%03d", &w, &i); err != nil {
			t.Fatalf("reply %q: %v", l, err)
		}
		k := fmt.Sprint(w)
		if prev, ok := last[k]; ok && i != prev+1 {
			t.Fatalf("writer %d: reply %d after %d", w, i, prev)
		}
		last[k] = i
	}
	for w := 0; w < writers; w++ {
		mustRun(t, fmt.Sprintf("%d\n", perWriter-1), "get", path, fmt.Sprintf("k%d", w))
	}
}

func TestServeMaxBytesErrorsAreClean(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	var sb strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&sb, "w1 p%d put k%d %s\n", i, i, strings.Repeat("v", 200))
	}
	got := serveLines(t, path, sb.String(), "--max-bytes", "4096")
	acked, full := 0, 0
	for _, l := range got {
		switch {
		case strings.HasPrefix(l, "ACK "):
			acked++
		case strings.Contains(l, " full "):
			full++
		default:
			t.Fatalf("unexpected reply %q", l)
		}
	}
	if acked == 0 || full == 0 {
		t.Fatalf("acked %d full %d", acked, full)
	}
	fi, _ := os.Stat(path)
	if fi.Size() > 4096 {
		t.Fatalf("file %d bytes exceeds cap", fi.Size())
	}
	code, out, _ := run(t, "", "dump", path)
	if code != 0 || strings.Count(out, "\n") != acked {
		t.Fatalf("dump has %d keys, %d acked", strings.Count(out, "\n"), acked)
	}
}

func TestServeNeverAcksFailedWrites(t *testing.T) {
	for _, e := range []error{kvdb.ErrOutcomeUnknown, kvdb.ErrFailed, kvdb.ErrFull, errors.New("disk on fire")} {
		var o, se bytes.Buffer
		st := &failingStore{err: e}
		in := "w1 a put k v\nw1 b del k\nw1 c batch 1\nput k v\nw1 d compact\n"
		code := Run([]string{"serve", "x"}, Env{Stdin: strings.NewReader(in), Stdout: &o, Stderr: &se,
			Open: func(string, int64) (Store, error) { return st, nil }})
		if code != 0 {
			t.Fatalf("%v: exit %d", e, code)
		}
		if strings.Contains(o.String(), "ACK") {
			t.Fatalf("%v: serve acknowledged a failed write:\n%s", e, o.String())
		}
		if n := strings.Count(o.String(), "ERR "); n != 4 {
			t.Fatalf("%v: %d ERR lines:\n%s", e, n, o.String())
		}
		if errors.Is(e, kvdb.ErrOutcomeUnknown) && !strings.Contains(o.String(), "ERR a unknown") {
			t.Fatalf("unknown outcome not labelled: %s", o.String())
		}
	}
}

type failingStore struct{ err error }

func (f *failingStore) Put(k, v []byte) error               { return f.err }
func (f *failingStore) Get(k []byte) ([]byte, error)        { return nil, kvdb.ErrNotFound }
func (f *failingStore) Delete(k []byte) error               { return f.err }
func (f *failingStore) Batch(ops []kvdb.Op) error           { return f.err }
func (f *failingStore) Compact() error                      { return f.err }
func (f *failingStore) Close() error                        { return nil }
func (f *failingStore) Stats() kvdb.Stats                   { return kvdb.Stats{} }
func (f *failingStore) Recovery() kvdb.RecoveryInfo         { return kvdb.RecoveryInfo{} }
func (f *failingStore) Range(func(k, v []byte) error) error { return nil }
func (f *failingStore) Verify() (kvdb.VerifyReport, error)  { return kvdb.VerifyReport{}, nil }

func TestLineReaderLimits(t *testing.T) {
	long := strings.Repeat("x", MaxLine+10)
	lr := newLineReader(strings.NewReader("a\r\n" + long + "\nb"))
	if l, err := lr.next(); l != "a" || err != nil {
		t.Fatalf("first: %q %v", l, err)
	}
	if _, err := lr.next(); !errors.Is(err, errLineTooLong) {
		t.Fatalf("long line: %v", err)
	}
	if l, err := lr.next(); l != "b" || err != nil {
		t.Fatalf("after long line: %q %v", l, err)
	}
	if _, err := lr.next(); err != io.EOF {
		t.Fatalf("eof: %v", err)
	}
	// A 1 MiB value fits.
	v := strings.Repeat("y", kvdb.MaxValueLen)
	op, err := parseOp("put k " + v)
	if err != nil || len(op.Value) != kvdb.MaxValueLen {
		t.Fatalf("1 MiB put: %v", err)
	}
}

func FuzzParseRequest(f *testing.F) {
	for _, s := range []string{"w1 op1 put k v", "op put k", "w op batch 2\nput a 1\ndel b", "w op get", "", "x y z", "w op del k extra"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		lines := strings.SplitN(s, "\n", 2)
		rest := ""
		if len(lines) == 2 {
			rest = lines[1]
		}
		r := parseRequest(lines[0], newLineReader(strings.NewReader(rest)))
		if r == nil {
			t.Fatal("nil request")
		}
		if r.err == nil {
			switch r.verb {
			case "put", "del", "delete", "get":
				if len(r.key) == 0 {
					t.Fatalf("accepted %q without a key", s)
				}
			case "batch":
				if err := kvdb.ValidateBatch(r.ops); err != nil {
					t.Fatalf("accepted invalid batch %q: %v", s, err)
				}
			}
		}
	})
}

// A request cut off mid-line (the sender died while writing it) is never
// executed, even though serve sees the bytes before end of input.
func TestServeIgnoresIncompleteFinalLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	got := serveLines(t, path, "w1 a put k v\nw1 b put k2 trunc")
	sort.Strings(got) // the protocol error is written by the reader, the ACK by the writer
	if len(got) != 2 || got[0] != "ACK a" || !strings.Contains(got[1], "incomplete") {
		t.Fatalf("replies %q", got)
	}
	mustRun(t, "NOTFOUND\n", "get", path, "k2")
	got = serveLines(t, path, "w1 c batch 2\nput x 1\nput y 2")
	if len(got) != 1 || !strings.HasPrefix(got[0], "ERR c protocol") {
		t.Fatalf("replies %q", got)
	}
	mustRun(t, "NOTFOUND\n", "get", path, "x")
}

// dirState returns name -> contents of every file in dir except lock files.
func dirState(t *testing.T, dir string) map[string]string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".lock") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		m[e.Name()] = string(b)
	}
	return m
}

// checkRO runs `check --no-repair` and fails if any file in the database's
// directory changed (bytes or names).
func checkRO(t *testing.T, path string) (int, string, string) {
	t.Helper()
	dir := filepath.Dir(path)
	before := dirState(t, dir)
	code, out, errs := run(t, "", "check", path, "--no-repair")
	after := dirState(t, dir)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("check --no-repair modified files:\nbefore %d files\nafter %d files", len(before), len(after))
	}
	return code, out, errs
}

// check --no-repair on a valid file, a torn tail, middle corruption, a
// damaged snapshot, a damaged superblock, an interrupted compaction and a
// missing database: never modifies anything; default check still repairs.
func TestCheckNoRepair(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ro.db")
	for i := 0; i < 5; i++ {
		mustRun(t, "OK\n", "put", path, fmt.Sprintf("k%d", i), strings.Repeat("v", 100))
	}
	// 1. Valid file.
	code, out, errs := checkRO(t, path)
	if code != ExitOK || out != "OK\n" || !strings.Contains(errs, "5 keys") || !strings.Contains(errs, "not modified") {
		t.Fatalf("valid: %d %q %q", code, out, errs)
	}

	// 2. Torn tail: reported, not cut; the default check then cuts it.
	good, _ := os.ReadFile(path)
	if err := os.Truncate(path, int64(len(good)-1)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // twice: still there the second time
		code, out, errs = checkRO(t, path)
		if code != ExitNeedsRepair || !strings.HasPrefix(out, "NEEDS REPAIR: open would discard a torn") || !strings.Contains(errs, "4 keys") {
			t.Fatalf("torn tail: %d %q %q", code, out, errs)
		}
	}
	if fi, _ := os.Stat(path); fi.Size() != int64(len(good)-1) {
		t.Fatalf("torn tail was cut by --no-repair: size %d", fi.Size())
	}
	code, out, _ = run(t, "", "check", path)
	if code != ExitOK || out != "OK\n" {
		t.Fatalf("default check after torn tail: %d %q", code, out)
	}
	if code, out, _ = checkRO(t, path); code != ExitOK || out != "OK\n" {
		t.Fatalf("after repair: %d %q", code, out)
	}

	// 3. Middle corruption (valid frames follow): CORRUPT, exit 1, unchanged.
	b, _ := os.ReadFile(path)
	bad := append([]byte(nil), b...)
	bad[32+32+11+5] ^= 0xff
	os.WriteFile(path, bad, 0o644)
	if code, out, _ = checkRO(t, path); code != ExitError || !strings.HasPrefix(out, "CORRUPT") {
		t.Fatalf("middle corruption: %d %q", code, out)
	}

	// 4. Damaged snapshot: compact, then flip a byte at the very end of the
	// compacted snapshot (a torn tail would be legal there only outside it).
	os.WriteFile(path, b, 0o644)
	if code, out, errs = run(t, "1 compact\n", "serve", path); code != 0 || !strings.Contains(out, "ACK 1") {
		t.Fatalf("compact: %d %q %q", code, out, errs)
	}
	snap, _ := os.ReadFile(path)
	bad = append([]byte(nil), snap...)
	bad[len(bad)-1] ^= 0x01
	os.WriteFile(path, bad, 0o644)
	if code, out, _ = checkRO(t, path); code != ExitError || !strings.HasPrefix(out, "CORRUPT") {
		t.Fatalf("damaged snapshot: %d %q", code, out)
	}

	// 5. Damaged superblock: BAD FORMAT.
	bad = append([]byte(nil), snap...)
	bad[0] ^= 0xff
	os.WriteFile(path, bad, 0o644)
	if code, out, _ = checkRO(t, path); code != ExitError || !strings.HasPrefix(out, "BAD FORMAT") {
		t.Fatalf("damaged superblock: %d %q", code, out)
	}

	// 6. Interrupted compaction: path gone, sealed .compact present (A3).
	os.Remove(path)
	os.WriteFile(path+".compact", snap, 0o644)
	code, out, _ = checkRO(t, path)
	if code != ExitNeedsRepair || !strings.Contains(out, "rule A3") {
		t.Fatalf("interrupted compaction: %d %q", code, out)
	}
	if code, out, _ = run(t, "", "check", path); code != ExitOK || out != "OK\n" {
		t.Fatalf("default check completes the compaction: %d %q", code, out)
	}
	mustRun(t, strings.Repeat("v", 100)+"\n", "get", path, "k3") // k4 was the torn tail

	// 7. Leftover temp next to a valid file: reported, not deleted.
	os.WriteFile(path+".old", snap, 0o644)
	if code, out, _ = checkRO(t, path); code != ExitNeedsRepair || !strings.Contains(out, "leftover temp file") {
		t.Fatalf("stale temp: %d %q", code, out)
	}

	// 8. Missing database: reported, not created.
	missing := filepath.Join(t.TempDir(), "none.db")
	if code, out, _ = checkRO(t, missing); code != ExitNeedsRepair || !strings.Contains(out, "create an empty one") {
		t.Fatalf("missing: %d %q", code, out)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Fatal("--no-repair created the database")
	}

	// --no-repair belongs to check only.
	if code, _, _ = run(t, "", "open", path, "--no-repair"); code != ExitUsage {
		t.Fatalf("open --no-repair: exit %d, want usage", code)
	}
}

// --no-repair while another process holds the database: LOCKED, exit 1.
func TestCheckNoRepairLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l.db")
	db, err := kvdb.Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	code, out, _ := checkRO(t, path)
	if code != ExitError || !strings.HasPrefix(out, "LOCKED") {
		t.Fatalf("locked: %d %q", code, out)
	}
}
