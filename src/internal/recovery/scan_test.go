package recovery

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio/faultfs"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/index"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/locking"
)

type indexLoc = index.Loc

// ---- file construction helpers (Phase 1 encoder, byte by byte) ----

func put(k, v string) format.Entry {
	return format.Entry{Op: format.OpPut, Key: []byte(k), Value: []byte(v)}
}
func del(k string) format.Entry { return format.Entry{Op: format.OpDelete, Key: []byte(k)} }

func frame(t testing.TB, seq uint64, es ...format.Entry) []byte {
	t.Helper()
	var b bytes.Buffer
	if _, err := format.WriteFrame(&b, seq, es, format.MaxFrameOps); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func superblock(t testing.TB, firstSeq, snapEnd uint64) []byte {
	t.Helper()
	b, err := format.Superblock{FirstSeq: firstSeq, SnapshotEnd: snapEnd}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b[:]
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// reseal recomputes the payload CRC and header CRC of a whole frame after its
// payload was edited, producing a CRC-valid frame (for C7 tests).
func reseal(t testing.TB, fr []byte) []byte {
	t.Helper()
	fr = append([]byte(nil), fr...)
	h, err := format.DecodeFrameHeader(fr)
	if err != nil {
		t.Fatal(err)
	}
	h.PayloadCRC = format.Checksum(fr[format.FrameHeaderSize:])
	hb, err := h.Encode()
	if err != nil {
		t.Fatal(err)
	}
	copy(fr, hb[:])
	return fr
}

func writeDB(t *testing.T, data []byte) (dir, db string) {
	t.Helper()
	dir = t.TempDir()
	db = filepath.Join(dir, "db.kv")
	if err := os.WriteFile(db, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, db
}

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// contents reads every indexed value back through the handle and verifies
// each entry (CRC, key), returning key -> value.
func contents(t *testing.T, st *State) map[string]string {
	t.Helper()
	out := map[string]string{}
	var bad error
	st.Scan.Index.Range(func(k string, l indexLoc) bool {
		b := make([]byte, l.Size)
		if err := fileio.ReadFullAt(st.File, b, l.Off); err != nil {
			bad = err
			return false
		}
		_, v, err := format.DecodeEntryForKey(b, []byte(k))
		if err != nil {
			bad = fmt.Errorf("key %q: %w", k, err)
			return false
		}
		out[k] = string(v)
		return true
	})
	if bad != nil {
		t.Fatal(bad)
	}
	return out
}

// openState opens db, returns its contents and closes it.
func openState(t *testing.T, fsys fileio.FS, db string) (map[string]string, *State) {
	t.Helper()
	st, err := Open(fsys, db, fastOpts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if sz, err := st.File.Size(); err != nil || sz != st.Scan.End {
		t.Fatalf("file size %d (%v) != End %d after Open", sz, err, st.Scan.End)
	}
	return contents(t, st), st
}

// wantRefusal checks that Open fails with want (and not the other format
// error), that nothing was written or truncated, and that the lock is free.
func wantRefusal(t *testing.T, db string, want error) error {
	t.Helper()
	before := readFile(t, db)
	ffs := faultfs.New(fileio.OS{})
	_, err := Open(ffs, db, fastOpts)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	other := dberr.ErrCorruption
	if want == dberr.ErrCorruption {
		other = dberr.ErrBadFormat
	}
	if errors.Is(err, other) {
		t.Fatalf("err is also %v: %v", other, err)
	}
	if m := mutating(ffs, locking.Path(db)); len(m) != 0 {
		t.Fatalf("refusal modified the file: %v", m)
	}
	for _, e := range ffs.Events() {
		if e.Op == faultfs.OpSync {
			t.Fatalf("refusal synced: %s", e)
		}
	}
	if !bytes.Equal(before, readFile(t, db)) {
		t.Fatal("file bytes changed")
	}
	if ffs.OpenHandles() != 0 {
		t.Fatalf("%d handles leaked", ffs.OpenHandles())
	}
	l, lerr := locking.Acquire(fileio.OS{}, db, fileio.NoRetry)
	if lerr != nil {
		t.Fatalf("lock not released: %v", lerr)
	}
	l.Release()
	return err
}

// base is a small log used by many tests: 3 frames, first_seq 7.
//
//	seq 7: put a=1, put b=2
//	seq 8: del a, put c=3       (one batch)
//	seq 9: put b=22
func base(t testing.TB) (data []byte, want map[string]string) {
	data = cat(superblock(t, 7, 32),
		frame(t, 7, put("a", "1"), put("b", "2")),
		frame(t, 8, del("a"), put("c", "3")),
		frame(t, 9, put("b", "22")))
	return data, map[string]string{"b": "22", "c": "3"}
}

// ---- clean logs ----

func TestOpenRebuildsIndex(t *testing.T) {
	data, want := base(t)
	_, db := writeDB(t, data)
	got, st := openState(t, fileio.OS{}, db)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("contents = %v, want %v", got, want)
	}
	s := st.Scan
	if s.Frames != 3 || s.NextSeq != 10 || s.Tail != TailClean || st.Truncated || s.End != int64(len(data)) {
		t.Fatalf("scan = %+v truncated=%v", s, st.Truncated)
	}
	live := s.Index.LiveBytes()
	if live != uint64((format.EntryHeaderSize+1+2)+(format.EntryHeaderSize+1+1)) { // b=22, c=3
		t.Fatalf("live bytes = %d", live)
	}
	if s.DeadBytes() != uint64(len(data))-32-live {
		t.Fatalf("dead bytes = %d", s.DeadBytes())
	}
	if !bytes.Equal(data, readFile(t, db)) {
		t.Fatal("clean Open changed the file")
	}
}

func TestOpenFreshDatabase(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db.kv")
	got, st := openState(t, fileio.OS{}, db)
	if len(got) != 0 || st.Scan.NextSeq != 1 || st.Scan.Frames != 0 || st.Truncated || !st.Resolution.Created {
		t.Fatalf("fresh: %v %+v", got, st.Scan)
	}
}

func TestStateCloseReleasesEverything(t *testing.T) {
	data, _ := base(t)
	dir, db := writeDB(t, data)
	ffs := faultfs.New(fileio.OS{})
	st, err := Open(ffs, db, fastOpts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(fileio.OS{}, db, fastOpts); !errors.Is(err, dberr.ErrLocked) {
		t.Fatalf("second Open: %v, want ErrLocked", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if ffs.OpenHandles() != 0 {
		t.Fatalf("%d handles open after Close", ffs.OpenHandles())
	}
	// Windows: the data file can be moved once the handle is closed.
	if err := os.Rename(db, filepath.Join(dir, "moved")); err != nil {
		t.Fatalf("data file still held: %v", err)
	}
}

// ---- tail rules ----

// T-REC-1 (C1): 1..31 trailing bytes are cut; Sync follows the truncate.
func TestTailC1ShortTrailer(t *testing.T) {
	data, want := base(t)
	for n := 1; n < format.FrameHeaderSize; n++ {
		for _, fill := range []byte{0x00, 0xA5} {
			_, db := writeDB(t, cat(data, bytes.Repeat([]byte{fill}, n)))
			ffs := faultfs.New(fileio.OS{})
			got, st := openState(t, ffs, db)
			if !reflect.DeepEqual(got, want) || st.Scan.Tail != TailC1 || !st.Truncated {
				t.Fatalf("n=%d: %v tail=%q", n, got, st.Scan.Tail)
			}
			if !bytes.Equal(readFile(t, db), data) {
				t.Fatalf("n=%d: not truncated to the last frame end", n)
			}
			assertTruncateThenSync(t, ffs, db, int64(len(data)))
		}
	}
}

func assertTruncateThenSync(t *testing.T, ffs *faultfs.FS, db string, _ int64) {
	t.Helper()
	ti, si := -1, -1
	for i, e := range ffs.Events() {
		if e.Path != db {
			continue
		}
		if e.Op == faultfs.OpTruncate {
			if ti >= 0 {
				t.Fatal("more than one truncate")
			}
			ti = i
		}
		if e.Op == faultfs.OpSync && ti >= 0 && si < 0 {
			si = i
		}
		if e.Op == faultfs.OpWrite {
			t.Fatalf("recovery wrote to the data file:\n%s", ffs.TraceString())
		}
	}
	if ti < 0 || si < 0 {
		t.Fatalf("missing truncate (%d) or sync after it (%d):\n%s", ti, si, ffs.TraceString())
	}
}

// T-REC-2 (C2): a damaged header (bad magic, bad header CRC), with or
// without arbitrary bytes after it, is the torn final frame.
func TestTailC2DamagedHeader(t *testing.T) {
	data, want := base(t)
	next := frame(t, 10, put("d", "4"))
	badMagic := append([]byte(nil), next...)
	badMagic[0] ^= 0xFF
	badCRC := append([]byte(nil), next...)
	badCRC[9] ^= 0x01 // a seq byte: header CRC no longer matches
	cases := map[string][]byte{
		"bad-magic":           badMagic[:format.FrameHeaderSize],
		"bad-magic+payload":   badMagic,
		"bad-header-crc":      badCRC,
		"bad-header-crc+junk": cat(badCRC, []byte("arbitrary junk after the frame")),
		"zero-filled-4k":      make([]byte, 4096),
		"zero-header+ones":    cat(make([]byte, 32), bytes.Repeat([]byte{1}, 100)),
		"header-crc-zeroed":   cat(next[:28], []byte{0, 0, 0, 0}, next[32:]),
		"ascii-text-at-tail":  []byte(strings.Repeat("not a frame header ", 4)),
		"bad-magic-only-32":   bytes.Repeat([]byte{0xFF}, 32),
		// A valid frame with seq < expected after the junk is not evidence of
		// committed data (it cannot be a later write), so C2 still applies.
		"bad-magic-plus-stale-frame": cat(bytes.Repeat([]byte{0xEE}, 32), frame(t, 3, put("x", "y"))),
	}
	for name, tail := range cases {
		t.Run(name, func(t *testing.T) {
			_, db := writeDB(t, cat(data, tail))
			got, st := openState(t, fileio.OS{}, db)
			if !reflect.DeepEqual(got, want) || st.Scan.Tail != TailC2 {
				t.Fatalf("%v tail=%q cause=%v", got, st.Scan.Tail, st.Scan.TailCause)
			}
			if !bytes.Equal(readFile(t, db), data) {
				t.Fatal("not truncated to the damaged header start")
			}
		})
	}
}

// T-REC-3 (C3, amended): a damaged header followed by a complete, fully valid
// frame with seq >= expected cannot be a torn final write (frames are written
// one at a time and each is synced before the next starts). Truncating would
// drop committed data, so open refuses with ErrCorruption and leaves the file
// byte-for-byte unchanged. Nothing past the damaged header is ever applied.
func TestTailC3MidLogHeaderDamageRefused(t *testing.T) {
	data, _ := base(t)
	damaged := frame(t, 10, put("d", "4"))
	damaged[0] ^= 0xFF
	badCRC := frame(t, 10, put("d", "4"))
	badCRC[9] ^= 0x01
	cases := map[string][]byte{
		"bad-magic+next-frame":      cat(data, damaged, frame(t, 11, put("e", "5"))),
		"bad-header-crc+next-frame": cat(data, badCRC, frame(t, 11, put("e", "5"))),
		"junk-header+same-seq":      cat(data, bytes.Repeat([]byte{0xEE}, 32), frame(t, 10, put("x", "y"))),
		"bad-magic+two-frames":      cat(data, damaged, frame(t, 11, put("e", "5")), frame(t, 12, put("f", "6"))),
	}
	for name, file := range cases {
		t.Run(name, func(t *testing.T) {
			_, db := writeDB(t, file)
			_, err := Open(fileio.OS{}, db, Options{})
			if !errors.Is(err, dberr.ErrCorruption) {
				t.Fatalf("err = %v, want ErrCorruption", err)
			}
			var se *ScanError
			if !errors.As(err, &se) || se.Rule != "C3" || se.Offset != uint64(len(data)) {
				t.Fatalf("err = %#v, want ScanError rule C3 at offset %d", err, len(data))
			}
			if !bytes.Equal(readFile(t, db), file) {
				t.Fatal("refused open modified the file")
			}
		})
	}
}

// Documented trade-off of amended C3 (KNOWN_LIMITATIONS.md). A process kill
// leaves a prefix of the last frame, so its header is intact and C1/C4
// apply; the forward check never runs. Only an out-of-order power loss that
// loses the header sector but keeps later sectors, AND a value that embeds a
// complete valid frame with a future seq, reaches this state. NEXUS then
// refuses (availability loss) and never truncates or modifies the file: it
// fails safe instead of risking silent loss of committed frames.
func TestTailC3CraftedValueBehindLostHeaderRefusesSafely(t *testing.T) {
	data, _ := base(t)
	embedded := frame(t, 11, put("fake", "frame"))
	outer := frame(t, 10, put("k", "prefix|"+string(embedded)+"|suffix"))
	copy(outer[:format.FrameHeaderSize], make([]byte, format.FrameHeaderSize)) // header sector lost
	file := cat(data, outer)
	_, db := writeDB(t, file)
	_, err := Open(fileio.OS{}, db, Options{})
	var se *ScanError
	if !errors.As(err, &se) || se.Rule != "C3" {
		t.Fatalf("err = %v, want C3 refusal", err)
	}
	if !bytes.Equal(readFile(t, db), file) {
		t.Fatal("refused open modified the file")
	}
}

// A damaged header followed only by an incomplete or payload-damaged frame
// is not proof of committed data: C2 still truncates.
func TestTailC2LaterFrameNotProof(t *testing.T) {
	data, want := base(t)
	damaged := frame(t, 10, put("d", "4"))
	damaged[0] ^= 0xFF
	next := frame(t, 11, put("e", "5"))
	badPayload := append([]byte(nil), next...)
	badPayload[len(badPayload)-1] ^= 0xFF
	cases := map[string][]byte{
		"next-frame-cut":         next[:len(next)-3],
		"next-frame-bad-payload": badPayload,
	}
	for name, after := range cases {
		t.Run(name, func(t *testing.T) {
			_, db := writeDB(t, cat(data, damaged, after))
			got, st := openState(t, fileio.OS{}, db)
			if !reflect.DeepEqual(got, want) || st.Scan.Tail != TailC2 {
				t.Fatalf("%v tail=%q", got, st.Scan.Tail)
			}
			if !bytes.Equal(readFile(t, db), data) {
				t.Fatal("not truncated at the damaged header")
			}
		})
	}
}

// T-REC-4 (C3) and T-REC-11: a final frame whose value is full of valid
// frame headers (even with the expected seq) is cut at every byte offset.
// Open always succeeds, truncates to the frame start, and the index is the
// state before that frame: a torn batch never partially appears.
func TestTailKillPointSweep(t *testing.T) {
	data, want := base(t)
	trap := cat(frame(t, 10, put("x", "evil")), frame(t, 11, put("y", "evil")), frame(t, 10, put("z", "evil")))
	last := frame(t, 10, put("b", "overwritten?"), del("c"), put("v", string(trap)), put("w", "after"))
	for cut := 1; cut < len(last); cut++ {
		_, db := writeDB(t, cat(data, last[:cut]))
		got, st := openState(t, fileio.OS{}, db)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("cut %d: contents %v, want %v", cut, got, want)
		}
		wantRule := TailC4
		if cut < format.FrameHeaderSize {
			wantRule = TailC1
		}
		if st.Scan.Tail != wantRule || st.Scan.NextSeq != 10 {
			t.Fatalf("cut %d: tail %q next %d", cut, st.Scan.Tail, st.Scan.NextSeq)
		}
		if !bytes.Equal(readFile(t, db), data) {
			t.Fatalf("cut %d: not truncated to the frame start", cut)
		}
		// Repeatable: a second Open finds a clean log with the same state.
		got2, st2 := openState(t, fileio.OS{}, db)
		if !reflect.DeepEqual(got2, want) || st2.Truncated {
			t.Fatalf("cut %d: second Open differs", cut)
		}
	}
	// The complete frame is applied as one batch.
	_, db := writeDB(t, cat(data, last))
	got, _ := openState(t, fileio.OS{}, db)
	wantAll := map[string]string{"b": "overwritten?", "v": string(trap), "w": "after"}
	if !reflect.DeepEqual(got, wantAll) {
		t.Fatalf("complete frame: %v", got)
	}
}

// T-REC-5 (C5): last frame, valid header, payload ends exactly at EOF but
// one payload byte is flipped or the payload tail is zero-filled.
func TestTailC5PayloadCRCAtEOF(t *testing.T) {
	data, want := base(t)
	last := frame(t, 10, put("d", "4444"), put("e", "5555"))
	for i := format.FrameHeaderSize; i < len(last); i++ {
		bad := append([]byte(nil), last...)
		bad[i] ^= 0x40
		zero := append([]byte(nil), last...)
		for j := i; j < len(zero); j++ {
			zero[j] = 0
		}
		for name, fr := range map[string][]byte{"flip": bad, "zero": zero} {
			if bytes.Equal(fr, last) {
				continue // zeroing bytes that were already zero
			}
			_, db := writeDB(t, cat(data, fr))
			got, st := openState(t, fileio.OS{}, db)
			if !reflect.DeepEqual(got, want) || st.Scan.Tail != TailC5 {
				t.Fatalf("%s at %d: %v tail=%q cause=%v", name, i, got, st.Scan.Tail, st.Scan.TailCause)
			}
			if !bytes.Equal(readFile(t, db), data) {
				t.Fatalf("%s at %d: not truncated", name, i)
			}
		}
	}
}

// T-REC-6 (C6): the same damaged frame with anything after its declared end
// is refused with ErrCorruption, and the file is unchanged.
func TestTailC6BytesAfterBadPayload(t *testing.T) {
	data, _ := base(t)
	last := frame(t, 10, put("d", "4444"))
	last[len(last)-1] ^= 0x01
	for name, after := range map[string][]byte{
		"one-byte":    {0},
		"valid-frame": frame(t, 11, put("e", "5")),
		"zeros":       make([]byte, 100),
	} {
		t.Run(name, func(t *testing.T) {
			_, db := writeDB(t, cat(data, last, after))
			err := wantRefusal(t, db, dberr.ErrCorruption)
			if !strings.Contains(err.Error(), "rule C6") {
				t.Fatalf("err = %v, want rule C6", err)
			}
		})
	}
}

// T-REC-7 (C4): valid header whose payload_len runs past EOF.
func TestTailC4PayloadPastEOF(t *testing.T) {
	data, want := base(t)
	last := frame(t, 10, put("d", "4444"))
	_, db := writeDB(t, cat(data, last[:len(last)-1]))
	got, st := openState(t, fileio.OS{}, db)
	if !reflect.DeepEqual(got, want) || st.Scan.Tail != TailC4 {
		t.Fatalf("%v %q", got, st.Scan.Tail)
	}
	// A header claiming a huge (but legal) payload, alone at the tail.
	h, err := format.FrameHeader{OpCount: 1, Seq: 10, PayloadLen: format.MaxPayload}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	_, db = writeDB(t, cat(data, h[:], []byte("x")))
	got, st = openState(t, fileio.OS{}, db)
	if !reflect.DeepEqual(got, want) || st.Scan.Tail != TailC4 {
		t.Fatalf("huge payload_len: %v %q", got, st.Scan.Tail)
	}
}

// T-REC-8 (C7): CRC-valid frames that a torn write cannot produce.
func TestTailC7CRCValidButWrong(t *testing.T) {
	data, _ := base(t)
	// Malformed payload with a matching payload CRC: break the entry CRC.
	fr := frame(t, 10, put("d", "4444"))
	fr[format.FrameHeaderSize+7] ^= 0x01 // entry_crc byte
	malformedPayload := reseal(t, fr)
	// CRC-valid header with an invalid field (reserved != 0).
	hdrBad := frame(t, 10, put("d", "4"))
	hdrBad[5] = 1
	binary.LittleEndian.PutUint32(hdrBad[28:], format.Checksum(hdrBad[:28]))
	cases := map[string][]byte{
		"seq-skips":           cat(data, frame(t, 11, put("d", "4"))),
		"seq-repeats":         cat(data, frame(t, 9, put("d", "4"))),
		"first-seq-mismatch":  cat(superblock(t, 7, 32), frame(t, 8, put("a", "1"))),
		"malformed-payload":   cat(data, malformedPayload),
		"malformed-at-eof":    cat(data, malformedPayload), // C7 even though it ends at EOF
		"reserved-field-set":  cat(data, hdrBad),
		"seq-wrong-then-junk": cat(data, frame(t, 12, put("d", "4")), []byte("junk")),
	}
	for name, file := range cases {
		t.Run(name, func(t *testing.T) {
			_, db := writeDB(t, file)
			err := wantRefusal(t, db, dberr.ErrCorruption)
			if !strings.Contains(err.Error(), "rule C7") {
				t.Fatalf("err = %v, want rule C7", err)
			}
		})
	}
}

// T-REC-9 (C0): any damage before snapshot_end is corruption, even when it
// is shaped like a torn tail; nothing below snapshot_end is ever truncated.
func TestTailC0InsideSnapshot(t *testing.T) {
	f1 := frame(t, 7, put("a", "1"))
	f2 := frame(t, 8, put("b", "2"))
	snap := uint64(32 + len(f1) + len(f2))
	sbk := superblock(t, 7, snap)

	c5 := append([]byte(nil), f2...)
	c5[len(c5)-1] ^= 1 // C5 shape: payload CRC bad, frame ends at EOF == snapshot_end
	badHdr := append([]byte(nil), f2...)
	badHdr[0] ^= 0xFF
	cases := map[string][]byte{
		"c5-shaped":         cat(sbk, f1, c5),
		"bad-header":        cat(sbk, f1, badHdr),
		"c5-then-tail":      cat(sbk, f1, c5, []byte{1, 2, 3}),
		"wrong-seq":         cat(sbk, f1, frame(t, 9, put("b", "2"))),
		"boundary-mismatch": cat(superblock(t, 7, snap-1), f1, f2),
	}
	for name, file := range cases {
		t.Run(name, func(t *testing.T) {
			_, db := writeDB(t, file)
			wantRefusal(t, db, dberr.ErrCorruption)
		})
	}
	// Torn tail AFTER the snapshot is still truncated, to >= snapshot_end.
	_, db := writeDB(t, cat(sbk, f1, f2, frame(t, 9, put("c", "3"))[:20]))
	got, st := openState(t, fileio.OS{}, db)
	if !reflect.DeepEqual(got, map[string]string{"a": "1", "b": "2"}) || st.Scan.End != int64(snap) || st.Scan.Tail != TailC1 {
		t.Fatalf("%v %+v", got, st.Scan)
	}
	// A torn frame that starts exactly at snapshot_end is truncatable.
	_, db = writeDB(t, cat(sbk, f1, f2, frame(t, 9, put("c", "3"))[:40]))
	if _, st := openState(t, fileio.OS{}, db); st.Scan.Tail != TailC4 || st.Scan.End != int64(snap) {
		t.Fatalf("%+v", st.Scan)
	}
}

// T-REC-10 / T-ERR-4 (C8): a read error at any read position fails Open with
// that error (never ErrCorruption, ErrBadFormat or success) and nothing is
// truncated or written. A short read (EOF before the known size) is treated
// the same way: it is never evidence of a torn write.
func TestReadErrorsNeverTruncate(t *testing.T) {
	small, _ := base(t)
	big := cat(superblock(t, 1, 32),
		frame(t, 1, put("k1", strings.Repeat("v", format.MaxValueLen))),
		frame(t, 2, put("k2", strings.Repeat("w", format.MaxValueLen))),
		frame(t, 3, put("k3", strings.Repeat("x", format.MaxValueLen))),
		frame(t, 4, put("k4", strings.Repeat("y", format.MaxValueLen))),
		frame(t, 5, put("k5", strings.Repeat("z", format.MaxValueLen))),
		[]byte{1, 2, 3}) // torn tail: a successful Open would truncate
	for name, data := range map[string][]byte{"small-torn": cat(small, []byte{9, 9}), "5MiB-torn": big} {
		t.Run(name, func(t *testing.T) {
			_, db := writeDB(t, data)
			dry := faultfs.New(fileio.OS{})
			st, err := Open(dry, db, fastOpts)
			if err != nil {
				t.Fatal(err)
			}
			st.Close()
			reads := 0
			for _, e := range dry.Events() {
				if e.Op == faultfs.OpRead && e.Path == db {
					reads++
				}
			}
			if reads < 3 {
				t.Fatalf("only %d reads; test is too weak", reads)
			}
			for i := 0; i < reads; i++ {
				for _, r := range []faultfs.Rule{
					{Op: faultfs.OpRead, Skip: i, Count: 1, Err: errX},
					{Op: faultfs.OpRead, Skip: i, Count: 1, Mode: faultfs.Partial, Keep: 1, Err: io.EOF},
				} {
					_, db := writeDB(t, data)
					r.Path = db
					ffs := faultfs.New(fileio.OS{})
					ffs.Add(r)
					st, err := Open(ffs, db, fastOpts)
					if err == nil {
						st.Close()
						t.Fatalf("read %d (%v): Open succeeded", i, r.Err)
					}
					if errors.Is(err, dberr.ErrCorruption) || errors.Is(err, dberr.ErrBadFormat) {
						t.Fatalf("read %d: I/O error became %v", i, err)
					}
					if r.Err == errX && !errors.Is(err, errX) {
						t.Fatalf("read %d: err %v does not wrap the injected error", i, err)
					}
					for _, e := range ffs.Events() {
						if e.Op == faultfs.OpTruncate || e.Op == faultfs.OpWrite {
							t.Fatalf("read %d: %s after a read error", i, e)
						}
					}
					if !bytes.Equal(readFile(t, db), data) {
						t.Fatalf("read %d: file changed", i)
					}
				}
			}
		})
	}
}

// T-ERR-1: not our file -> ErrBadFormat, never ErrCorruption.
// T-ERR-2: our file with impossible content -> ErrCorruption.
func TestErrorMeanings(t *testing.T) {
	v2 := superblock(t, 1, 32)
	binary.LittleEndian.PutUint16(v2[8:], 2)
	binary.LittleEndian.PutUint32(v2[28:], format.Checksum(v2[:28]))
	for name, data := range map[string][]byte{
		"random":    []byte("\x13\x37 random bytes that are clearly not a database file at all"),
		"empty":     {},
		"version-2": v2,
	} {
		t.Run("bad-format/"+name, func(t *testing.T) {
			_, db := writeDB(t, data)
			wantRefusal(t, db, dberr.ErrBadFormat)
		})
	}
	data, _ := base(t)
	t.Run("corruption/snapshot-end-beyond-eof", func(t *testing.T) {
		_, db := writeDB(t, cat(superblock(t, 7, uint64(len(data)+1)), data[32:]))
		wantRefusal(t, db, dberr.ErrCorruption)
	})
}

// D2 end to end: a scan refusal leaves the stale temps alone, so a valid
// .old survives for manual recovery; after a successful Open they are gone.
func TestStaleTempsSurviveScanRefusal(t *testing.T) {
	data, _ := base(t)
	bad := append([]byte(nil), data...)
	bad[len(bad)-1] ^= 1 // C5-shaped
	bad = append(bad, 0) // ...with a byte after it: C6
	dir, db := writeDB(t, bad)
	old := superblock(t, 1, 32)
	if err := os.WriteFile(db+SuffixOld, old, 0o644); err != nil {
		t.Fatal(err)
	}
	wantRefusal(t, db, dberr.ErrCorruption)
	if !bytes.Equal(readFile(t, db+SuffixOld), old) {
		t.Fatal(".old was touched")
	}
	if err := os.WriteFile(db, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, st := openState(t, fileio.OS{}, db); len(st.Warnings) != 0 {
		t.Fatal(st.Warnings)
	}
	if got := names(snapshot(t, dir)); !reflect.DeepEqual(got, []string{"db.kv"}) {
		t.Fatalf("after successful Open: %v", got)
	}
}

// Sequence exhaustion: a frame with seq 2^64-1 leaves NextSeq = 0; any
// CRC-valid frame after it is corruption.
func TestSequenceExhaustion(t *testing.T) {
	last := uint64(math.MaxUint64)
	data := cat(superblock(t, last, 32), frame(t, last, put("a", "1")))
	_, db := writeDB(t, data)
	got, st := openState(t, fileio.OS{}, db)
	if st.Scan.NextSeq != 0 || got["a"] != "1" {
		t.Fatalf("NextSeq=%d %v", st.Scan.NextSeq, got)
	}
	_, db = writeDB(t, cat(data, frame(t, 1, put("b", "2"))))
	wantRefusal(t, db, dberr.ErrCorruption)
	// A torn tail after it is still just a torn tail.
	_, db = writeDB(t, cat(data, []byte{1, 2, 3}))
	if _, st := openState(t, fileio.OS{}, db); st.Scan.Tail != TailC1 || st.Scan.NextSeq != 0 {
		t.Fatalf("%+v", st.Scan)
	}
}

// ---- fault and crash sweeps over Open on a torn log ----

func tornLog(t *testing.T) (data, clean []byte, want map[string]string) {
	clean, want = base(t)
	return cat(clean, frame(t, 10, put("d", "4"), put("e", "5"))[:30]), clean, want
}

// Every operation of Open (lock, resolve, open, scan, truncate, sync,
// cleanup) fails in turn. Open either succeeds or returns the injected error;
// a clean re-run always reaches the same file bytes and contents.
func TestOpenFaultSweep(t *testing.T) {
	data, clean, want := tornLog(t)
	_, db := writeDB(t, data)
	dry := faultfs.New(fileio.OS{})
	st, err := Open(dry, db, fastOpts)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	n := dry.OpCount()
	for i := 0; i < n; i++ {
		for _, mode := range []faultfs.Mode{faultfs.FailBefore, faultfs.FailAfter} {
			_, db := writeDB(t, data)
			ffs := faultfs.New(fileio.OS{})
			ffs.Add(faultfs.Rule{Skip: i, Count: 1, Mode: mode, Err: errX})
			st, err := Open(ffs, db, fastOpts)
			if err == nil {
				st.Close()
			} else if !errors.Is(err, errX) {
				t.Fatalf("op %d mode %d: err = %v\n%s", i, mode, err, ffs.TraceString())
			}
			if ffs.OpenHandles() != 0 {
				t.Fatalf("op %d mode %d: %d handles leaked", i, mode, ffs.OpenHandles())
			}
			if b := readFile(t, db); !bytes.Equal(b, data) && !bytes.Equal(b, clean) {
				t.Fatalf("op %d mode %d: file is neither original nor truncated", i, mode)
			}
			got, _ := openState(t, fileio.OS{}, db)
			if !reflect.DeepEqual(got, want) || !bytes.Equal(readFile(t, db), clean) {
				t.Fatalf("op %d mode %d: re-run reached %v", i, mode, got)
			}
		}
	}
}

// T-REC-12 and kill during recovery: the process is killed before and after
// every operation of Open, including the truncate and the sync after it.
// Re-running Open always reaches the same index and file bytes.
func TestOpenCrashSweep(t *testing.T) {
	data, clean, want := tornLog(t)
	_, db := writeDB(t, data)
	dry := faultfs.New(fileio.OS{})
	st, err := Open(dry, db, fastOpts)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	n := dry.OpCount()
	sawTruncate := false
	for i := 0; i < n; i++ {
		for _, mode := range []faultfs.Mode{faultfs.Crash, faultfs.CrashAfter} {
			_, db := writeDB(t, data)
			ffs := faultfs.New(fileio.OS{})
			ffs.Add(faultfs.Rule{Skip: i, Count: 1, Mode: mode})
			if !faultfs.RunUntilCrash(func() {
				if st, err := Open(ffs, db, fastOpts); err == nil {
					st.Close()
				}
			}) {
				t.Fatalf("op %d: no crash", i)
			}
			ev := ffs.Events()
			if ev[len(ev)-1].Op == faultfs.OpTruncate {
				sawTruncate = true
			}
			got, _ := openState(t, fileio.OS{}, db)
			if !reflect.DeepEqual(got, want) || !bytes.Equal(readFile(t, db), clean) {
				t.Fatalf("op %d mode %d: after kill, re-run reached %v", i, mode, got)
			}
			got2, st2 := openState(t, fileio.OS{}, db)
			if !reflect.DeepEqual(got2, want) || st2.Truncated {
				t.Fatalf("op %d mode %d: third Open differs", i, mode)
			}
		}
	}
	if !sawTruncate {
		t.Fatal("sweep never killed at the truncate")
	}
}
