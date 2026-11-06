package xray

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/recovery"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/kvdb"
)

// LabCase is one disposable database with a known, deliberately injected
// fault. The lab exists to demonstrate and test X-RAY; it only ever writes
// inside the directory it is given, and every case is built from a fresh
// database created by the real kvdb API, then damaged with exact byte
// edits.
type LabCase struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"` // what was done to the bytes
	Path        string `json:"path"`
	// Expected diagnosis, checked by the tests.
	ExpectCode     string         `json:"expect_code"`
	ExpectSeverity Severity       `json:"expect_severity"`
	ExpectRule     string         `json:"expect_rule,omitempty"`
	ExpectRecovery RecoveryStatus `json:"expect_recovery"`
	ExpectOffset   *int64         `json:"expect_offset,omitempty"`
	// HoldLock: the scan must run while the database is held open (the
	// caller opens it; see OpenHolder).
	HoldLock bool `json:"hold_lock,omitempty"`
}

// LabCaseNames lists the cases BuildLab creates, in order.
var LabCaseNames = []string{
	"clean", "bad-magic", "unsupported-version", "superblock-checksum",
	"torn-tail", "incomplete-header", "final-payload-checksum",
	"committed-frame-corrupted", "header-damaged-mid-log", "invalid-seq",
	"invalid-op", "entry-checksum", "snapshot-corrupted",
	"interrupted-compaction", "compaction-leftover", "lock-conflict",
}

type frameLoc struct {
	off     int64
	payload int64 // payload length
	ops     int
}

// seedDB creates a database with five single-PUT frames, one 3-op batch
// frame and two more PUT frames, using the real API, and returns its frames.
func seedDB(path string) ([]frameLoc, error) {
	db, err := kvdb.Open(path, 0)
	if err != nil {
		return nil, err
	}
	for i := 0; i < 5; i++ {
		if err := db.Put([]byte(fmt.Sprintf("user:%d", i)), []byte(fmt.Sprintf("value-%d-%s", i, "abcdefghijklmnop"))); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := db.Batch([]kvdb.Op{{Kind: kvdb.OpPut, Key: []byte("acct:a"), Value: []byte("100")},
		{Kind: kvdb.OpPut, Key: []byte("acct:b"), Value: []byte("200")},
		{Kind: kvdb.OpDelete, Key: []byte("user:0")}}); err != nil {
		db.Close()
		return nil, err
	}
	for i := 5; i < 7; i++ {
		if err := db.Put([]byte(fmt.Sprintf("user:%d", i)), []byte(fmt.Sprintf("value-%d", i))); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := db.Close(); err != nil {
		return nil, err
	}
	return frames(path)
}

func frames(path string) ([]frameLoc, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []frameLoc
	pos := int64(format.SuperblockSize)
	for pos < int64(len(b)) {
		h, err := format.DecodeFrameHeader(b[pos:])
		if err != nil {
			return nil, err
		}
		out = append(out, frameLoc{off: pos, payload: int64(h.PayloadLen), ops: int(h.OpCount)})
		pos += format.FrameHeaderSize + int64(h.PayloadLen)
	}
	return out, nil
}

// edit applies fn to the file's bytes and writes them back.
func edit(path string, fn func(b []byte) []byte) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, fn(b), 0o644)
}

func resealHeader(b []byte, off int64) {
	h := b[off : off+format.FrameHeaderSize]
	binary.LittleEndian.PutUint32(h[fhOffHdrCRC:], format.Checksum(h[:fhOffHdrCRC]))
}

func resealPayload(b []byte, fr frameLoc) {
	p := b[fr.off+format.FrameHeaderSize : fr.off+format.FrameHeaderSize+fr.payload]
	binary.LittleEndian.PutUint32(b[fr.off+fhOffPayCRC:], format.Checksum(p))
	resealHeader(b, fr.off)
}

