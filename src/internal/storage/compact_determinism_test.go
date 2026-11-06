package storage

import (
	"bytes"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
)

// snapshotKeys decodes the sealed snapshot of a compacted file and returns
// its keys in file order (every snapshot entry is a PUT).
func snapshotKeys(t *testing.T, db string) []string {
	t.Helper()
	b := readFile(t, db)
	sb, err := format.DecodeSuperblock(b)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for off := uint64(format.SuperblockSize); off < sb.SnapshotEnd; {
		h, err := format.DecodeFrameHeader(b[off : off+format.FrameHeaderSize])
		if err != nil {
			t.Fatalf("frame at %d: %v", off, err)
		}
		p := off + format.FrameHeaderSize
		end := p + h.PayloadLen
		for p < end {
			eh, err := format.DecodeEntryHeader(b[p:])
			if err != nil {
				t.Fatal(err)
			}
			_, k, _, err := format.DecodeEntry(b[p : p+eh.Size()])
			if err != nil {
				t.Fatal(err)
			}
			if eh.Op != format.OpPut {
				t.Fatalf("snapshot entry %q is op %d", k, eh.Op)
			}
			keys = append(keys, string(k))
			p += eh.Size()
		}
		off = end
	}
	return keys
}

func assertSorted(t *testing.T, keys []string) {
	t.Helper()
	if !sort.SliceIsSorted(keys, func(i, j int) bool { return keys[i] < keys[j] }) {
		t.Fatalf("snapshot keys not in byte order: %q", keys[:min(len(keys), 20)])
	}
	for i := 1; i < len(keys); i++ {
		if keys[i] == keys[i-1] {
			t.Fatalf("duplicate key %q in snapshot", keys[i])
		}
	}
}

// history builds a database from a random sequence of commits that ends in
// the live state want, with exactly n frames (next_seq n+1), and
// returns the file after an explicit Compact.
func history(t *testing.T, seed int64, want map[string]string, n int) []byte {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	db := newDB(t)
	l := open(t, fileio.OS{}, db)
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	r.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
	// Noise until exactly n-len(keys) frames exist: random overwrites,
	// deletes and batches. A commit with no net effect writes no frame
	// (and uses no sequence number), so progress is measured by NextSeq.
	target := uint64(1 + n - len(keys))
	for l.Stats().NextSeq < target {
		k := keys[r.Intn(len(keys))]
		switch r.Intn(3) {
		case 0:
			mustCommit(t, l, put(k, fmt.Sprintf("noise-%d", r.Int())))
		case 1:
			mustCommit(t, l, put(k, "x"), del(k)) // net effect: delete (or nothing)
		default:
			mustCommit(t, l, put("tmp-"+k, "t"), del("tmp-"+k), put(k, "y"))
		}
	}
	// Final values, in a random order, one frame each.
	for _, k := range keys {
		mustCommit(t, l, put(k, want[k]))
	}
	if got := l.Stats().NextSeq; got != uint64(1+n) {
		t.Fatalf("history ended at next_seq %d, want %d", got, 1+n)
	}
	if got := contents(t, l); !reflect.DeepEqual(got, want) {
		t.Fatal("history did not reach the wanted state")
	}
	if err := l.Compact(); err != nil {
		t.Fatal(err)
	}
	l.Close()
	return readFile(t, db)
}

// Two different histories with the same live state and the same number of
// frames (so the same next sequence number) compact to byte-identical files,
// and the snapshot lists keys in ascending byte order.
func TestCompactDeterministicOutput(t *testing.T) {
	want := map[string]string{}
	for i := 0; i < 300; i++ {
		want[fmt.Sprintf("key-%03d-%s", (i*7919)%300, strings.Repeat("z", i%5))] = fmt.Sprintf("value-%d", i)
	}
	want["\x00binary"], want["\xffhigh"], want["Upper"] = "a", "b", "c"
	const n = 2000
	a := history(t, 1, want, n)
	b := history(t, 2, want, n)
	c := history(t, 3, want, n)
	if !bytes.Equal(a, b) || !bytes.Equal(a, c) {
		t.Fatalf("same state, different compacted bytes (%d, %d, %d bytes)", len(a), len(b), len(c))
	}
}

// Every compaction path (plain, automatic, compacting commit under a cap,
// multi-frame) writes keys in byte order; the result reopens with the same
// contents and accepts writes afterwards.
func TestCompactSnapshotSortedEveryPath(t *testing.T) {
	t.Run("plain multi-frame", func(t *testing.T) {
		db := newDB(t)
		l := open(t, fileio.OS{}, db)
		want := map[string]string{}
		r := rand.New(rand.NewSource(7))
		for i := 0; i < 3000; i++ {
			k := fmt.Sprintf("%08x", r.Uint32())
			v := strings.Repeat(string(rune('a'+i%26)), 1500+r.Intn(1000))
			mustCommit(t, l, put(k, v))
			want[k] = v
		}
		if err := l.Compact(); err != nil {
			t.Fatal(err)
		}
		keys := snapshotKeys(t, db)
		assertSorted(t, keys)
		if len(keys) != len(want) {
			t.Fatalf("snapshot has %d keys, want %d", len(keys), len(want))
		}
		mustCommit(t, l, put("after", "1"))
		want["after"] = "1"
		l.Close()
		if !reflect.DeepEqual(reopen(t, db), want) {
			t.Fatal("reopen after compaction differs")
		}
	})
	t.Run("compacting commit under cap", func(t *testing.T) {
		db := newDB(t)
		l := openCap(t, fileio.OS{}, db, 8192, false)
		want := map[string]string{}
		v := strings.Repeat("v", 300)
		compacted := false
		for i := 0; i < 60; i++ {
			k := fmt.Sprintf("k%d", (i*37)%9)
			before := l.Stats().Size
			mustCommit(t, l, put(k, fmt.Sprint(i)+v), put("a"+k, "x"))
			want[k], want["a"+k] = fmt.Sprint(i)+v, "x"
			if l.Stats().Size < before {
				compacted = true
				assertSorted(t, snapshotKeys(t, db))
			}
		}
		if !compacted {
			t.Fatal("no compacting commit happened")
		}
		mustCommit(t, l, put("k0", "after"))
		want["k0"] = "after"
		l.Close()
		if !reflect.DeepEqual(reopen(t, db), want) {
			t.Fatal("reopen differs")
		}
	})
	t.Run("automatic", func(t *testing.T) {
		db := newDB(t)
		l := open(t, fileio.OS{}, db)
		defer l.Close()
		big := strings.Repeat("x", 200<<10)
		for i := 0; i < 60; i++ {
			mustCommit(t, l, put(fmt.Sprintf("z%02d", 59-i%10), fmt.Sprint(i)+big))
		}
		if sb := superblockOf(t, db); sb.SnapshotEnd == format.SuperblockSize {
			t.Fatal("automatic compaction did not run")
		}
		assertSorted(t, snapshotKeys(t, db))
	})
}
