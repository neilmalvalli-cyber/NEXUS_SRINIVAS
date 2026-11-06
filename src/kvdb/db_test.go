package kvdb

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func openT(t *testing.T, path string) *DB {
	t.Helper()
	db, err := Open(path, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return db
}

func mustGet(t *testing.T, db *DB, k string) string {
	t.Helper()
	v, err := db.Get([]byte(k))
	if err != nil {
		t.Fatalf("Get(%q): %v", k, err)
	}
	return string(v)
}

func notFound(t *testing.T, db *DB, k string) {
	t.Helper()
	if _, err := db.Get([]byte(k)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(%q) = %v, want ErrNotFound", k, err)
	}
}

func TestOpenValidatesArguments(t *testing.T) {
	dir := t.TempDir()
	for _, mb := range []int64{-1, 1, 31} {
		if _, err := Open(filepath.Join(dir, "a.kv"), mb); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("max_bytes %d: %v", mb, err)
		}
	}
	if _, err := Open("", 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty path: %v", err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatal("rejected Open created files")
	}
	for _, mb := range []int64{0, 32, 1 << 30} {
		db, err := Open(filepath.Join(dir, fmt.Sprintf("ok%d.kv", mb)), mb)
		if err != nil {
			t.Fatalf("max_bytes %d: %v", mb, err)
		}
		db.Close()
	}
}

func TestAPIRoundTripAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.kv")
	db := openT(t, path)
	if err := db.Put([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	maxVal := bytes.Repeat([]byte{0xAB}, MaxValueLen)
	maxKey := bytes.Repeat([]byte("k"), MaxKeyLen)
	if err := db.Put(maxKey, maxVal); err != nil {
		t.Fatal(err)
	}
	if err := db.Put([]byte("empty"), nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete([]byte("never-existed")); err != nil {
		t.Fatalf("delete of missing key: %v", err)
	}
	if err := db.Batch(nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	// Batch semantics: in order, last operation wins.
	err := db.Batch([]Op{
		{Kind: OpPut, Key: []byte("x"), Value: []byte("1")}, {Kind: OpPut, Key: []byte("x"), Value: []byte("2")},
		{Kind: OpPut, Key: []byte("y"), Value: []byte("1")}, {Kind: OpDelete, Key: []byte("y")},
		{Kind: OpDelete, Key: []byte("z")}, {Kind: OpPut, Key: []byte("z"), Value: []byte("3")},
		{Kind: OpDelete, Key: []byte("a")},
	})
	if err != nil {
		t.Fatal(err)
	}
	check := func(db *DB) {
		t.Helper()
		if mustGet(t, db, "x") != "2" || mustGet(t, db, "z") != "3" {
			t.Fatal("batch last-wins semantics broken")
		}
		notFound(t, db, "y")
		notFound(t, db, "a")
		if v, _ := db.Get(maxKey); !bytes.Equal(v, maxVal) {
			t.Fatal("1 MiB value mismatch")
		}
		if v, err := db.Get([]byte("empty")); err != nil || v == nil || len(v) != 0 {
			t.Fatalf("empty value: %v %v", v, err)
		}
	}
	check(db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openT(t, path)
	defer db.Close()
	check(db)
}

func TestAPIValidationErrors(t *testing.T) {
	db := openT(t, filepath.Join(t.TempDir(), "db.kv"))
	defer db.Close()
	long := bytes.Repeat([]byte("k"), MaxKeyLen+1)
	cases := map[string]error{
		"put empty key":   db.Put(nil, []byte("v")),
		"put long key":    db.Put(long, nil),
		"put big value":   db.Put([]byte("k"), make([]byte, MaxValueLen+1)),
		"delete empty":    db.Delete(nil),
		"delete long":     db.Delete(long),
		"batch too large": db.Batch(make([]Op, MaxBatchOps+1)),
		"batch bad kind":  db.Batch([]Op{{Kind: 7, Key: []byte("k")}}),
		"batch del value": db.Batch([]Op{{Kind: OpDelete, Key: []byte("k"), Value: []byte("v")}}),
	}
	_, cases["get empty"] = db.Get(nil)
	_, cases["get long"] = db.Get(long)
	for name, err := range cases {
		if !errors.Is(err, ErrInvalidArgument) && !errors.Is(err, ErrTooLarge) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := db.Put([]byte("k"), make([]byte, MaxValueLen+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("big value: %v", err)
	}
}

func TestMaxBatchOfDistinctKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.kv")
	db := openT(t, path)
	ops := make([]Op, MaxBatchOps)
	for i := range ops {
		ops[i] = Op{Kind: OpPut, Key: []byte(fmt.Sprintf("key-%05d", i)), Value: []byte(strconv.Itoa(i))}
	}
	if err := db.Batch(ops); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db = openT(t, path)
	defer db.Close()
	for _, i := range []int{0, 4999, MaxBatchOps - 1} {
		if mustGet(t, db, fmt.Sprintf("key-%05d", i)) != strconv.Itoa(i) {
			t.Fatalf("key %d", i)
		}
	}
}

func TestLockedAndClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.kv")
	db := openT(t, path)
	if _, err := Open(path, 0); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Open: %v", err)
	}
	db.Put([]byte("a"), []byte("1"))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	for name, err := range map[string]error{
		"put":    db.Put([]byte("a"), []byte("2")),
		"delete": db.Delete([]byte("a")),
		"batch":  db.Batch([]Op{{Kind: OpPut, Key: []byte("b"), Value: nil}}),
		"empty":  db.Batch(nil),
	} {
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("%s after Close: %v", name, err)
		}
	}
	if _, err := db.Get([]byte("a")); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestBadFormatFileIsUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	content := []byte("my important notes, definitely not a database")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, 0); !errors.Is(err, ErrBadFormat) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, content) {
		t.Fatal("file changed")
	}
}

