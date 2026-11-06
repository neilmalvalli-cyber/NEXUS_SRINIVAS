package powercut

import (
	"fmt"
	"math/rand"
	"sort"
)

// SectorSize is the atomic write unit of the model (assumption M2).
const SectorSize = 512

// The storage failure model (docs/POWER_CUT_EXPLORER.md §2), as code:
//
//	M1 Synced data is durable: after Sync of an inode returns, every write
//	   to that inode issued before the Sync survives power loss.
//	M2 Sectors are atomic: a 512-byte aligned sector is either entirely old
//	   or entirely new after power loss; never a mix, never garbage.
//	M3 Unsynced writes may be lost, kept, or kept partially at sector
//	   granularity, in ANY combination (writeback can reorder them). Bytes a
//	   lost write would have extended the file with read as zeros if a later
//	   write that survived extended it further.
//	M4 Directory operations (create, rename, remove) are atomic and applied
//	   in the order issued; after SyncDir they are durable; before, any
//	   ordered prefix of them may have survived.
//	M5 File contents and directory entries are independent: an inode's data
//	   survives according to M1–M3 whichever names point at it.
//
// Not modeled (out of scope; see the document): a disk that acknowledges a
// flush it did not perform (violates M1), torn sectors (violates M2),
// bit rot and misdirected writes, and filesystem metadata journaling bugs.

type pendingWrite struct {
	event int // trace index
	off   int64
	data  []byte
	trunc bool
	size  int64
}

type inode struct {
	durable []byte
	pending []pendingWrite
}

type dirOp struct {
	event int
	op    Op
	name  string
	to    string
	inode int
}

// disk is the model state while replaying a trace.
type disk struct {
	inodes     map[int]*inode
	durableDir map[string]int
	pendingDir []dirOp
}

func newDisk() *disk { return &disk{inodes: map[int]*inode{}, durableDir: map[string]int{}} }

func applyWrite(b []byte, w pendingWrite) []byte {
	if w.trunc {
		if int64(len(b)) >= w.size {
			return b[:w.size]
		}
		return append(b, make([]byte, w.size-int64(len(b)))...)
	}
	end := w.off + int64(len(w.data))
	if int64(len(b)) < end {
		b = append(b, make([]byte, end-int64(len(b)))...)
	}
	copy(b[w.off:], w.data)
	return b
}

// apply advances the model by one event.
func (d *disk) apply(i int, e Event) {
	switch e.Op {
	case OpCreate:
		d.inodes[e.Inode] = &inode{}
		d.pendingDir = append(d.pendingDir, dirOp{event: i, op: OpCreate, name: e.Name, inode: e.Inode})
	case OpWrite:
		n := d.inodes[e.Inode]
		n.pending = append(n.pending, pendingWrite{event: i, off: e.Off, data: e.Data})
	case OpTruncate:
		n := d.inodes[e.Inode]
		n.pending = append(n.pending, pendingWrite{event: i, trunc: true, size: e.Size})
	case OpSync:
		n := d.inodes[e.Inode]
		for _, w := range n.pending {
			n.durable = applyWrite(n.durable, w)
		}
		n.pending = nil
	case OpMove, OpRemove:
		d.pendingDir = append(d.pendingDir, dirOp{event: i, op: e.Op, name: e.Name, to: e.To, inode: e.Inode})
	case OpSyncDir:
		d.durableDir = applyDir(d.durableDir, d.pendingDir)
		d.pendingDir = nil
	}
}

func applyDir(dir map[string]int, ops []dirOp) map[string]int {
	out := make(map[string]int, len(dir))
	for k, v := range dir {
		out[k] = v
	}
	for _, o := range ops {
		switch o.op {
		case OpCreate:
			out[o.name] = o.inode
		case OpMove:
			delete(out, o.name)
			out[o.to] = o.inode
		case OpRemove:
			delete(out, o.name)
		}
	}
	return out
}

// Image is one modeled post-power-loss state of the database directory.
type Image struct {
	Point   int               // crash point: events [0, Point) happened
	Variant string            // how unsynced state was resolved
	Files   map[string][]byte // base name -> contents
	// Lost reports whether any unsynced write or directory operation was
	// dropped (false for "everything reached the disk").
	Lost bool
}

// pendingCount is the number of unsynced data writes plus pending directory
// operations: zero means every variant is the same image.
func (d *disk) pendingCount() (writes, dirOps int) {
	for _, n := range d.inodes {
		writes += len(n.pending)
	}
	return writes, len(d.pendingDir)
}

