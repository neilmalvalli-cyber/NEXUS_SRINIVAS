package recovery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio/faultfs"
)

// readOnlyEvents fails the test if ffs recorded any operation that could
// change a file: create, remove, move, write, truncate, sync, directory
// sync, or a read-write open. Lock/unlock of `<db>.lock` is allowed.
func readOnlyEvents(t *testing.T, ffs *faultfs.FS) {
	t.Helper()
	for _, e := range ffs.Events() {
		switch e.Op {
		case faultfs.OpStat, faultfs.OpOpen, faultfs.OpRead, faultfs.OpSize,
			faultfs.OpClose, faultfs.OpLock, faultfs.OpUnlock:
		default:
			t.Fatalf("read-only inspection performed %s\n%s", e, ffs.TraceString())
		}
	}
}

// inspectUnchanged runs Inspect through faultfs and checks that no file in
// dir changed (bytes and names; the lock file is ignored) and that only
// read-only operations were performed.
func inspectUnchanged(t *testing.T, dir, db string) (*Inspection, error) {
	t.Helper()
	before := snapshot(t, dir)
	ffs := faultfs.New(fileio.OS{})
	in, err := Inspect(ffs, db, fastOpts)
	readOnlyEvents(t, ffs)
	if after := snapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatalf("Inspect changed the directory:\nbefore %v\nafter  %v", names(before), names(after))
	}
	if ffs.OpenHandles() != 0 {
		t.Fatalf("Inspect leaked %d handles", ffs.OpenHandles())
	}
	return in, err
}

func TestInspectCleanLog(t *testing.T) {
	data, want := base(t)
	dir, db := writeDB(t, data)
	in, err := inspectUnchanged(t, dir, db)
	if err != nil {
		t.Fatal(err)
	}
	if in.NeedsRepair() || len(in.Repairs()) != 0 {
		t.Fatalf("clean log needs repair: %v", in.Repairs())
	}
	if !reflect.DeepEqual(in.Rules, []Rule{RuleA1}) || in.Target != db || in.Missing {
		t.Fatalf("inspection = %+v", in)
	}
	if in.Scan.Index.Len() != len(want) || in.Scan.Frames != 3 || in.Scan.NextSeq != 10 || in.Scan.End != int64(len(data)) {
		t.Fatalf("scan = %+v", in.Scan)
	}
}

// Every possible torn length of the final frame: Inspect reports exactly the
// truncation a normal Open then performs, and changes nothing itself.
func TestInspectTornTailEveryLength(t *testing.T) {
	clean, want := base(t)
	last := frame(t, 10, put("d", "4"), put("e", "5"))
	for n := 1; n < len(last); n++ {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			data := cat(clean, last[:n])
			dir, db := writeDB(t, data)
			in, err := inspectUnchanged(t, dir, db)
			if err != nil {
				t.Fatalf("torn tail of %d bytes: %v", n, err)
			}
			if !in.NeedsRepair() || in.Scan.Tail == TailClean || in.Scan.End != int64(len(clean)) || in.Scan.Size != int64(len(data)) {
				t.Fatalf("inspection = %+v scan = %+v", in, in.Scan)
			}
			if len(in.Repairs()) != 1 {
				t.Fatalf("repairs = %v", in.Repairs())
			}
			// The real Open does exactly what Inspect predicted.
			got, st := openState(t, fileio.OS{}, db)
			if !reflect.DeepEqual(got, want) || st.Scan.End != in.Scan.End || st.Scan.Tail != in.Scan.Tail || !st.Truncated {
				t.Fatalf("Open: contents %v end %d tail %s truncated %v; Inspect said end %d tail %s",
					got, st.Scan.End, st.Scan.Tail, st.Truncated, in.Scan.End, in.Scan.Tail)
			}
			// Afterwards, nothing more to repair.
			in2, err := inspectUnchanged(t, dir, db)
			if err != nil || in2.NeedsRepair() {
				t.Fatalf("after Open: err %v repairs %v", err, in2.Repairs())
			}
		})
	}
}

// Damage with valid data after it is corruption: Inspect refuses with
// ErrCorruption exactly as Open does, and changes nothing.
func TestInspectMiddleCorruption(t *testing.T) {
	data, _ := base(t)
	f7 := len(superblock(t, 7, 32))
	f8 := f7 + len(frame(t, 7, put("a", "1"), put("b", "2")))
	cases := map[string]int{
		"payload byte of the first frame":  f7 + 40,
		"payload byte of the middle frame": f8 + 35,
	}
	for name, off := range cases {
		t.Run(name, func(t *testing.T) {
			bad := append([]byte(nil), data...)
			bad[off] ^= 0x40
			dir, db := writeDB(t, bad)
			_, err := inspectUnchanged(t, dir, db)
			if !errors.Is(err, dberr.ErrCorruption) {
				t.Fatalf("err = %v, want ErrCorruption", err)
			}
			if _, oerr := Open(fileio.OS{}, db, fastOpts); !errors.Is(oerr, dberr.ErrCorruption) {
				t.Fatalf("Open err = %v; Inspect and Open disagree", oerr)
			}
		})
	}
}

