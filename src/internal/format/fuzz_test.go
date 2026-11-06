package format

import (
	"bytes"
	"errors"
	"testing"
)

// The fuzz targets assert properties, not just "does not panic":
//   - a decoder never panics and never allocates from an untrusted length
//     before validating it;
//   - anything a decoder accepts re-encodes to exactly the same bytes
//     (no two encodings of one value, no ignored bits);
//   - every error has a defined class.
//
// `go test ./...` runs only the seed corpus. Run a fuzzer for real with e.g.
//   go test ./src/internal/format -run=^$ -fuzz=FuzzDecodeFrameHeader -fuzztime=30s

func FuzzDecodeSuperblock(f *testing.F) {
	for _, s := range []Superblock{NewSuperblock(), {FirstSeq: 9, SnapshotEnd: 1 << 20}} {
		b, _ := s.Encode()
		f.Add(b[:])
	}
	f.Add(make([]byte, 32))
	f.Add([]byte("KIPDBLOG"))
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := DecodeSuperblock(data)
		if err != nil {
			if Classify(err) == ClassIO || Classify(err) == ClassNone {
				t.Fatalf("unclassified decode error: %v", err)
			}
			return
		}
		b, err := s.Encode()
		if err != nil {
			t.Fatalf("decoded value does not re-encode: %+v: %v", s, err)
		}
		if !bytes.Equal(b[:], data[:SuperblockSize]) {
			t.Fatalf("re-encode differs:\n% x\n% x", b, data[:SuperblockSize])
		}
	})
}

func FuzzDecodeFrameHeader(f *testing.F) {
	for _, h := range []FrameHeader{
		{OpCount: 1, Seq: 1, PayloadLen: 12},
		{OpCount: MaxFrameOps, Seq: ^uint64(0), PayloadLen: MaxPayload, PayloadCRC: 7},
	} {
		b, _ := h.Encode()
		f.Add(b[:])
	}
	f.Add(make([]byte, 32))
	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := DecodeFrameHeader(data)
		if err != nil {
			if Classify(err) == ClassIO || Classify(err) == ClassNone {
				t.Fatalf("unclassified decode error: %v", err)
			}
			return
		}
		if h.PayloadLen > MaxPayload || h.PayloadLen < MinEntrySize || h.OpCount == 0 || h.Seq == 0 ||
			uint64(h.OpCount)*MinEntrySize > h.PayloadLen {
			t.Fatalf("decoder accepted invalid header %+v", h)
		}
		b, err := h.Encode()
		if err != nil {
			t.Fatalf("decoded header does not re-encode: %+v: %v", h, err)
		}
		if !bytes.Equal(b[:], data[:FrameHeaderSize]) {
			t.Fatalf("re-encode differs")
		}
	})
}

func FuzzDecodeEntry(f *testing.F) {
	for _, e := range []Entry{{OpPut, []byte("k"), []byte("v")}, {OpDelete, []byte("key"), nil}, {OpPut, []byte("e"), nil}} {
		b, _ := AppendEntry(nil, e.Op, e.Key, e.Value)
		f.Add(b)
	}
	f.Add(rawEntryHeader(2, 1, 1))
	f.Fuzz(func(t *testing.T, data []byte) {
		h, key, value, err := DecodeEntry(data)
		if err != nil {
			if c := Classify(err); c != ClassDamaged && c != ClassCorrupt {
				t.Fatalf("decode error has class %v: %v", c, err)
			}
			return
		}
		if h.Op == OpDelete && len(value) != 0 {
			t.Fatal("accepted DEL with value")
		}
		b, err := AppendEntry(nil, h.Op, key, value)
		if err != nil {
			t.Fatalf("accepted entry does not re-encode: %v", err)
		}
		if !bytes.Equal(b, data) {
			t.Fatal("re-encode differs")
		}
	})
}

// FuzzScanFrame decodes arbitrary bytes as a frame. It checks that the
// scanner never panics, that every error is classified, and that a frame it
// accepts re-encodes byte-for-byte from the entries it reported.
func FuzzScanFrame(f *testing.F) {
	enc := func(entries []Entry) []byte {
		var buf bytes.Buffer
		if _, err := WriteFrame(&buf, 1, entries, MaxFrameOps); err != nil {
			f.Fatal(err)
		}
		return buf.Bytes()
	}
	f.Add(enc([]Entry{{OpPut, []byte("k"), []byte("v")}}))
	f.Add(enc([]Entry{{OpPut, []byte("x"), []byte("1")}, {OpDelete, []byte("x"), nil}, {OpPut, []byte("y"), bytes.Repeat([]byte{3}, 40)}}))
	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := DecodeFrameHeader(data)
		if err != nil {
			return
		}
		// Mirror recovery: bound payload_len by the bytes actually present.
		if !PayloadFits(0, uint64(len(data)), h.PayloadLen) {
			return
		}
		var got []Entry
		err = ScanPayload(bytes.NewReader(data[FrameHeaderSize:FrameHeaderSize+h.PayloadLen]), h, func(e EntryView) error {
			got = append(got, Entry{e.Header.Op, append([]byte(nil), e.Key...), append([]byte(nil), e.Value...)})
			return nil
		})
		if err != nil {
			switch Classify(err) {
			case ClassDamaged, ClassCorrupt:
			default:
				t.Fatalf("unclassified scan error: %v", err)
			}
			if errors.Is(err, ErrTruncated) {
				t.Fatalf("payload fully present but reported truncated: %v", err)
			}
			return
		}
		var buf bytes.Buffer
		if _, err := WriteFrame(&buf, h.Seq, got, MaxFrameOps); err != nil {
			t.Fatalf("accepted frame does not re-encode: %v", err)
		}
		if !bytes.Equal(buf.Bytes(), data[:FrameHeaderSize+h.PayloadLen]) {
			t.Fatal("re-encode differs")
		}
	})
}
