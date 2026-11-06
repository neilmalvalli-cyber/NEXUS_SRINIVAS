package admission

// Decision says how a commit may proceed.
type Decision int

const (
	// Append: write the frame at the end of the live log.
	Append Decision = iota
	// Compact: run a compacting commit (snapshot of the live state with the
	// commit applied, swapped in with the replacement protocol).
	Compact
	// Full: refuse with ErrFull; nothing changes.
	Full
)

func (d Decision) String() string {
	switch d {
	case Append:
		return "append"
	case Compact:
		return "compacting commit"
	case Full:
		return "full"
	}
	return "Decision(?)"
}

// Request describes a commit for admission. All values are byte counts.
type Request struct {
	Cap       uint64 // max_bytes; 0 = unlimited
	End       uint64 // current file size (end of the last frame)
	FrameSize uint64 // size of the frame an append would write
	// PutBearing is true when the commit's net effect leaves at least one
	// key holding a PUT. A commit whose net effect only removes keys is
	// "delete-only" (DESIGN §12.3 rule 3).
	PutBearing bool
}

// Fits reports whether a file of size end can grow by n bytes and stay
// within cap. It checks end against cap before subtracting, so it cannot
// underflow (DESIGN §12.1), and never adds, so it cannot overflow.
func Fits(cap, end, n uint64) bool {
	if cap == 0 {
		return true
	}
	return end <= cap && n <= cap-end
}

// Decide applies the approved working policy (DESIGN §12.3, assumption A2
// in JUDGE_CLARIFICATIONS.md). snapshotSize returns the exact size of the
// file a compacting commit would produce; it is called only when needed,
// because computing it walks the whole index.
//
//   - Unlimited, or the append stays within the cap: Append.
//   - PUT-bearing commit: Compact if the snapshot fits the cap, else Full.
//     (Rule 2: a commit with PUTs must finish at or below max_bytes.)
//   - Delete-only commit: Compact if the snapshot fits the cap or is
//     strictly smaller than the current file, else Full. (Rule 3: on an
//     over-cap file a delete must strictly reduce the size.)
//
// Full never changes committed data (rule 5).
func Decide(r Request, snapshotSize func() uint64) Decision {
	if Fits(r.Cap, r.End, r.FrameSize) {
		return Append
	}
	s := snapshotSize()
	if s <= r.Cap {
		return Compact
	}
	if !r.PutBearing && s < r.End {
		return Compact
	}
	return Full
}

// CompactWorthwhile is the rule for a standalone compaction (DESIGN §12.3
// rule 4: it must never increase the file): run it only when the snapshot is
// strictly smaller than the current file.
func CompactWorthwhile(end, snapshot uint64) bool { return snapshot < end }

// AutoCompactMinSize is the smallest file that triggers automatic
// compaction (DESIGN §10.1).
const AutoCompactMinSize = 4 << 20

// ShouldAutoCompact implements the automatic trigger of DESIGN §10.1: the
// file is at least 4 MiB and more than twice its estimated compacted size.
// estimate must not exceed the real snapshot size by much; a low estimate
// only makes the trigger fire earlier, and the compaction itself still
// checks CompactWorthwhile against the exact size.
func ShouldAutoCompact(end, estimate uint64) bool {
	return end >= AutoCompactMinSize && end/2 > estimate
}
