// Command crosscheck tests NEXUS against an independent implementation of
// the same on-disk format: the recovery package written by a teammate
// (github.com/sharanyashetty013-droid/kipdb-recovery, module "kipdb").
//
// It drives the real `kvdb` binary (built from this repository) and calls
// the independent package directly, and checks, in both directions:
//
//  1. byte-identical encoding of the superblock, single-op frames and
//     batch frames (same constants, byte order and checksum rules);
//  2. NEXUS writes (through `kvdb serve`) -> independent Verify;
//  3. independent compaction -> `kvdb check`, `check --no-repair`, `dump`;
//  4. NEXUS writes after the independent compaction;
//  5. files created and written by the independent code -> NEXUS reads
//     and writes;
//  6. a torn tail at every byte of the last frame: both implementations
//     agree on where the valid log ends, and NEXUS repairs it;
//  7. corruption followed by valid data: both refuse;
//  8. NEXUS and independent compaction produce byte-identical files.
//
// Usage: crosscheck <path-to-kvdb-binary> <scratch-dir>
// Prints one PASS/FAIL line per check; exit status 1 if any check fails.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"kipdb/recovery"
)

var (
	kvdb    string
	scratch string
	failed  int
	n       int
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: crosscheck <kvdb-binary> <scratch-dir>")
		os.Exit(2)
	}
	kvdb, scratch = os.Args[1], os.Args[2]
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		fmt.Println("FAIL setup:", err)
		os.Exit(1)
	}
	check("1 encoding: superblock + PUT frame byte-identical", checkEncodingPut)
	check("1 encoding: batch frame (PUT+DEL, same key twice) byte-identical", checkEncodingBatch)
	check("2 NEXUS serve writes -> independent Verify", checkNexusToVerify)
	check("3 independent Compact -> kvdb check / check --no-repair / dump", checkIndependentCompact)
	check("4 NEXUS writes after independent compaction", checkWritesAfterCompact)
	check("5 independent-created file -> NEXUS reads and writes", checkIndependentCreated)
	check("6 torn tail at every byte of the last frame: same valid end, NEXUS repairs", checkTornTails)
	check("7 corruption followed by valid data: both refuse", checkMiddleCorruption)
	check("8 NEXUS compaction == independent compaction, byte for byte", checkCompactionIdentical)
	fmt.Printf("crosscheck: %d checks, %d failed\n", n, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

func check(name string, fn func(dir string) error) {
	n++
	dir := filepath.Join(scratch, fmt.Sprintf("c%02d", n))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Printf("FAIL %s: %v\n", name, err)
		failed++
		return
	}
	if err := fn(dir); err != nil {
		fmt.Printf("FAIL %s: %v\n", name, err)
		failed++
		return
	}
	fmt.Printf("PASS %s\n", name)
}

// ---- helpers ----

// run executes kvdb and returns exit code, stdout, stderr.
func run(stdin string, args ...string) (int, string, string, error) {
	cmd := exec.Command(kvdb, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code, err = ee.ExitCode(), nil
	}
	return code, o.String(), e.String(), err
}

func must(code int, out, errs string, err error) func(want int, prefix string) error {
	return func(want int, prefix string) error {
		if err != nil {
			return err
		}
		if code != want || !strings.HasPrefix(out, prefix) {
			return fmt.Errorf("exit %d stdout %q stderr %q (want exit %d, stdout %q...)", code, out, errs, want, prefix)
		}
		return nil
	}
}

type roFile struct{ *os.File }

func (f roFile) Size() (int64, error) {
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}
func (roFile) Truncate(int64) error { return errors.New("read-only") }
func (roFile) Sync() error          { return nil }

// verify runs the independent read-only Verify and returns key -> value.
func verify(path string) (map[string]string, *recovery.Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	r, err := recovery.Verify(roFile{f})
	if err != nil {
		return nil, nil, err
	}
	m := map[string]string{}
	for k, l := range r.Index {
		v := make([]byte, l.ValueLen)
		if _, err := f.ReadAt(v, l.ValueOffset); err != nil {
			return nil, nil, err
		}
		m[k] = string(v)
	}
	return m, r, nil
}