// BuildLab creates every lab case under dir (which must be empty or not
// exist) and returns them. It refuses a non-empty directory so that it can
// never overwrite anything.
func BuildLab(dir string) ([]LabCase, error) {
	if ents, err := os.ReadDir(dir); err == nil && len(ents) > 0 {
		return nil, fmt.Errorf("xray lab: %s is not empty; refusing to write there", dir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var cases []LabCase
	for _, name := range LabCaseNames {
		c, err := buildCase(dir, name)
		if err != nil {
			return nil, fmt.Errorf("xray lab case %s: %w", name, err)
		}
		cases = append(cases, c)
	}
	return cases, nil
}

func buildCase(root, name string) (LabCase, error) {
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return LabCase{}, err
	}
	path := filepath.Join(dir, "lab.kv")
	fr, err := seedDB(path)
	if err != nil {
		return LabCase{}, err
	}
	c := LabCase{Name: name, Path: path}
	last := fr[len(fr)-1]
	mid := fr[2]
	batch := fr[5]
	switch name {
	case "clean":
		c.Title, c.Description = "Healthy database", "Eight frames (seven PUTs and one 3-operation batch) written through the kvdb API; no changes."
		c.ExpectCode, c.ExpectSeverity, c.ExpectRecovery = "SCAN_CLEAN", Info, NotApplicable
	case "bad-magic":
		err = edit(path, func(b []byte) []byte { b[0] ^= 0xFF; return b })
		c.Title, c.Description = "Bad superblock magic", "Byte 0 of the superblock (the signature) inverted."
		c.ExpectCode, c.ExpectSeverity, c.ExpectRecovery, c.ExpectOffset = "SB_BAD_MAGIC", Critical, Unrecoverable, i64(0)
	case "unsupported-version":
		err = edit(path, func(b []byte) []byte {
			binary.LittleEndian.PutUint16(b[sbOffVersion:], 2)
			binary.LittleEndian.PutUint32(b[sbOffCRC:], format.Checksum(b[:sbOffCRC]))
			return b
		})
		c.Title, c.Description = "Unsupported format version", "Superblock version set to 2 and its CRC recomputed, so only the version is wrong."
		c.ExpectCode, c.ExpectSeverity, c.ExpectRecovery, c.ExpectOffset = "SB_UNSUPPORTED_VERSION", Critical, Refused, i64(sbOffVersion)
	case "superblock-checksum":
		err = edit(path, func(b []byte) []byte { b[12] ^= 0x01; return b })
		c.Title, c.Description = "Superblock checksum mismatch", "One bit of superblock first_seq (offset 12) flipped; CRC left as stored."
		c.ExpectCode, c.ExpectSeverity, c.ExpectRecovery, c.ExpectOffset = "SB_CHECKSUM", Critical, Unrecoverable, i64(sbOffCRC)
	case "torn-tail":
		cut := last.off + format.FrameHeaderSize + last.payload/2
		err = edit(path, func(b []byte) []byte { return b[:cut] })
		c.Title, c.Description = "Torn final write", fmt.Sprintf("File cut in the middle of the last frame's payload (at byte %d), as an interrupted append leaves it.", cut)
		c.ExpectCode, c.ExpectSeverity, c.ExpectRule, c.ExpectRecovery, c.ExpectOffset = "FRAME_PAYLOAD_TRUNCATED", Warning, "C4", Recoverable, i64(last.off)
	case "incomplete-header":
		var size int64
		err = edit(path, func(b []byte) []byte {
			size = int64(len(b))
			return append(b, 0xA5, 0xB1, 0xC3, 0xD8, 1, 0, 1, 0, 9, 0) // 10 bytes of a new header
		})
		c.Title, c.Description = "Incomplete final frame header", "Ten bytes of a new frame header appended after the last frame."
		c.ExpectCode, c.ExpectSeverity, c.ExpectRule, c.ExpectRecovery, c.ExpectOffset = "FRAME_TORN_HEADER", Warning, "C1", Recoverable, i64(size)
	case "final-payload-checksum":
		err = edit(path, func(b []byte) []byte { b[last.off+format.FrameHeaderSize+12] ^= 0x10; return b })
		c.Title, c.Description = "Final frame payload checksum mismatch", "One payload byte of the last frame flipped; the frame still ends exactly at end of file."
		c.ExpectCode, c.ExpectSeverity, c.ExpectRule, c.ExpectRecovery, c.ExpectOffset = "FRAME_PAYLOAD_CHECKSUM", Warning, "C5", Recoverable, i64(last.off)
	case "committed-frame-corrupted":
		err = edit(path, func(b []byte) []byte { b[mid.off+format.FrameHeaderSize+14] ^= 0x04; return b })
		c.Title, c.Description = "Corrupted committed frame", "One payload byte of frame 3 (of 8) flipped; five intact frames follow it."
		c.ExpectCode, c.ExpectSeverity, c.ExpectRule, c.ExpectRecovery, c.ExpectOffset = "FRAME_PAYLOAD_CHECKSUM", Critical, "C6", Refused, i64(mid.off)
	case "header-damaged-mid-log":
		err = edit(path, func(b []byte) []byte { b[mid.off+fhOffPayLen] ^= 0x01; return b })
		c.Title, c.Description = "Damaged header in the middle of the log", "One bit of frame 3's payload_len flipped (header CRC now fails). By rule C3 open would cut everything from frame 3 on; X-RAY finds the committed frames behind it."
		c.ExpectCode, c.ExpectSeverity, c.ExpectRule, c.ExpectRecovery, c.ExpectOffset = "FRAME_HEADER_DAMAGED", Critical, "C2", Detected, i64(mid.off)
	case "invalid-seq":
		err = edit(path, func(b []byte) []byte {
			binary.LittleEndian.PutUint64(b[mid.off+fhOffSeq:], 99)
			resealHeader(b, mid.off)
			return b
		})
		c.Title, c.Description = "Invalid sequence number", "Frame 3's seq set to 99 and its header CRC recomputed, so only the order is wrong."
		c.ExpectCode, c.ExpectSeverity, c.ExpectRule, c.ExpectRecovery, c.ExpectOffset = "SEQ_MISMATCH", Critical, "C7", Refused, i64(mid.off+fhOffSeq)
	case "invalid-op":
		err = edit(path, func(b []byte) []byte {
			b[batch.off+format.FrameHeaderSize] = 7 // op code of the batch's first entry
			resealPayload(b, batch)
			return b
		})
		c.Title, c.Description = "Invalid operation payload", "The first entry of the 3-op batch frame given op code 7 (only PUT=1 and DEL=2 exist); payload and header CRCs recomputed."
		c.ExpectCode, c.ExpectSeverity, c.ExpectRule, c.ExpectRecovery, c.ExpectOffset = "OP_MALFORMED", Critical, "C7", Refused, i64(batch.off+format.FrameHeaderSize)
	case "entry-checksum":
		var at int64
		err = edit(path, func(b []byte) []byte {
			e := batch.off + format.FrameHeaderSize
			kl := int64(binary.LittleEndian.Uint16(b[e+1:]))
			vl := int64(binary.LittleEndian.Uint32(b[e+3:]))
			second := e + format.EntryHeaderSize + kl + vl
			at = second
			b[second+format.EntryHeaderSize] ^= 0x20 // first key byte of the second entry
			resealPayload(b, batch)
			return b
		})
		c.Title, c.Description = "Operation checksum mismatch", "A key byte of the batch's second entry changed, then the frame's payload and header CRCs recomputed: only the entry CRC catches it."
		c.ExpectCode, c.ExpectSeverity, c.ExpectRule, c.ExpectRecovery = "OP_ENTRY_CHECKSUM", Critical, "C7", Refused
		c.ExpectOffset = i64(at)
	case "snapshot-corrupted":
		db, oerr := kvdb.Open(path, 0)
		if oerr != nil {
			return c, oerr
		}
		if err = db.Compact(); err != nil {
			db.Close()
			return c, err
		}
		if err = db.Close(); err != nil {
			return c, err
		}
		sf, ferr := frames(path)
		if ferr != nil {
			return c, ferr
		}
		err = edit(path, func(b []byte) []byte { b[sf[0].off+format.FrameHeaderSize+5] ^= 0x01; return b })
		c.Title, c.Description = "Damage inside the compacted snapshot", "Database compacted (all live keys copied into one sealed snapshot frame), then one byte of that frame flipped."
		c.ExpectRule, c.ExpectRecovery, c.ExpectSeverity, c.ExpectOffset = "C0", Refused, Critical, i64(sf[0].off)
		// The snapshot frame ends at EOF, so outside a snapshot this would be
		// a torn final frame (C5); inside the sealed snapshot it is C0.
		c.ExpectCode = "FRAME_PAYLOAD_CHECKSUM"
	case "interrupted-compaction":
		compact, old, _ := recovery.Names(path)
		db, oerr := kvdb.Open(path, 0)
		if oerr != nil {
			return c, oerr
		}
		if err = db.Compact(); err != nil {
			db.Close()
			return c, err
		}
		db.Close()
		// State after compaction R1 (path -> .old) and before R2 (.compact -> path):
		// reproduce it from real files: the compacted file as .compact, an
		// uncompacted copy as .old, and path missing.
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return c, rerr
		}
		if err = os.WriteFile(compact, b, 0o644); err != nil {
			return c, err
		}
		if err = os.Rename(path, old); err != nil {
			return c, err
		}
		c.Title, c.Description = "Compaction interrupted between its two renames", "The sealed compaction output is present as lab.kv.compact, the pre-compaction file as lab.kv.old, and lab.kv itself is missing: the state after rename R1 and before R2."
		c.ExpectCode, c.ExpectSeverity, c.ExpectRule, c.ExpectRecovery = "COMPACTION_INTERRUPTED_SWAP", Warning, "A3", Recoverable
	case "compaction-leftover":
		compact, _, _ := recovery.Names(path)
		err = os.WriteFile(compact, make([]byte, 4096), 0o644) // unsealed: zero superblock
		c.Title, c.Description = "Leftover from a compaction interrupted before the swap", "An unsealed 4 KiB lab.kv.compact (zero superblock, as compaction leaves it before sealing) next to a valid database."
		c.ExpectCode, c.ExpectSeverity, c.ExpectRule, c.ExpectRecovery = "COMPACTION_LEFTOVER_COMPACT", Warning, "A1", Recoverable
	case "lock-conflict":
		c.Title, c.Description = "Database open in another process", "A healthy database scanned while another handle holds its lock."
		c.ExpectCode, c.ExpectSeverity, c.ExpectRecovery, c.HoldLock = "LOCK_HELD", Warning, Detected, true
	}
	return c, err
}

// OpenHolder opens a lab case's database and keeps it open (holding the
// lock) until the returned function is called. Used for "lock-conflict".
func OpenHolder(path string) (func() error, error) {
	db, err := kvdb.Open(path, 0)
	if err != nil {
		return nil, err
	}
	return db.Close, nil
}