// sectorPieces splits a write into its 512-byte-aligned sector pieces (M2).
func sectorPieces(w pendingWrite) []pendingWrite {
	if w.trunc {
		return []pendingWrite{w}
	}
	var out []pendingWrite
	off, data := w.off, w.data
	for len(data) > 0 {
		n := SectorSize - int(off%SectorSize)
		if n > len(data) {
			n = len(data)
		}
		out = append(out, pendingWrite{event: w.event, off: off, data: data[:n]})
		off += int64(n)
		data = data[n:]
	}
	return out
}

// contents builds an inode's image from durable data plus the chosen
// subset of its pending sector pieces, applied in issue order.
func contents(n *inode, keep func(piece pendingWrite, idx, total int) bool) ([]byte, bool) {
	b := append([]byte(nil), n.durable...)
	var pieces []pendingWrite
	for _, w := range n.pending {
		pieces = append(pieces, sectorPieces(w)...)
	}
	lost := false
	for i, p := range pieces {
		if keep(p, i, len(pieces)) {
			b = applyWrite(b, p)
		} else {
			lost = true
		}
	}
	return b, lost
}

// images generates the modeled images for the current state. The variants
// are chosen deterministically from rng (seeded per run), up to max.
func (d *disk) images(point int, rng *rand.Rand, max int) []Image {
	writes, dops := d.pendingCount()
	var out []Image
	type policy struct {
		name    string
		dirKeep int // number of pending dir ops kept (ordered prefix, M4)
		keep    func(p pendingWrite, i, n int) bool
	}
	all := func(pendingWrite, int, int) bool { return true }
	none := func(pendingWrite, int, int) bool { return false }
	pols := []policy{{"all-pending-persisted", dops, all}}
	if writes > 0 || dops > 0 {
		pols = append(pols, policy{"only-synced-state", 0, none})
	}
	if writes > 0 {
		// Ordered prefixes at sector granularity (includes torn writes:
		// a prefix that ends inside a multi-sector write).
		total := 0
		for _, n := range d.inodes {
			for _, w := range n.pending {
				total += len(sectorPieces(w))
			}
		}
		cuts := map[int]bool{}
		for _, c := range []int{1, total / 2, total - 1} {
			if c > 0 && c < total {
				cuts[c] = true
			}
		}
		for len(cuts) < 6 && len(cuts) < total-1 {
			cuts[1+rng.Intn(total-1)] = true
		}
		var cs []int
		for c := range cuts {
			cs = append(cs, c)
		}
		sort.Ints(cs)
		for _, c := range cs {
			c := c
			pols = append(pols, policy{fmt.Sprintf("prefix-%d-of-%d-sectors", c, total), dops, func(_ pendingWrite, i, _ int) bool { return i < c }})
		}
		// Reordered writeback (M3): random subsets of sectors.
		for k := 0; k < 3; k++ {
			seed := rng.Int63()
			sub := rand.New(rand.NewSource(seed))
			pols = append(pols, policy{fmt.Sprintf("random-subset-%x", seed&0xffff), dops, func(pendingWrite, int, int) bool { return sub.Intn(2) == 0 }})
		}
	}
	if dops > 1 {
		for k := 1; k < dops; k++ {
			pols = append(pols, policy{fmt.Sprintf("dir-prefix-%d-of-%d", k, dops), k, all})
		}
	}
	if dops > 0 && writes > 0 {
		pols = append(pols, policy{"dir-synced-only+all-data", 0, all})
	}
	for _, p := range pols {
		if len(out) >= max {
			break
		}
		dir := applyDir(d.durableDir, d.pendingDir[:p.dirKeep])
		img := Image{Point: point, Variant: p.name, Files: map[string][]byte{}, Lost: p.dirKeep < dops}
		// Inodes are visited in a fixed order so random policies are
		// reproducible.
		var inos []int
		for ino := range d.inodes {
			inos = append(inos, ino)
		}
		sort.Ints(inos)
		data := map[int][]byte{}
		for _, ino := range inos {
			b, lost := contents(d.inodes[ino], p.keep)
			data[ino] = b
			img.Lost = img.Lost || lost
		}
		for name, ino := range dir {
			img.Files[name] = data[ino]
		}
		out = append(out, img)
	}
	return out
}
