package recovery

import (
	"encoding/binary"
	"io"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
)

// LaterCommittedFrame looks for a complete, fully valid frame after a
// damaged frame header at pos: header CRC valid, sequence >= expected, the
// payload inside the file and its payload CRC valid.
//
// It never applies anything it finds. It exists only to tell a torn final
// write (C2: nothing can follow it, because the engine writes frames strictly
// one after another and never starts a frame before the previous one is
// synced) from damage in the middle of the log. In the second case
// truncating at pos would silently drop committed frames, so the caller
// refuses instead (rule C3, as amended in DESIGN §28 / RECOVERY.md).
//
// A read error ends the search without a result. The caller then keeps the
// C2 decision, which is exactly the pre-amendment behaviour.
func LaterCommittedFrame(r io.ReaderAt, pos, size, expected uint64) (at, seq uint64, found bool) {
	if expected == 0 {
		return 0, 0, false
	}
	var magic [4]byte
	binary.LittleEndian.PutUint32(magic[:], format.FrameMagic)
	const chunk = 1 << 20
	buf := make([]byte, chunk+3) // +3: a magic split across chunks is still seen
	for start := pos + 1; start < size; start += chunk {
		n, err := r.ReadAt(buf, int64(start))
		if n <= 0 {
			if err != nil {
				return 0, 0, false
			}
			continue
		}
		b := buf[:n]
		for i := 0; i+4 <= len(b); i++ {
			if b[i] != magic[0] || b[i+1] != magic[1] || b[i+2] != magic[2] || b[i+3] != magic[3] {
				continue
			}
			cand := start + uint64(i)
			if !format.HeaderFits(cand, size) {
				return 0, 0, false
			}
			var hb [format.FrameHeaderSize]byte
			if _, err := r.ReadAt(hb[:], int64(cand)); err != nil {
				return 0, 0, false
			}
			h, err := format.DecodeFrameHeader(hb[:])
			if err != nil || h.Seq < expected || !format.PayloadFits(cand, size, h.PayloadLen) {
				continue
			}
			pr := io.NewSectionReader(r, int64(cand)+format.FrameHeaderSize, int64(h.PayloadLen))
			if format.ScanPayload(pr, h, func(format.EntryView) error { return nil }) != nil {
				continue
			}
			return cand, h.Seq, true
		}
	}
	return 0, 0, false
}