// ---- real process kills (the official crash model) ----

const (
	killHelperEnv  = "KVDB_KILL_HELPER_DB"
	killHelperMode = "KVDB_KILL_HELPER_MODE"
)

// TestHelperWriterProcess is not a real test. As a child process it writes
// forever and prints "ACK <n>" only after the write for n returned nil.
//
//	mode "put":   Put(key-n, value-n) with values up to 300 KiB
//	mode "batch": Batch of 50 keys b<n>-<j> = n, plus Delete of b<n-1>-0
func TestHelperWriterProcess(t *testing.T) {
	path := os.Getenv(killHelperEnv)
	if path == "" {
		t.Skip("helper process only")
	}
	var maxBytes int64
	if os.Getenv(killHelperMode) == "capped" {
		maxBytes = cappedMax
	}
	db, err := Open(path, maxBytes)
	if err != nil {
		fmt.Println("ERROR", err)
		os.Exit(3)
	}
	out := bufio.NewWriterSize(os.Stdout, 64)
	for n := 0; ; n++ {
		switch os.Getenv(killHelperMode) {
		case "capped":
			err = db.Put([]byte(fmt.Sprintf("c%d", n%cappedKeys)), cappedValue(n))
		case "put":
			err = db.Put([]byte(fmt.Sprintf("key-%d", n)), killValue(n))
		default:
			ops := make([]Op, 0, 51)
			for j := 0; j < 50; j++ {
				ops = append(ops, Op{Kind: OpPut, Key: []byte(fmt.Sprintf("b%d-%d", n, j)), Value: []byte(strconv.Itoa(n))})
			}
			ops = append(ops, Op{Kind: OpDelete, Key: []byte(fmt.Sprintf("b%d-0", n-1))})
			err = db.Batch(ops)
		}
		if err != nil {
			fmt.Println("ERROR", err)
			os.Exit(4)
		}
		fmt.Fprintf(out, "ACK %d\n", n)
		out.Flush() // the ACK leaves the process only after the write returned
	}
}

func killValue(n int) []byte {
	size := (n * 7919) % (300 << 10)
	return bytes.Repeat([]byte{byte('a' + n%26)}, size)
}

