package xray

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/kvdb"
)

// dirState hashes every file in dir (name -> sha256). Used to prove a
// read-only scan creates, changes and deletes nothing, not even a lock file.
func dirState(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil { // a held lock file cannot be read on Windows: compare size and mtime
			fi, serr := os.Stat(filepath.Join(dir, e.Name()))
			if serr != nil {
				t.Fatal(err)
			}
			out[e.Name()] = fmt.Sprintf("unreadable size=%d mtime=%d", fi.Size(), fi.ModTime().UnixNano())
			continue
		}
		s := sha256.Sum256(b)
		out[e.Name()] = hex.EncodeToString(s[:])
	}
	return out
}

func find(r *Report, code string) *Finding {
	for i := range r.Findings {
		if r.Findings[i].Code == code {
			return &r.Findings[i]
		}
	}
	return nil
}

func codes(r *Report) string {
	var c []string
	for _, f := range r.Findings {
		c = append(c, fmt.Sprintf("%s/%s/%s", f.Code, f.Severity, f.Recovery))
	}
	sort.Strings(c)
	return strings.Join(c, " ")
}

// TestLabDiagnoses builds every lab case and checks the diagnosis, its
// severity, rule, offset and recovery classification; that the engine and
// the walk agree; that nothing claims "repaired" or "recovered" in a
// read-only scan; and that no byte of any file changed and no file appeared.
func TestLabDiagnoses(t *testing.T) {
	cases, err := BuildLab(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != len(LabCaseNames) {
		t.Fatalf("built %d cases, want %d", len(cases), len(LabCaseNames))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var release func() error
			if c.HoldLock {
				if release, err = OpenHolder(c.Path); err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			dir := filepath.Dir(c.Path)
			before := dirState(t, dir)
			r, err := Scan(c.Path, Options{})
			if err != nil {
				t.Fatal(err)
			}
			after := dirState(t, dir)
			if fmt.Sprint(before) != fmt.Sprint(after) {
				t.Fatalf("read-only scan changed the directory:\nbefore %v\nafter  %v", before, after)
			}
			if r.Modified || r.Mode != "read-only" {
				t.Fatalf("modified=%v mode=%s", r.Modified, r.Mode)
			}
			f := find(r, c.ExpectCode)
			if f == nil {
				t.Fatalf("no %s finding; got: %s", c.ExpectCode, codes(r))
			}
			if f.Severity != c.ExpectSeverity {
				t.Errorf("severity %s, want %s", f.Severity, c.ExpectSeverity)
			}
			if f.Recovery != c.ExpectRecovery {
				t.Errorf("recovery %s, want %s", f.Recovery, c.ExpectRecovery)
			}
			if c.ExpectRule != "" && f.Rule != c.ExpectRule {
				t.Errorf("rule %s, want %s", f.Rule, c.ExpectRule)
			}
			if c.ExpectOffset != nil && (f.Offset == nil || *f.Offset != *c.ExpectOffset) {
				t.Errorf("offset %v, want %d", f.Offset, *c.ExpectOffset)
			}
			if f.Simple == "" || f.Technical == "" || f.Action == "" || f.DataImpact == "" || f.Component == "" {
				t.Errorf("incomplete finding: %+v", f)
			}
			for _, g := range r.Findings {
				if g.Recovery == Repaired || g.Recovery == Recovered {
					t.Errorf("read-only scan claims %s for %s", g.Recovery, g.Code)
				}
				if g.Code == "XRAY_ENGINE_DISAGREEMENT" {
					t.Errorf("engine and walk disagree: %s", g.Technical)
				}
			}
			if r.Recovery.Status == Repaired || r.Recovery.Status == Recovered || r.Recovery.FileModified || r.Recovery.Truncated {
				t.Errorf("read-only recovery summary claims changes: %+v", r.Recovery)
			}
			// JSON round trip (the API and CLI --json use it).
			if _, err := json.Marshal(r); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: health=%s engine=%s status=%s findings=%s", c.Name, r.Health, r.Engine.Outcome, r.Recovery.Status, codes(r))
		})
	}
}