// dump returns NEXUS's view (`kvdb dump`) as key -> value (plain keys only).
func dump(path string) (map[string]string, error) {
	code, out, errs, err := run("", "dump", path)
	if err != nil || code != 0 {
		return nil, fmt.Errorf("dump: exit %d %v %s", code, err, errs)
	}
	m := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		k, v, _ := strings.Cut(line, " ")
		m[k] = v
	}
	return m, nil
}

func equal(a, b map[string]string) error {
	if len(a) != len(b) {
		return fmt.Errorf("key counts differ: %d vs %d (%v vs %v)", len(a), len(b), keys(a), keys(b))
	}
	for k, v := range a {
		if b[k] != v {
			return fmt.Errorf("key %q: %q vs %q", k, v, b[k])
		}
	}
	return nil
}

func keys(m map[string]string) []string {
	var s []string
	for k := range m {
		s = append(s, k)
	}
	sort.Strings(s)
	return s
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// nexusDB writes a database through `kvdb serve` using the torture tool's
// request format (op-id first, writer id omitted), including batches. The
// dependent requests share one writer (requests of one writer run in
// order); a second writer touches a disjoint key concurrently. Six frames.
func nexusDB(dir string) (string, error) {
	p := filepath.Join(dir, "n.kv")
	in := "1000 put 5 a1b2c3\n" +
		"1001 batch 3\nput 6 dead\ndel 5\nput 7 beef\n" +
		"1002 put 7 cafe\n" +
		"2000 del 6\n" +
		"2001 batch 2\nput 8 aa\nput 8 bb\n" +
		"w2 2002 put 9 " + strings.Repeat("9", 5000) + "\n"
	code, out, errs, err := run(in, "serve", p)
	if err != nil || code != 0 {
		return "", fmt.Errorf("serve: exit %d %v %s", code, err, errs)
	}
	for _, id := range []string{"1000", "1001", "1002", "2000", "2001", "2002"} {
		if !strings.Contains(out, "ACK "+id+"\n") {
			return "", fmt.Errorf("serve did not ACK %s: %q", id, out)
		}
	}
	return p, nil
}

// ---- checks ----

func checkEncodingPut(dir string) error {
	p := filepath.Join(dir, "e.kv")
	if err := must(run("", "put", p, "hello", "world"))(0, "OK"); err != nil {
		return err
	}
	got, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	fr, err := recovery.EncodeFrame(1, []recovery.Op{{Kind: recovery.OpPut, Key: []byte("hello"), Value: []byte("world")}})
	if err != nil {
		return err
	}
	want := append(recovery.EncodeSuperblock(1, recovery.SuperblockSize), fr...)
	if !bytes.Equal(got, want) {
		return fmt.Errorf("bytes differ:\nNEXUS       %x\nindependent %x", got, want)
	}
	return nil
}

func checkEncodingBatch(dir string) error {
	p := filepath.Join(dir, "b.kv")
	bf := filepath.Join(dir, "ops.txt")
	if err := os.WriteFile(bf, []byte("put a 1\nput b 2\ndel a\nput b 22\n"), 0o644); err != nil {
		return err
	}
	if err := must(run("", "batch", p, bf))(0, "OK 4 ops"); err != nil {
		return err
	}
	got, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	fr, err := recovery.EncodeFrame(1, []recovery.Op{
		{Kind: recovery.OpPut, Key: []byte("a"), Value: []byte("1")},
		{Kind: recovery.OpPut, Key: []byte("b"), Value: []byte("2")},
		{Kind: recovery.OpDelete, Key: []byte("a")},
		{Kind: recovery.OpPut, Key: []byte("b"), Value: []byte("22")},
	})
	if err != nil {
		return err
	}
	want := append(recovery.EncodeSuperblock(1, recovery.SuperblockSize), fr...)
	if !bytes.Equal(got, want) {
		return fmt.Errorf("bytes differ:\nNEXUS       %x\nindependent %x", got, want)
	}
	m, _, err := verify(p)
	if err != nil {
		return err
	}
	return equal(m, map[string]string{"b": "22"}) // last write wins inside a batch, in both
}

func checkNexusToVerify(dir string) error {
	p, err := nexusDB(dir)
	if err != nil {
		return err
	}
	m, r, err := verify(p)
	if err != nil {
		return err
	}
	if r.TornTailAt != -1 || r.Frames != 6 || r.NextSeq != 7 {
		return fmt.Errorf("independent scan: frames %d next %d torn %d", r.Frames, r.NextSeq, r.TornTailAt)
	}
	d, err := dump(p)
	if err != nil {
		return err
	}
	return equal(m, d)
}

func checkIndependentCompact(dir string) error {
	p, err := nexusDB(dir)
	if err != nil {
		return err
	}
	before, err := dump(p)
	if err != nil {
		return err
	}
	h, err := recovery.Open(recovery.OSFS{}, p, false)
	if err != nil {
		return err
	}
	if err := h.Compact(); err != nil {
		h.Close()
		return err
	}
	h.Close()
	if err := must(run("", "check", p, "--no-repair"))(0, "OK"); err != nil {
		return fmt.Errorf("check --no-repair: %v", err)
	}
	if err := must(run("", "check", p))(0, "OK"); err != nil {
		return fmt.Errorf("check: %v", err)
	}
	after, err := dump(p)
	if err != nil {
		return err
	}
	return equal(before, after)
}

func checkWritesAfterCompact(dir string) error {
	p, err := nexusDB(dir)
	if err != nil {
		return err
	}
	h, err := recovery.Open(recovery.OSFS{}, p, false)
	if err != nil {
		return err
	}
	if err := h.Compact(); err != nil {
		h.Close()
		return err
	}
	h.Close()
	if err := must(run("", "put", p, "new", "after"))(0, "OK"); err != nil {
		return err
	}
	if err := must(run("", "del", p, "7"))(0, "OK"); err != nil {
		return err
	}
	m, r, err := verify(p)
	if err != nil {
		return err
	}
	if m["new"] != "after" || m["7"] != "" || r.SnapshotEnd == recovery.SuperblockSize {
		return fmt.Errorf("independent view after NEXUS writes: %v snapEnd %d", keys(m), r.SnapshotEnd)
	}
	d, err := dump(p)
	if err != nil {
		return err
	}
	return equal(m, d)
}

func checkIndependentCreated(dir string) error {
	p := filepath.Join(dir, "t.kv")
	h, err := recovery.Open(recovery.OSFS{}, p, true)
	if err != nil {
		return err
	}
	off, seq := h.Result.ValidEnd, h.Result.NextSeq
	for _, ops := range [][]recovery.Op{
		{{Kind: recovery.OpPut, Key: []byte("a"), Value: []byte("1")}},
		{{Kind: recovery.OpPut, Key: []byte("b"), Value: []byte("2")}, {Kind: recovery.OpDelete, Key: []byte("a")}, {Kind: recovery.OpPut, Key: []byte("c"), Value: []byte("3")}},
		{{Kind: recovery.OpPut, Key: []byte("b"), Value: []byte("22")}},
	} {
		b, err := recovery.EncodeFrame(seq, ops)
		if err != nil {
			h.Close()
			return err
		}
		if _, err := h.File.WriteAt(b, off); err != nil {
			h.Close()
			return err
		}
		off += int64(len(b))
		seq++
	}
	if err := h.File.Sync(); err != nil {
		h.Close()
		return err
	}
	h.Close()
	if err := must(run("", "check", p, "--no-repair"))(0, "OK"); err != nil {
		return err
	}
	d, err := dump(p)
	if err != nil {
		return err
	}
	if err := equal(d, map[string]string{"b": "22", "c": "3"}); err != nil {
		return err
	}
	if err := must(run("", "put", p, "d", "4"))(0, "OK"); err != nil {
		return err
	}
	m, _, err := verify(p)
	if err != nil {
		return err
	}
	return equal(m, map[string]string{"b": "22", "c": "3", "d": "4"})
}

func checkTornTails(dir string) error {
	p, err := nexusDB(dir)
	if err != nil {
		return err
	}
	clean, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	_, r, err := verify(p)
	if err != nil {
		return err
	}
	// Find where the last frame starts: append one more frame with NEXUS.
	if err := must(run("", "put", p, "last", "frame-to-tear"))(0, "OK"); err != nil {
		return err
	}
	full, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	start := int64(len(clean))
	if r.ValidEnd != start {
		return fmt.Errorf("independent valid end %d != file size %d", r.ValidEnd, start)
	}
	for cut := start + 1; cut < int64(len(full)); cut++ {
		tp := filepath.Join(dir, fmt.Sprintf("torn%d.kv", cut))
		if err := os.WriteFile(tp, full[:cut], 0o644); err != nil {
			return err
		}
		_, tr, err := verify(tp)
		if err != nil {
			return fmt.Errorf("cut %d: independent: %v", cut, err)
		}
		if tr.TornTailAt != start {
			return fmt.Errorf("cut %d: independent torn tail at %d, want %d", cut, tr.TornTailAt, start)
		}
		code, out, errs, err := run("", "check", tp, "--no-repair")
		if err != nil || code != 4 || !strings.Contains(out, fmt.Sprintf("at offset %d", start)) {
			return fmt.Errorf("cut %d: check --no-repair exit %d %q %s %v", cut, code, out, errs, err)
		}
		if b, _ := os.ReadFile(tp); int64(len(b)) != cut {
			return fmt.Errorf("cut %d: --no-repair changed the file", cut)
		}
		if err := must(run("", "check", tp))(0, "OK"); err != nil {
			return fmt.Errorf("cut %d: check: %v", cut, err)
		}
		repaired, _ := os.ReadFile(tp)
		if !bytes.Equal(repaired, clean) {
			return fmt.Errorf("cut %d: NEXUS repaired to %d bytes, want %d", cut, len(repaired), len(clean))
		}
		os.Remove(tp)
		os.Remove(tp + ".lock")
	}
	return nil
}

func checkMiddleCorruption(dir string) error {
	p, err := nexusDB(dir)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	b[32+32+11+1] ^= 0x20 // inside the first frame's payload; 5 valid frames follow
	if err := os.WriteFile(p, b, 0o644); err != nil {
		return err
	}
	if _, _, err := verify(p); !errors.Is(err, recovery.ErrCorruption) {
		return fmt.Errorf("independent: err %v, want ErrCorruption", err)
	}
	if err := must(run("", "check", p, "--no-repair"))(1, "CORRUPT"); err != nil {
		return err
	}
	if err := must(run("", "check", p))(1, "CORRUPT"); err != nil {
		return err
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(after, b) {
		return errors.New("a refusal changed the file")
	}
	return nil
}

func checkCompactionIdentical(dir string) error {
	p, err := nexusDB(dir)
	if err != nil {
		return err
	}
	q := filepath.Join(dir, "copy.kv")
	if err := copyFile(p, q); err != nil {
		return err
	}
	if err := must(run("1 compact\n", "serve", p))(0, "ACK 1"); err != nil {
		return err
	}
	h, err := recovery.Open(recovery.OSFS{}, q, false)
	if err != nil {
		return err
	}
	if err := h.Compact(); err != nil {
		h.Close()
		return err
	}
	h.Close()
	a, _ := os.ReadFile(p)
	b, _ := os.ReadFile(q)
	if !bytes.Equal(a, b) {
		return fmt.Errorf("compacted files differ (%d vs %d bytes)", len(a), len(b))
	}
	return nil
}