// A child writes in a loop; the parent kills it (TerminateProcess on
// Windows) at a random moment, then reopens. Every acknowledged write is
// present and correct; the only other possible change is the single write
// that was in flight, all or nothing.
func TestKillDuringWritesLosesNothingAcknowledged(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns child processes")
	}
	for _, mode := range []string{"put", "batch"} {
		for round := 0; round < 3; round++ {
			t.Run(fmt.Sprintf("%s-%d", mode, round), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "db.kv")
				last := runAndKill(t, path, mode, 20+round*37)
				db := openT(t, path)
				defer db.Close()
				switch mode {
				case "put":
					for n := 0; n <= last; n++ {
						if v, err := db.Get([]byte(fmt.Sprintf("key-%d", n))); err != nil || !bytes.Equal(v, killValue(n)) {
							t.Fatalf("acknowledged key-%d lost or wrong: %v", n, err)
						}
					}
					if v, err := db.Get([]byte(fmt.Sprintf("key-%d", last+1))); err == nil && !bytes.Equal(v, killValue(last+1)) {
						t.Fatal("in-flight write is torn")
					}
					notFound(t, db, fmt.Sprintf("key-%d", last+2))
				default:
					for n := 0; n <= last; n++ {
						for j := 0; j < 50; j++ {
							k := fmt.Sprintf("b%d-%d", n, j)
							if j == 0 && n < last {
								notFound(t, db, k) // deleted by batch n+1
								continue
							}
							if j == 0 && n == last {
								continue // deleted only if the in-flight batch landed
							}
							if mustGet(t, db, k) != strconv.Itoa(n) {
								t.Fatalf("%s wrong", k)
							}
						}
					}
					// The in-flight batch: all 51 operations or none.
					_, err := db.Get([]byte(fmt.Sprintf("b%d-1", last+1)))
					landed := err == nil
					for j := 1; j < 50; j++ {
						_, err := db.Get([]byte(fmt.Sprintf("b%d-%d", last+1, j)))
						if (err == nil) != landed {
							t.Fatal("in-flight batch partially present")
						}
					}
					_, err = db.Get([]byte(fmt.Sprintf("b%d-0", last)))
					if landed == (err == nil) {
						t.Fatal("in-flight batch's delete does not match its puts")
					}
				}
			})
		}
	}
}

// runAndKill starts the writer, kills it after at least minAcks ACKs, and
// returns the highest acknowledged n.
func runAndKill(t *testing.T, path, mode string, minAcks int) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperWriterProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), killHelperEnv+"="+path, killHelperMode+"="+mode)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	last := -1
	acks := make(chan int, 1024)
	done := make(chan error, 1)
	go func() {
		s := bufio.NewScanner(stdout)
		for s.Scan() {
			line := s.Text()
			if n, ok := strings.CutPrefix(line, "ACK "); ok {
				v, _ := strconv.Atoi(n)
				acks <- v
			} else if strings.HasPrefix(line, "ERROR") {
				done <- errors.New(line)
				return
			}
		}
		done <- nil
	}()
	deadline := time.After(60 * time.Second)
	for last < minAcks {
		select {
		case n := <-acks:
			last = n
		case err := <-done:
			t.Fatalf("writer stopped early: %v", err)
		case <-deadline:
			cmd.Process.Kill()
			t.Fatal("writer too slow")
		}
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	<-done
	close(acks)
	for n := range acks { // ACKs printed before the kill also count
		last = n
	}
	return last
}

// ---- Phase 3: max_bytes and compaction through the public API ----

const (
	cappedMax  = 64 << 10
	cappedKeys = 12
)

func cappedValue(n int) []byte {
	return bytes.Repeat([]byte{byte('A' + n%26)}, 1000+(n*331)%3000)
}

