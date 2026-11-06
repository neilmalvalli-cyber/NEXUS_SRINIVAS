package powercut

import (
	"bytes"
	"math/rand"
	"path/filepath"
	"testing"
)

func TestSectorPieces(t *testing.T) {
	w := pendingWrite{off: 500, data: bytes.Repeat([]byte{1}, 1100)}
	ps := sectorPieces(w)
	// [500,512) [512,1024) [1024,1536) [1536,1600)
	if len(ps) != 4 || ps[0].off != 500 || len(ps[0].data) != 12 || ps[3].off != 1536 || len(ps[3].data) != 64 {
		t.Fatalf("%+v", ps)
	}
}

func TestModelSyncAndDirectory(t *testing.T) {
	d := newDisk()
	d.apply(0, Event{Op: OpCreate, Name: "a", Inode: 1})
	d.apply(1, Event{Op: OpWrite, Inode: 1, Off: 0, Data: []byte("hello")})
	imgs := d.images(2, rand.New(rand.NewSource(1)), 50)
	var synced, all *Image
	for i := range imgs {
		switch imgs[i].Variant {
		case "only-synced-state":
			synced = &imgs[i]
		case "all-pending-persisted":
			all = &imgs[i]
		}
	}
	if synced == nil || all == nil {
		t.Fatalf("variants: %v", imgs)
	}
	if _, ok := synced.Files["a"]; ok {
		t.Fatal("an unsynced directory entry must be absent in the synced-only image")
	}
	if string(all.Files["a"]) != "hello" {
		t.Fatalf("all-pending: %q", all.Files["a"])
	}
	d.apply(2, Event{Op: OpSync, Inode: 1})
	d.apply(3, Event{Op: OpSyncDir})
	d.apply(4, Event{Op: OpWrite, Inode: 1, Off: 5, Data: []byte(" world")})
	for _, img := range d.images(5, rand.New(rand.NewSource(1)), 50) {
		s := string(img.Files["a"])
		if s != "hello" && s != "hello world" {
			t.Fatalf("%s: %q (synced bytes must survive; one sector, so all or nothing)", img.Variant, s)
		}
	}
}

// TestExplorerSmall runs the whole pipeline on a small workload: the real
// engine, recorded trace, modeled images, real recovery, oracle, controls.
func TestExplorerSmall(t *testing.T) {
	cfg := Config{Seed: 7, Writers: 3, OpsPerW: 25, MaxPoints: 120, MaxVar: 6, Controls: true, Dir: filepath.Join(t.TempDir(), "run")}
	rep, err := Run(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s; events=%d bytes=%d frames=%d points=%d/%d images=%d outcomes=%v controls=%d", rep.Verdict,
		rep.Trace.Events, rep.Trace.BytesWritten, rep.Trace.Frames, rep.CrashPoints, rep.CrashSpace, rep.Images, rep.Outcomes, len(rep.Controls))
	for _, f := range rep.Failures {
		t.Errorf("failure %s: %s: %s (xray %+v)", f.Image, f.Kind, f.Detail, f.XRay)
	}
	if rep.Images == 0 || rep.Trace.ByOp[OpSync] == 0 || rep.Checks["batch-atomicity"] == 0 {
		t.Fatalf("pipeline did not exercise anything: %+v", rep.Checks)
	}
	kinds := map[string]bool{}
	for _, c := range rep.Controls {
		if !c.Detected {
			t.Errorf("negative control not detected: %+v", c)
		}
		kinds[c.Name] = true
	}
	if !kinds["fsync-lie"] {
		t.Errorf("no fsync-lie control ran")
	}
	if _, err := Run(cfg, nil); err == nil {
		t.Fatal("a second run into the same non-empty directory must be refused")
	}
}