// TestLabEngineOutcomes pins what the engine itself says per case, so the
// lab cannot drift into faults the engine treats differently.
func TestLabEngineOutcomes(t *testing.T) {
	want := map[string]string{
		"clean": "clean", "bad-magic": "refused", "unsupported-version": "refused",
		"superblock-checksum": "refused", "torn-tail": "repairable", "incomplete-header": "repairable",
		"final-payload-checksum": "repairable", "committed-frame-corrupted": "refused",
		"header-damaged-mid-log": "repairable", "invalid-seq": "refused", "invalid-op": "refused",
		"entry-checksum": "refused", "snapshot-corrupted": "refused",
		"interrupted-compaction": "repairable", "compaction-leftover": "repairable", "lock-conflict": "clean",
	}
	cases, err := BuildLab(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		r, err := Scan(c.Path, Options{NoHash: true})
		if err != nil {
			t.Fatal(err)
		}
		if r.Engine.Outcome != want[c.Name] {
			t.Errorf("%s: engine outcome %s, want %s (%s)", c.Name, r.Engine.Outcome, want[c.Name], r.Engine.Error)
		}
		// The real open must agree with the verdict: refused cases fail to
		// open with ErrCorruption/ErrBadFormat. Checked on a copy so the
		// lab case itself stays untouched.
		if want[c.Name] == "refused" {
			cp := filepath.Join(t.TempDir(), "copy.kv")
			b, _ := os.ReadFile(c.Path)
			os.WriteFile(cp, b, 0o644)
			db, err := kvdb.Open(cp, 0)
			if err == nil {
				db.Close()
				t.Errorf("%s: X-RAY says refused but kvdb.Open succeeded", c.Name)
			} else if !errors.Is(err, dberr.ErrCorruption) && !errors.Is(err, dberr.ErrBadFormat) {
				t.Errorf("%s: open error %v", c.Name, err)
			}
		}
	}
}