// Damage inside the sealed snapshot (rule C0) is corruption even at the end
// of the file, and a damaged superblock is a bad format (A2).
func TestInspectDamagedSnapshotAndSuperblock(t *testing.T) {
	f7 := frame(t, 7, put("a", "1"), put("b", "2"))
	f8 := frame(t, 8, del("a"), put("c", "3"))
	snap := cat(superblock(t, 7, uint64(32+len(f7)+len(f8))), f7, f8)

	torn := append([]byte(nil), snap[:len(snap)-3]...) // tail inside the snapshot
	tornDir := t.TempDir()
	tornDB := filepath.Join(tornDir, "db.kv")
	if err := os.WriteFile(tornDB, torn, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectUnchanged(t, tornDir, tornDB); !errors.Is(err, dberr.ErrCorruption) {
		t.Fatalf("snapshot cut short: err = %v, want ErrCorruption", err)
	}

	flip := append([]byte(nil), snap...)
	flip[len(flip)-2] ^= 1 // last frame of the snapshot, CRC now wrong
	dir, db := writeDB(t, flip)
	if _, err := inspectUnchanged(t, dir, db); !errors.Is(err, dberr.ErrCorruption) {
		t.Fatalf("damaged snapshot frame: err = %v, want ErrCorruption", err)
	}

	sbBad := append([]byte(nil), snap...)
	sbBad[3] ^= 0xff
	dir, db = writeDB(t, sbBad)
	if _, err := inspectUnchanged(t, dir, db); !errors.Is(err, dberr.ErrBadFormat) {
		t.Fatalf("damaged superblock: err = %v, want ErrBadFormat", err)
	}
}

// Inspect's plan agrees with what Open actually does for every combination
// of {missing, valid, unsealed, corrupt} for path, .compact, .old and .new
// (256 layouts): same refusals, same authority rules, and "needs repair"
// exactly when Open changes the directory.
func TestInspectMatchesOpenForEveryLayout(t *testing.T) {
	kinds := []string{"missing", "valid", "unsealed", "corrupt"}
	sufs := []string{"", ".compact", ".old", ".new"}
	seqs := []uint64{seqPath, seqCompact, seqOld, seqNew}
	for mask := 0; mask < 256; mask++ {
		l := layout{}
		desc := ""
		for i, suf := range sufs {
			k := (mask >> (2 * i)) & 3
			desc += fmt.Sprintf("%s=%s ", "path"+suf, kinds[k])
			switch k {
			case 1:
				l[suf] = sb(seqs[i])
			case 2:
				l[suf] = unsealed
			case 3:
				l[suf] = corrupt
			}
		}
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			dirI, dirO := t.TempDir(), t.TempDir()
			dbI, dbO := setup(t, dirI, l), setup(t, dirO, l)
			in, ierr := inspectUnchanged(t, dirI, dbI)

			before := snapshot(t, dirO)
			st, oerr := Open(fileio.OS{}, dbO, fastOpts)
			if st != nil {
				st.Close()
			}
			changed := !reflect.DeepEqual(before, snapshot(t, dirO))

			switch {
			case (ierr == nil) != (oerr == nil):
				t.Fatalf("%s: Inspect err %v, Open err %v", desc, ierr, oerr)
			case ierr != nil:
				for _, cls := range []error{dberr.ErrBadFormat, dberr.ErrCorruption} {
					if errors.Is(ierr, cls) != errors.Is(oerr, cls) {
						t.Fatalf("%s: Inspect err %v, Open err %v", desc, ierr, oerr)
					}
				}
				if changed {
					t.Fatalf("%s: Open refused but changed files", desc)
				}
			default:
				want := st.Resolution.Rules
				got := in.Rules
				if in.Missing {
					got = []Rule{RuleA7, RuleA1}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s: Inspect rules %v, Open rules %v", desc, got, want)
				}
				if in.NeedsRepair() != changed {
					t.Fatalf("%s: NeedsRepair %v but Open changed files: %v (repairs %v)", desc, in.NeedsRepair(), changed, in.Repairs())
				}
			}
		})
	}
}

// A held lock makes Inspect fail with ErrLocked without reading anything.
func TestInspectLocked(t *testing.T) {
	data, _ := base(t)
	dir, db := writeDB(t, data)
	st, err := Open(fileio.OS{}, db, fastOpts)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := inspectUnchanged(t, dir, db); !errors.Is(err, dberr.ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
}
