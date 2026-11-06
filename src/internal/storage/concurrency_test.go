package storage

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
)

// TestConcurrentMixedWorkloadStaysConsistent runs PUT, DELETE, BATCH, GET
// and Range concurrently (exercising the lock split, the read-handle pool
// and group commit), then checks that the handle verifies, that a reopen
// rebuilds exactly the same state, and that every batch was atomic: each
// batch writes the same value under all its keys, so after the run all keys
// of one batch group must agree.
func TestConcurrentMixedWorkloadStaysConsistent(t *testing.T) {
	db := newDB(t)
	l := open(t, fileio.OS{}, db)
	const writers, readers, rounds, groups = 6, 4, 60, 8
	var wg sync.WaitGroup
	var stop atomic.Bool
	var gets atomic.Int64
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(r)))
			for !stop.Load() {
				k := fmt.Sprintf("s%d", rng.Intn(40))
				if _, err := l.Get([]byte(k)); err != nil && !errors.Is(err, dberr.ErrNotFound) {
					t.Errorf("get %s: %v", k, err)
					return
				}
				gets.Add(1)
				if rng.Intn(200) == 0 {
					if err := l.Range(func(k, v []byte) error { return nil }); err != nil {
						t.Errorf("range: %v", err)
						return
					}
				}
			}
		}(r)
	}
	var ww sync.WaitGroup
	for w := 0; w < writers; w++ {
		ww.Add(1)
		go func(w int) {
			defer ww.Done()
			rng := rand.New(rand.NewSource(int64(100 + w)))
			for i := 0; i < rounds; i++ {
				var es []format.Entry
				switch rng.Intn(3) {
				case 0:
					es = []format.Entry{put(fmt.Sprintf("s%d", rng.Intn(40)), fmt.Sprintf("w%d-%d", w, i))}
				case 1:
					es = []format.Entry{del(fmt.Sprintf("s%d", rng.Intn(40)))}
				default: // batch: one value under every key of a group
					g, v := rng.Intn(groups), fmt.Sprintf("b%d-%d", w, i)
					for k := 0; k < 5; k++ {
						es = append(es, put(fmt.Sprintf("g%d-%d", g, k), v))
					}
				}
				if err := l.Commit(es); err != nil {
					t.Errorf("commit: %v", err)
					return
				}
			}
		}(w)
	}
	ww.Wait()
	stop.Store(true)
	wg.Wait()
	if t.Failed() {
		l.Close()
		return
	}
	if _, err := l.Verify(); err != nil {
		t.Fatalf("verify after concurrent run: %v", err)
	}
	want := contents(t, l)
	for g := 0; g < groups; g++ {
		v0, ok := want[fmt.Sprintf("g%d-0", g)]
		for k := 1; k < 5; k++ {
			if v, ok2 := want[fmt.Sprintf("g%d-%d", g, k)]; ok2 != ok || v != v0 {
				t.Fatalf("batch group %d not atomic: key 0 %q, key %d %q", g, v0, k, v)
			}
		}
	}
	l.Close()
	if got := reopen(t, db); !reflect.DeepEqual(got, want) {
		t.Fatalf("reopen differs: %d keys vs %d", len(got), len(want))
	}
	t.Logf("%d gets during the run", gets.Load())
}

// TestGroupCommitSplitsUnderMaxBytes: when a merged frame would not be a
// plain append (the cap forces a compacting commit or ErrFull), the group is
// split and every caller gets the answer it would get alone. Here the cap
// only admits a few small PUTs; concurrent large PUTs must each get ErrFull
// or succeed individually, and the database must verify afterwards.
func TestGroupCommitSplitsUnderMaxBytes(t *testing.T) {
	db := newDB(t)
	l := openCap(t, fileio.OS{}, db, 4096, false)
	var wg sync.WaitGroup
	var ok, full atomic.Int64
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				err := l.Commit([]format.Entry{put(fmt.Sprintf("k%d-%d", w, i), fmt.Sprintf("%0300d", i))})
				switch {
				case err == nil:
					ok.Add(1)
				case errors.Is(err, dberr.ErrFull):
					full.Add(1)
				default:
					t.Errorf("unexpected error: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
	if ok.Load() == 0 || full.Load() == 0 {
		t.Fatalf("expected some commits admitted and some refused, got ok=%d full=%d", ok.Load(), full.Load())
	}
	if st := l.Stats(); st.Size > 4096 {
		t.Fatalf("file %d bytes exceeds max_bytes 4096", st.Size)
	}
	if _, err := l.Verify(); err != nil {
		t.Fatal(err)
	}
	if got := contents(t, l); len(got) != int(ok.Load()) {
		t.Fatalf("%d keys present, %d commits acknowledged", len(got), ok.Load())
	}
	l.Close()
}