// TestMidLogHeaderDamageWouldLoseCommittedFrames proves the X-RAY forward
// check is right: opening the damaged copy really does drop committed keys.
func TestMidLogHeaderDamageWouldLoseCommittedFrames(t *testing.T) {
	root := t.TempDir()
	c, err := buildCase(root, "header-damaged-mid-log")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := Scan(c.Path, Options{})
	if r.Walk == nil || r.Walk.LaterFrameSeq == nil {
		t.Fatalf("forward check found nothing: %s", codes(r))
	}
	if !strings.HasPrefix(r.Recovery.DataLoss, "YES") || r.Recovery.Status != Detected {
		t.Fatalf("summary does not warn: %+v", r.Recovery)
	}
	rep, err := Repair(c.Path, filepath.Join(root, "bak"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Repair.Verified || rep.Repair.OpenError == "" || rep.Modified {
		t.Fatalf("repair must refuse: %+v", rep.Repair)
	}
	// Evidence: a normal open of a copy loses keys written after frame 3.
	cp := filepath.Join(t.TempDir(), "copy.kv")
	b, _ := os.ReadFile(c.Path)
	os.WriteFile(cp, b, 0o644)
	db, err := kvdb.Open(cp, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Get([]byte("user:6")); !errors.Is(err, kvdb.ErrNotFound) {
		t.Fatalf("expected the committed key user:6 to be lost by a normal open, got %v", err)
	}
}

// TestRepairTornTail: repair backs up, truncates via the real open,
// verifies, and only then reports "repaired".
func TestRepairTornTail(t *testing.T) {
	root := t.TempDir()
	c, err := buildCase(root, "torn-tail")
	if err != nil {
		t.Fatal(err)
	}
	orig, _ := os.ReadFile(c.Path)
	r, err := Repair(c.Path, filepath.Join(root, "bak"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	rr := r.Repair
	if !rr.Verified || r.Recovery.Status != Repaired || !r.Recovery.Truncated {
		t.Fatalf("repair not verified: %+v / %+v", rr, r.Recovery)
	}
	if f := find(r, "FRAME_PAYLOAD_TRUNCATED"); f == nil || f.Recovery != Repaired {
		t.Fatalf("finding not marked repaired: %+v", f)
	}
	if len(rr.Backups) == 0 {
		t.Fatal("no backup")
	}
	bak, _ := os.ReadFile(rr.Backups[0])
	if string(bak) != string(orig) {
		t.Fatal("backup differs from the original")
	}
	if rr.After == nil || rr.After.Outcome != "clean" {
		t.Fatalf("post-repair scan: %+v", rr.After)
	}
}

// TestRepairRefusesCorruption: nothing is touched for refused files.
func TestRepairRefusesCorruption(t *testing.T) {
	for _, name := range []string{"committed-frame-corrupted", "bad-magic", "invalid-seq"} {
		root := t.TempDir()
		c, err := buildCase(root, name)
		if err != nil {
			t.Fatal(err)
		}
		before := dirState(t, filepath.Dir(c.Path))
		r, err := Repair(c.Path, filepath.Join(root, "bak"), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if r.Repair.Verified || r.Modified || len(r.Repair.Backups) != 0 {
			t.Errorf("%s: repair acted on a refused file: %+v", name, r.Repair)
		}
		if fmt.Sprint(before) != fmt.Sprint(dirState(t, filepath.Dir(c.Path))) {
			t.Errorf("%s: files changed", name)
		}
	}
}

func TestRepairInterruptedCompaction(t *testing.T) {
	root := t.TempDir()
	c, err := buildCase(root, "interrupted-compaction")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Repair(c.Path, filepath.Join(root, "bak"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Repair.Verified || len(r.Repair.Backups) != 2 { // .compact and .old (path is missing)
		t.Fatalf("%+v", r.Repair)
	}
	db, err := kvdb.Open(c.Path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if v, err := db.Get([]byte("acct:b")); err != nil || string(v) != "200" {
		t.Fatalf("data after adoption: %q %v", v, err)
	}
}

func TestMissingAndUsage(t *testing.T) {
	if _, err := Scan("", Options{}); !errors.Is(err, dberr.ErrInvalidArgument) {
		t.Fatal(err)
	}
	dir := t.TempDir()
	r, err := Scan(filepath.Join(dir, "none.kv"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Engine.Outcome != "missing" || find(r, "DB_MISSING") == nil {
		t.Fatalf("%s %s", r.Engine.Outcome, codes(r))
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("scan of a missing database created files: %v", ents)
	}
}

func TestFromError(t *testing.T) {
	at := time.Now()
	for _, tc := range []struct {
		err  error
		code string
		sev  Severity
	}{
		{fmt.Errorf("x: %w", dberr.ErrFull), "OP_FULL", Error},
		{fmt.Errorf("x: %w", dberr.ErrOutcomeUnknown), "OP_OUTCOME_UNKNOWN", Error},
		{fmt.Errorf("x: %w", dberr.ErrFailed), "OP_HANDLE_FAILED", Error},
		{fmt.Errorf("x: %w", dberr.ErrLocked), "OP_LOCKED", Warning},
		{fmt.Errorf("x: %w", dberr.ErrCorruption), "OP_CORRUPTION", Critical},
		{errors.New("disk on fire"), "OP_ERROR", Error},
	} {
		f, ok := FromError("put", "db.kv", tc.err, at)
		if !ok || f.Code != tc.code || f.Severity != tc.sev || f.OriginalError != tc.err.Error() {
			t.Errorf("%v -> %+v", tc.err, f)
		}
	}
	for _, e := range []error{nil, dberr.ErrNotFound, dberr.ErrInvalidArgument, dberr.ErrTooLarge} {
		if _, ok := FromError("get", "db.kv", e, at); ok {
			t.Errorf("%v must not be a finding", e)
		}
	}
}

func TestDeadBytesWarning(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.kv")
	db, err := kvdb.Open(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		db.Put([]byte("k"), []byte(strings.Repeat("v", 200)))
	}
	db.Close()
	r, _ := Scan(p, Options{})
	if find(r, "SPACE_DEAD_BYTES") != nil {
		t.Fatalf("warned below the auto-compaction floor: %s", codes(r))
	}
	r, _ = Scan(p, Options{DeadMinBytes: 1})
	if f := find(r, "SPACE_DEAD_BYTES"); f == nil || f.Severity != Warning {
		t.Fatalf("%s", codes(r))
	}
}
