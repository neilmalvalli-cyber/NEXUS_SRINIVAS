package format

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestClassifyTable(t *testing.T) {
	ioErr := errors.New("disk read failed")
	tests := []struct {
		err  error
		want Class
	}{
		{nil, ClassNone},
		{ErrBadMagic, ClassDamaged},
		{ErrHeaderChecksum, ClassDamaged},
		{ErrPayloadChecksum, ClassDamaged},
		{ErrTruncated, ClassDamaged},
		{ErrMalformed, ClassCorrupt},
		{ErrUnsupportedVersion, ClassCorrupt},
		{ErrEntryChecksum, ClassCorrupt},
		{fmt.Errorf("%w: %w", ErrMalformed, ErrEntryChecksum), ClassCorrupt},
		{malformed("entry", "op", 9, "x"), ClassCorrupt},
		{ErrInvalid, ClassUsage},
		{invalid("entry", "op", 9, "x"), ClassUsage},
		{ErrShortBuffer, ClassUsage},
		{ioErr, ClassIO},
		{fmt.Errorf("format: reading payload: %w", ioErr), ClassIO},
		{io.EOF, ClassIO}, // a bare EOF never leaks out of this package; if it did, it must not look like damage
	}
	for _, tt := range tests {
		if got := Classify(tt.err); got != tt.want {
			t.Errorf("Classify(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
	for c := ClassNone; c <= ClassUsage; c++ {
		if s := c.String(); s == "" || s[0] == 'C' {
			t.Errorf("Class(%d).String() = %q", int(c), s)
		}
	}
}

// Every error the GET-path decoder can return has a file-related class
// (Damaged or Corrupt), never IO or Usage.
func TestDecodeEntryErrorClasses(t *testing.T) {
	good, err := AppendEntry(nil, OpPut, []byte("key"), []byte("value"))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"short header": good[:5],
		"short body":   good[:len(good)-1],
		"long body":    append(append([]byte(nil), good...), 0),
		"bad op":       append([]byte{9}, good[1:]...),
	}
	flipped := append([]byte(nil), good...)
	flipped[len(flipped)-1] ^= 1
	cases["value flip"] = flipped
	for name, in := range cases {
		_, _, _, err := DecodeEntry(in)
		if c := Classify(err); c != ClassDamaged && c != ClassCorrupt {
			t.Errorf("%s: class %v (%v)", name, c, err)
		}
	}
	if _, _, err := DecodeEntryForKey(good, []byte("other")); Classify(err) != ClassCorrupt {
		t.Errorf("key mismatch: class %v", Classify(err))
	}
}

// A torn frame whose value happens to contain a perfectly valid frame header
// (with the next expected sequence number) is still just a torn frame: the
// decoder never looks inside the payload for headers. This pins down
// DESIGN §8.2 rule C3 (no forward scan) at the format level.
func TestTornFrameContainingEmbeddedFrameHeader(t *testing.T) {
	fake, err := FrameHeader{OpCount: 1, Seq: 2, PayloadLen: 12, PayloadCRC: 0}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	value := bytes.Repeat(fake[:], 8) // valid-looking headers throughout the value
	b := encodeFrame(t, 1, []Entry{{OpPut, []byte("k"), value}})
	for _, cut := range []int{FrameHeaderSize + 20, FrameHeaderSize + 60, len(b) - 1} {
		torn := b[:cut]
		h, err := DecodeFrameHeader(torn)
		if err != nil {
			t.Fatalf("cut %d: header: %v", cut, err)
		}
		if PayloadFits(0, uint64(len(torn)), h.PayloadLen) {
			t.Fatalf("cut %d: torn payload reported as fitting", cut)
		}
		err = ScanPayload(bytes.NewReader(torn[FrameHeaderSize:]), h, nil)
		if Classify(err) != ClassDamaged {
			t.Fatalf("cut %d: class %v (%v)", cut, Classify(err), err)
		}
	}
}