func fileSizeOf(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

func TestMaxBytesThroughAPI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.kv")
	const capBytes = 8 << 10
	db, err := Open(path, capBytes)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if err := db.Put([]byte(fmt.Sprintf("k%d", i%5)), bytes.Repeat([]byte("v"), 900)); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
		if s := fileSizeOf(t, path); s > capBytes {
			t.Fatalf("put %d: file %d over cap", i, s)
		}
	}
	if err := db.Put([]byte("huge"), bytes.Repeat([]byte("h"), capBytes)); !errors.Is(err, ErrFull) {
		t.Fatalf("oversized put: %v", err)
	}
	if err := db.Batch([]Op{{Kind: OpPut, Key: []byte("x"), Value: bytes.Repeat([]byte("x"), 3000)}, {Kind: OpPut, Key: []byte("y"), Value: bytes.Repeat([]byte("y"), 3000)}, {Kind: OpPut, Key: []byte("z"), Value: bytes.Repeat([]byte("z"), 3000)}}); !errors.Is(err, ErrFull) {
		t.Fatalf("batch over cap: %v", err)
	}
	if _, err := db.Get([]byte("x")); !errors.Is(err, ErrNotFound) {
		t.Fatal("rejected batch partially applied")
	}
	db.Close()
	// Reopen with a smaller cap: opens (rule 1), deletes shrink, puts refused.
	db, err = Open(path, 2048)
	if err != nil {
		t.Fatalf("over-cap open: %v", err)
	}
	defer db.Close()
	if err := db.Put([]byte("k0"), []byte("small")); !errors.Is(err, ErrFull) {
		t.Fatalf("put on over-cap db: %v", err)
	}
	before := fileSizeOf(t, path)
	if err := db.Delete([]byte("k1")); err != nil {
		t.Fatal(err)
	}
	if fileSizeOf(t, path) >= before {
		t.Fatal("delete on over-cap db did not shrink it")
	}
}

func TestCompactThroughAPI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.kv")
	db := openT(t, path)
	for i := 0; i < 300; i++ {
		db.Put([]byte(fmt.Sprintf("k%d", i%10)), bytes.Repeat([]byte{byte(i)}, 500))
	}
	before := fileSizeOf(t, path)
	if err := db.Compact(); err != nil {
		t.Fatal(err)
	}
	if after := fileSizeOf(t, path); after >= before {
		t.Fatalf("compact: %d -> %d", before, after)
	}
	if ents, _ := os.ReadDir(filepath.Dir(path)); len(ents) != 2 { // db.kv + db.kv.lock
		t.Fatalf("leftover files: %v", ents)
	}
	db.Close()
	if err := db.Compact(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Compact after Close: %v", err)
	}
	db = openT(t, path)
	defer db.Close()
	for k := 0; k < 10; k++ {
		if v := mustGet(t, db, fmt.Sprintf("k%d", k)); len(v) != 500 || v[0] != byte(290+k) {
			t.Fatalf("k%d wrong after compaction", k)
		}
	}
}

// A capped database whose every few writes is a compacting commit (two
// renames each) is killed at random moments. Every acknowledged write
// survives; the file stays within the cap; no extra file is left after the
// reopen.
func TestKillDuringCompactingCommits(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns child processes")
	}
	for round := 0; round < 6; round++ {
		t.Run(fmt.Sprint(round), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "db.kv")
			last := runAndKill(t, path, "capped", 40+round*53)
			db, err := Open(path, cappedMax)
			if err != nil {
				t.Fatalf("reopen after kill: %v", err)
			}
			defer db.Close()
			for k := 0; k < cappedKeys; k++ {
				n := last - ((last-k)%cappedKeys+cappedKeys)%cappedKeys // latest acked write of key k
				if n < 0 {
					continue
				}
				v, err := db.Get([]byte(fmt.Sprintf("c%d", k)))
				if err != nil {
					t.Fatalf("c%d: %v", k, err)
				}
				inFlight := last + 1
				if !bytes.Equal(v, cappedValue(n)) && !(inFlight%cappedKeys == k && bytes.Equal(v, cappedValue(inFlight))) {
					t.Fatalf("c%d holds neither acked write %d nor the in-flight one", k, n)
				}
			}
			if s := fileSizeOf(t, path); s > cappedMax {
				t.Fatalf("file %d over cap after reopen", s)
			}
			ents, _ := os.ReadDir(filepath.Dir(path))
			for _, e := range ents {
				if n := e.Name(); n != "db.kv" && n != "db.kv.lock" {
					t.Fatalf("leftover %s after reopen", n)
				}
			}
		})
	}
}
