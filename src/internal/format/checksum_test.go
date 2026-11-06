package format

import "testing"

// Known-answer vectors for CRC32C (Castagnoli). "123456789" is the standard
// check value; the 32-byte vectors are from RFC 3720 (iSCSI) Appendix B.4.
func TestChecksumKnownVectors(t *testing.T) {
	zeros := make([]byte, 32)
	ones := make([]byte, 32)
	inc := make([]byte, 32)
	dec := make([]byte, 32)
	for i := range 32 {
		ones[i] = 0xFF
		inc[i] = byte(i)
		dec[i] = byte(31 - i)
	}
	tests := []struct {
		name string
		in   []byte
		want uint32
	}{
		{"empty", nil, 0x00000000},
		{"123456789", []byte("123456789"), 0xE3069283},
		{"32 zeros", zeros, 0x8A9136AA},
		{"32 0xFF", ones, 0x62A8AB43},
		{"incrementing", inc, 0x46DD794E},
		{"decrementing", dec, 0x113FDB5C},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Checksum(tt.in); got != tt.want {
				t.Fatalf("Checksum = %#08x, want %#08x", got, tt.want)
			}
		})
	}
}

// Streaming in arbitrary pieces must equal the one-shot checksum.
func TestUpdateChecksumStreaming(t *testing.T) {
	data := []byte("the quick brown fox jumps over the lazy dog, 0123456789")
	want := Checksum(data)
	for split := 0; split <= len(data); split++ {
		got := UpdateChecksum(UpdateChecksum(0, data[:split]), data[split:])
		if got != want {
			t.Fatalf("split %d: %#08x != %#08x", split, got, want)
		}
	}
}
