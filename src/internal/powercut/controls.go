package powercut

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

func jsonMarshal(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

// maxControlsPerKind bounds the negative controls per kind.
const maxControlsPerKind = 3

// runControls generates states OUTSIDE the failure model at point k and
// checks that validation flags them. They are never counted as NEXUS
// failures: a detected control shows the checks have teeth; an undetected
// one is reported as a gap in the checks (not hidden).
func runControls(d *disk, k int, ops []WorkOp, prevSynced map[int][]byte, root string, rep *Report, done map[string]bool) {
	dbIno, ok := d.durableDir[DBName]
	if !ok {
		return
	}
	n := d.inodes[dbIno]
	scratch := &Report{Outcomes: map[string]int{}, Checks: map[string]int{}}
	base := func() map[string][]byte {
		files := map[string][]byte{}
		for name, ino := range d.durableDir {
			files[name] = append([]byte(nil), d.inodes[ino].durable...)
		}
		return files
	}

	// fsync-lie: an operation was acknowledged at exactly this point, but
	// the disk silently dropped the writes of the last flush it reported
	// complete. Violates M1.
	if prev, ok := prevSynced[dbIno]; ok && count(done, "fsync-lie") < maxControlsPerKind && len(prev) < len(n.durable) {
		for _, o := range ops {
			if o.Ack == k && o.Kind != KCompact && o.Kind != KDelete {
				files := base()
				files[DBName] = append([]byte(nil), prev...)
				f := validate(Image{Point: k, Variant: "control-fsync-lie", Files: files}, ops, filepath.Join(root, "controls"), scratch, false)
				c := Control{Name: "fsync-lie", Point: k, Model: "M1 (synced data is durable)", Detected: f != nil}
				if f != nil {
					c.How, c.XRay = f.Kind+": "+f.Detail, f.XRay
				} else {
					c.How = "NOT DETECTED: the checks accepted an image that lost a flushed, acknowledged write"
				}
				rep.Controls = append(rep.Controls, c)
				done[fmt.Sprintf("fsync-lie-%d", k)] = true
				break
			}
		}
	}

	// bit-flip: one bit of an already-synced committed frame flips (bit rot
	// or a misdirected write). Violates M2 and the no-bit-rot assumption.
	if count(done, "bit-flip") < maxControlsPerKind && len(n.durable) > 400 && len(n.pending) == 0 && k%7 == 0 {
		files := base()
		b := files[DBName]
		at := 32 + (len(b)-32)/3
		b[at] ^= 0x10
		f := validate(Image{Point: k, Variant: "control-bit-flip", Files: files}, ops, filepath.Join(root, "controls"), scratch, false)
		c := Control{Name: "bit-flip", Point: k, Model: fmt.Sprintf("M2 / no bit rot (byte %d of %d flipped)", at, len(b)), Detected: f != nil}
		if f != nil {
			c.How, c.XRay = f.Kind+": "+f.Detail, f.XRay
		} else {
			c.How = "NOT DETECTED: a flipped bit in committed data was accepted"
		}
		rep.Controls = append(rep.Controls, c)
		done[fmt.Sprintf("bit-flip-%d", k)] = true
	}
}

func count(done map[string]bool, prefix string) int {
	n := 0
	for k := range done {
		if len(k) > len(prefix) && k[:len(prefix)+1] == prefix+"-" {
			n++
		}
	}
	return n
}
