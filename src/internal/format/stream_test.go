package format

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"testing"
)

// encodeFrame writes one frame to a buffer.
func encodeFrame(t testing.TB, seq uint64, entries []Entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	n, err := WriteFrame(&buf, seq, entries, MaxFrameOps)
	if err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	if n != int64(buf.Len()) {
		t.Fatalf("WriteFrame returned %d, buffer has %d", n, buf.Len())
	}
	return buf.Bytes()
}

type scanned struct {
	op         OpCode
	key, value string
	off        uint64
}

// decodeFrame decodes a frame from b and returns its entries.
func decodeFrame(b []byte) (FrameHeader, []scanned, error) {
	h, err := DecodeFrameHeader(b)
	if err != nil {
		return FrameHeader{}, nil, err
	}
	var out []scanned
	err = ScanPayload(bytes.NewReader(b[FrameHeaderSize:]), h, func(e EntryView) error {
		out = append(out, scanned{e.Header.Op, string(e.Key), string(e.Value), e.Offset})
		return nil
	})
	return h, out, err
}

func TestWriteFrameRoundTrip(t *testing.T) {
	big := bytes.Repeat([]byte{0xC3}, MaxValueLen) // exercises the direct-write path
	mid := bytes.Repeat([]byte{0x3C}, directWriteThreshold-1)
	entries := []Entry{
		{OpPut, []byte("x"), []byte("1")},
		{OpPut, []byte("x"), []byte("2")},
		{OpDelete, []byte("y"), nil},
		{OpPut, []byte("big"), big},
		{OpPut, []byte("empty"), nil},
		{OpPut, []byte("mid"), mid},
		{OpPut, bytes.Repeat([]byte{'K'}, MaxKeyLen), []byte("v")},
	}
	b := encodeFrame(t, 42, entries)

	h, got, err := decodeFrame(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if h.Seq != 42 || int(h.OpCount) != len(entries) {
		t.Fatalf("header %+v", h)
	}
	if len(got) != len(entries) {
		t.Fatalf("got %d entries", len(got))
	}
	var off uint64
	for i, e := range entries {
		g := got[i]
		if g.op != e.Op || g.key != string(e.Key) || g.value != string(e.Value) || g.off != off {
			t.Fatalf("entry %d mismatch (off %d want %d)", i, g.off, off)
		}
		// The entry at Offset must decode standalone (this is the GET path).
		start := FrameHeaderSize + off
		size := EntryHeaderSize + uint64(len(e.Key)+len(e.Value))
		if _, v, err := DecodeEntryForKey(b[start:start+size], e.Key); err != nil || !bytes.Equal(v, e.Value) {
			t.Fatalf("entry %d standalone decode: %v", i, err)
		}
		off += size
	}
	if off != h.PayloadLen {
		t.Fatalf("payload len %d, sum %d", h.PayloadLen, off)
	}
}

func TestWriteFrameMatchesAppendEntry(t *testing.T) {
	entries := []Entry{{OpPut, []byte("a"), []byte("b")}, {OpDelete, []byte("c"), nil}}
	b := encodeFrame(t, 7, entries)
	var payload []byte
	for _, e := range entries {
		var err error
		payload, err = AppendEntry(payload, e.Op, e.Key, e.Value)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(b[FrameHeaderSize:], payload) {
		t.Fatal("WriteFrame payload differs from AppendEntry concatenation")
	}
	h, _ := DecodeFrameHeader(b)
	if h.PayloadCRC != Checksum(payload) {
		t.Fatal("payload crc mismatch")
	}
}

func TestWriteFrameValidation(t *testing.T) {
	tooMany := make([]Entry, MaxBatchOps+1)
	for i := range tooMany {
		tooMany[i] = Entry{OpDelete, []byte("k"), nil}
	}
	tests := []struct {
		name    string
		entries []Entry
		maxOps  int
	}{
		{"empty", nil, MaxBatchOps},
		{"bad op", []Entry{{OpCode(9), []byte("k"), nil}}, MaxBatchOps},
		{"empty key", []Entry{{OpPut, nil, []byte("v")}}, MaxBatchOps},
		{"key too long", []Entry{{OpPut, make([]byte, MaxKeyLen+1), nil}}, MaxBatchOps},
		{"value too long", []Entry{{OpPut, []byte("k"), make([]byte, MaxValueLen+1)}}, MaxBatchOps},
		{"delete with value", []Entry{{OpDelete, []byte("k"), []byte("v")}}, MaxBatchOps},
		{"batch over 10000", tooMany, MaxBatchOps},
		{"maxOps 0", []Entry{{OpPut, []byte("k"), nil}}, 0},
		{"maxOps too big", []Entry{{OpPut, []byte("k"), nil}}, MaxFrameOps + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			n, err := WriteFrame(&buf, 1, tt.entries, tt.maxOps)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if n != 0 || buf.Len() != 0 {
				t.Fatalf("wrote %d bytes on validation failure", buf.Len())
			}
		})
	}
	// 10,001 entries are fine for a compaction frame.
	if _, err := WriteFrame(io.Discard, 1, tooMany, MaxFrameOps); err != nil {
		t.Fatalf("compaction frame of %d ops: %v", len(tooMany), err)
	}
	// seq 0 is rejected by the header encoder, before any byte is written.
	var buf bytes.Buffer
	if _, err := WriteFrame(&buf, 0, []Entry{{OpPut, []byte("k"), nil}}, 1); !errors.Is(err, ErrInvalid) || buf.Len() != 0 {
		t.Fatalf("seq 0: err %v, wrote %d", err, buf.Len())
	}
}

// No 64 MiB-style cap: the size computation accepts a maximal user batch.
func TestPayloadSizeMaxBatchNoArbitraryCap(t *testing.T) {
	val := make([]byte, MaxValueLen) // shared backing array, no 10 GB allocation
	key := make([]byte, MaxKeyLen)
	entries := make([]Entry, MaxBatchOps)
	for i := range entries {
		entries[i] = Entry{OpPut, key, val}
	}
	got, err := PayloadSize(entries, MaxBatchOps)
	if err != nil {
		t.Fatal(err)
	}
	if want := uint64(MaxBatchOps) * MaxEntrySize; got != want {
		t.Fatalf("PayloadSize = %d, want %d", got, want)
	}
}

type failWriter struct {
	limit int // bytes accepted before failing
	n     int
}

var errDiskFull = errors.New("simulated disk full")

func (f *failWriter) Write(p []byte) (int, error) {
	if f.n+len(p) <= f.limit {
		f.n += len(p)
		return len(p), nil
	}
	k := f.limit - f.n
	f.n = f.limit
	return k, errDiskFull
}

func TestWriteFrameReportsWriterFailure(t *testing.T) {
	entries := []Entry{{OpPut, []byte("key"), bytes.Repeat([]byte{1}, 500)}}
	for _, limit := range []int{0, 1, 31, 32, 40, 500} {
		fw := &failWriter{limit: limit}
		n, err := WriteFrame(fw, 1, entries, 1)
		if !errors.Is(err, errDiskFull) {
			t.Fatalf("limit %d: err = %v", limit, err)
		}
		if n != int64(limit) {
			t.Fatalf("limit %d: n = %d (caller needs the exact count to truncate)", limit, n)
		}
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil } // contract violation

func TestWriteFrameDetectsShortWrite(t *testing.T) {
	_, err := WriteFrame(shortWriter{}, 1, []Entry{{OpPut, []byte("k"), []byte("v")}}, 1)
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("err = %v", err)
	}
}

// Every strict prefix of a frame (what a kill mid-write leaves) must be
// rejected and classified Damaged, never Corrupt, never accepted.
func TestTornFramePrefixesAreDamaged(t *testing.T) {
	entries := []Entry{
		{OpPut, []byte("alpha"), []byte("one")},
		{OpDelete, []byte("beta"), nil},
		{OpPut, []byte("gamma"), bytes.Repeat([]byte{7}, 300)},
	}
	b := encodeFrame(t, 3, entries)
	for cut := 0; cut < len(b); cut++ {
		prefix := b[:cut]
		var err error
		if cut < FrameHeaderSize {
			_, err = DecodeFrameHeader(prefix)
		} else {
			var h FrameHeader
			h, err = DecodeFrameHeader(prefix)
			if err == nil {
				err = ScanPayload(bytes.NewReader(prefix[FrameHeaderSize:]), h, nil)
			}
		}
		if err == nil {
			t.Fatalf("prefix of %d/%d bytes accepted", cut, len(b))
		}
		if c := Classify(err); c != ClassDamaged {
			t.Fatalf("prefix %d: class %v (%v)", cut, c, err)
		}
	}
}

// A torn tail followed by zeros (preallocated / sparse region) is Damaged.
func TestTornFrameWithZeroFill(t *testing.T) {
	b := encodeFrame(t, 1, []Entry{{OpPut, []byte("k"), bytes.Repeat([]byte{9}, 100)}})
	torn := append([]byte(nil), b...)
	for i := 60; i < len(torn); i++ {
		torn[i] = 0
	}
	h, err := DecodeFrameHeader(torn)
	if err != nil {
		t.Fatal(err)
	}
	err = ScanPayload(bytes.NewReader(torn[FrameHeaderSize:]), h, nil)
	if !errors.Is(err, ErrPayloadChecksum) || Classify(err) != ClassDamaged {
		t.Fatalf("err = %v", err)
	}
}

// buildFrame assembles a frame from a raw payload with a correct payload CRC
// and header CRC, so a test can reach the structural checks.
func buildFrame(t *testing.T, opCount uint16, payload []byte) []byte {
	t.Helper()
	h := make([]byte, FrameHeaderSize)
	binary.LittleEndian.PutUint32(h[fhOffMagic:], FrameMagic)
	h[fhOffType] = FrameTypeCommit
	binary.LittleEndian.PutUint16(h[fhOffOpCount:], opCount)
	binary.LittleEndian.PutUint64(h[fhOffSeq:], 1)
	binary.LittleEndian.PutUint64(h[fhOffPayloadLen:], uint64(len(payload)))
	binary.LittleEndian.PutUint32(h[fhOffPayloadCRC:], Checksum(payload))
	resealFH(h)
	return append(h, payload...)
}

func entryBytes(t *testing.T, op OpCode, k, v string) []byte {
	t.Helper()
	b, err := AppendEntry(nil, op, []byte(k), []byte(v))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// CRC-valid but structurally wrong frames are Corrupt, never Damaged: a torn
// write cannot produce them, so recovery must not truncate them away.
func TestMalformedPayloadIsCorrupt(t *testing.T) {
	e1 := entryBytes(t, OpPut, "a", "1")
	e2 := entryBytes(t, OpDelete, "b", "")
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

	badEntryCRC := append([]byte(nil), e1...)
	badEntryCRC[len(badEntryCRC)-1] ^= 0xFF // value changed, entry crc stale

	delWithVal := append(rawEntryHeader(2, 1, 1), 'k', 'v')
	keyTooLong := rawEntryHeader(1, 2000, 0)
	keyTooLong = append(keyTooLong, make([]byte, 2000)...)
	overrun := rawEntryHeader(1, 1, 100) // claims 100-byte value, payload ends sooner
	overrun = append(overrun, 'k', 'v')

	tests := []struct {
		name    string
		opCount uint16
		payload []byte
		extra   error
	}{
		{"op_count too high", 3, cat(e1, e2, make([]byte, 12)), nil},
		{"op_count too low", 1, cat(e1, e2), nil},
		{"trailing short garbage", 2, cat(e1, e2, []byte{1, 2, 3}), nil},
		{"entry crc mismatch", 1, badEntryCRC, ErrEntryChecksum},
		{"delete with value", 1, delWithVal, nil},
		{"key too long", 1, keyTooLong, nil},
		{"entry overruns payload", 1, overrun, nil},
		{"op 0 entry", 1, append(rawEntryHeader(0, 1, 0), 'k'), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := buildFrame(t, tt.opCount, tt.payload)
			_, _, err := decodeFrame(b)
			if !errors.Is(err, ErrMalformed) || Classify(err) != ClassCorrupt {
				t.Fatalf("err = %v (class %v), want malformed/corrupt", err, Classify(err))
			}
			if tt.extra != nil && !errors.Is(err, tt.extra) {
				t.Fatalf("err = %v, want also %v", err, tt.extra)
			}
		})
	}
}

// The same structural damage with a WRONG payload CRC is Damaged: the scanner
// drains the payload and lets the checksum decide.
func TestMalformedPayloadWithBadCRCIsDamaged(t *testing.T) {
	payload := append(rawEntryHeader(2, 1, 1), 'k', 'v') // DEL with a value
	b := buildFrame(t, 1, payload)
	binary.LittleEndian.PutUint32(b[fhOffPayloadCRC:], Checksum(payload)^1)
	resealFH(b)
	_, _, err := decodeFrame(b)
	if !errors.Is(err, ErrPayloadChecksum) || Classify(err) != ClassDamaged {
		t.Fatalf("err = %v", err)
	}
}

type errReader struct {
	data []byte
	err  error
}

func (r *errReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// A real read error must surface as an I/O error, never as truncation/EOF.
func TestScanPayloadPropagatesReadErrors(t *testing.T) {
	b := encodeFrame(t, 1, []Entry{{OpPut, []byte("k"), bytes.Repeat([]byte{1}, 1000)}})
	h, _ := DecodeFrameHeader(b)
	errIO := errors.New("simulated read error (sector unreadable)")
	payload := b[FrameHeaderSize:]
	for _, cut := range []int{0, 5, 11, 12, 500, len(payload) - 1} {
		err := ScanPayload(&errReader{data: payload[:cut], err: errIO}, h, nil)
		if !errors.Is(err, errIO) {
			t.Fatalf("cut %d: err = %v", cut, err)
		}
		if c := Classify(err); c != ClassIO {
			t.Fatalf("cut %d: class %v", cut, c)
		}
		if errors.Is(err, ErrTruncated) {
			t.Fatalf("cut %d: read error reported as truncation", cut)
		}
	}
	// Read error while draining after a structural problem is still IO.
	bad := buildFrame(t, 1, append(rawEntryHeader(2, 1, 1), 'k', 'v'))
	hb, _ := DecodeFrameHeader(bad)
	err := ScanPayload(&errReader{data: bad[FrameHeaderSize : FrameHeaderSize+11], err: errIO}, hb, nil)
	if !errors.Is(err, errIO) || Classify(err) != ClassIO {
		t.Fatalf("drain read error: %v", err)
	}
}

func TestScanPayloadCallbackError(t *testing.T) {
	b := encodeFrame(t, 1, []Entry{{OpPut, []byte("a"), nil}, {OpPut, []byte("b"), nil}})
	h, _ := DecodeFrameHeader(b)
	stop := errors.New("stop")
	calls := 0
	err := ScanPayload(bytes.NewReader(b[FrameHeaderSize:]), h, func(EntryView) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("err = %v, calls = %d", err, calls)
	}
}

// Entries are delivered before the payload CRC is known. This test pins down
// the contract the recovery code relies on: when the payload CRC fails,
// ScanPayload returns an error even though the callback already saw entries.
func TestScanPayloadEntriesAreTentative(t *testing.T) {
	b := encodeFrame(t, 1, []Entry{{OpPut, []byte("a"), []byte("1")}, {OpPut, []byte("b"), []byte("2")}})
	b[len(b)-1] ^= 0xFF // damage the last entry's value
	h, _ := DecodeFrameHeader(b)
	var seen int
	err := ScanPayload(bytes.NewReader(b[FrameHeaderSize:]), h, func(EntryView) error { seen++; return nil })
	if !errors.Is(err, ErrPayloadChecksum) {
		t.Fatalf("err = %v", err)
	}
	if seen != 1 {
		t.Fatalf("callback saw %d entries before the failure", seen)
	}
}

func TestScanPayloadRejectsHostileHeader(t *testing.T) {
	err := ScanPayload(bytes.NewReader(nil), FrameHeader{OpCount: 1, Seq: 1, PayloadLen: MaxPayload + 1}, nil)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

// Records every Write call: proves bytes arrive strictly in file order, that
// staged writes never exceed the staging buffer, and that large values are
// written directly from the caller's slice (no copy).
func TestWriteFrameWriteCalls(t *testing.T) {
	bigVal := bytes.Repeat([]byte{1}, directWriteThreshold)
	entries := []Entry{{OpPut, []byte("k1"), bigVal}, {OpPut, []byte("k2"), []byte("v")}}
	want := encodeFrame(t, 9, entries)
	var got bytes.Buffer
	var calls [][]byte
	if _, err := WriteFrame(writerFunc(func(p []byte) (int, error) {
		calls = append(calls, p)
		got.Write(p)
		return len(p), nil
	}), 9, entries, MaxFrameOps); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatal("concatenated writes differ from frame")
	}
	direct := false
	for _, c := range calls {
		if len(c) > stagingSize {
			t.Fatalf("write of %d bytes exceeds staging size", len(c))
		}
		if len(c) == len(bigVal) && &c[0] == &bigVal[0] {
			direct = true
		}
	}
	if !direct {
		t.Fatal("large value was not written directly from the caller slice")
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func ExampleWriteFrame() {
	var buf bytes.Buffer
	n, err := WriteFrame(&buf, 1, []Entry{{OpPut, []byte("k"), []byte("v")}}, MaxBatchOps)
	fmt.Println(n, err)
	// Output: 45 <nil>
}
