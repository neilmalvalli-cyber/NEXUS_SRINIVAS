package recovery

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
)

// TestOpenMillionKeysTiming measures Open on a log of 1,000,000 distinct
// keys (100 frames of 10,000 PUTs) against the 2-second budget of DESIGN
// §8.4. It writes about 40 MB, so it runs only with KVDB_PERF=1. The timing
// is reported, and the test fails if it exceeds the budget.
func TestOpenMillionKeysTiming(t *testing.T) {
	if os.Getenv("KVDB_PERF") != "1" {
		t.Skip("set KVDB_PERF=1 to run")
	}
	const keys, perFrame = 1_000_000, format.MaxBatchOps
	var buf bytes.Buffer
	buf.Write(superblock(t, 1, 32))
	es := make([]format.Entry, 0, perFrame)
	seq := uint64(1)
	for i := 0; i < keys; i++ {
		es = append(es, put(fmt.Sprintf("key-%08d", i), fmt.Sprintf("value-%08d", i)))
		if len(es) == perFrame {
			if _, err := format.WriteFrame(&buf, seq, es, format.MaxFrameOps); err != nil {
				t.Fatal(err)
			}
			seq++
			es = es[:0]
		}
	}
	db := filepath.Join(t.TempDir(), "db.kv")
	if err := os.WriteFile(db, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	start := time.Now()
	st, err := Open(fileio.OS{}, db, DefaultOptions)
	el := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	runtime.ReadMemStats(&m1)
	t.Logf("Open of %d keys (%.1f MB): %v, heap in use after %.0f MB, total allocated %.0f MB",
		st.Scan.Index.Len(), float64(buf.Len())/1e6, el,
		float64(m1.HeapInuse)/1e6, float64(m1.TotalAlloc-m0.TotalAlloc)/1e6)
	if st.Scan.Index.Len() != keys {
		t.Fatalf("index has %d keys", st.Scan.Index.Len())
	}
	if el > 2*time.Second {
		t.Fatalf("Open took %v, budget 2s", el)
	}
}
