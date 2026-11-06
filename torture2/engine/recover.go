package engine

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/oracle"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/proc"
)

// Recovery is what the engine observed while restarting a database after a
// crash, using only the real executable's CLI.
type Recovery struct {
	ReadOnly     string       `json:"read_only_check"`     // first line of `check --no-repair`
	ReadOnlyExit int          `json:"read_only_exit"`      // 0 OK, 4 needs repair
	ReadOnlyMs   float64      `json:"read_only_ms"`        //
	RestartMs    float64      `json:"restart_ms"`          // wall time of `kvdb open` (process start + recovery + exit)
	RecoveryMs   float64      `json:"recovery_ms"`         // recovery time reported by kvdb open
	OpenKeys     int          `json:"open_keys"`           //
	TornTail     bool         `json:"torn_tail_discarded"` //
	Authority    bool         `json:"interrupted_compaction_resolved"`
	Created      bool         `json:"created_new"`
	CheckMs      float64      `json:"check_ms"`
	DumpMs       float64      `json:"dump_ms"`
	FileBytes    int64        `json:"file_bytes"`
	StaleAfter   []string     `json:"stale_files_after_open,omitempty"`
	Problem      string       `json:"problem,omitempty"` // non-empty: recovery itself failed
	State        oracle.State `json:"-"`
}

// Recover restarts db after a crash and reads back its state:
//
//  1. `check --no-repair` must not modify any file and must say OK or
//     NEEDS REPAIR (a crash may leave a torn tail; never corruption);
//  2. `open` must succeed (this is the recovery run);
//  3. `check` must print OK (full-file verification);
//  4. `dump` gives the recovered state, which must list as many keys as
//     open reported.
func Recover(b proc.Binary, db string, maxBytes int64, timeout time.Duration) Recovery {
	var rc Recovery
	mb := maxBytesArgs(maxBytes)

	before, err := Fingerprint(db)
	if err != nil {
		rc.Problem = "fingerprint: " + err.Error()
		return rc
	}
	r := b.Run(timeout, nil, "check", db, "--no-repair")
	rc.ReadOnlyMs = ms(r.Dur)
	rc.ReadOnlyExit = r.Exit
	rc.ReadOnly = firstLine(r.Stdout)
	after, err := Fingerprint(db)
	if err != nil {
		rc.Problem = "fingerprint: " + err.Error()
		return rc
	}
	if d := before.Diff(after); d != "" {
		rc.Problem = "check --no-repair modified the database files: " + d
		return rc
	}
	switch {
	case r.StartErr != nil || r.TimedOut:
		rc.Problem = "check --no-repair: " + r.String()
		return rc
	case r.Exit == 0 && rc.ReadOnly == "OK":
	case r.Exit == 4 && strings.HasPrefix(rc.ReadOnly, "NEEDS REPAIR"):
	default:
		// A crash can only leave states that open repairs (exit 4); any
		// other answer (CORRUPT, BAD FORMAT, LOCKED, ERROR) is a failure.
		rc.Problem = "check --no-repair after a crash did not say OK or NEEDS REPAIR: " + r.String()
		return rc
	}

	r = b.Run(timeout, nil, append([]string{"open", db}, mb...)...)
	rc.RestartMs = ms(r.Dur)
	if !r.OK() {
		rc.Problem = "open after crash failed: " + r.String()
		return rc
	}
	var size int64
	if _, err := fmt.Sscanf(firstLine(r.Stdout), "keys=%d recovery_ms=%f size=%d", &rc.OpenKeys, &rc.RecoveryMs, &size); err != nil {
		rc.Problem = fmt.Sprintf("cannot parse open output %q: %v", firstLine(r.Stdout), err)
		return rc
	}
	rc.TornTail = strings.Contains(r.Stderr, "discarded a torn")
	rc.Authority = strings.Contains(r.Stderr, "rule A3") || strings.Contains(r.Stderr, "rule A4") || strings.Contains(r.Stderr, "rule A5") || strings.Contains(r.Stderr, "authority")
	rc.Created = strings.Contains(r.Stderr, "created a new")
	rc.StaleAfter = StaleFiles(db)

	r = b.Run(timeout, nil, "check", db)
	rc.CheckMs = ms(r.Dur)
	if !r.OK() || firstLine(r.Stdout) != "OK" {
		rc.Problem = "check after recovery did not print OK: " + r.String()
		return rc
	}
	st, dms, err := Dump(b, db, timeout)
	rc.DumpMs = dms
	if err != nil {
		rc.Problem = "dump: " + err.Error()
		return rc
	}
	rc.State = st
	if len(st) != rc.OpenKeys {
		rc.Problem = fmt.Sprintf("open reported %d keys but dump listed %d", rc.OpenKeys, len(st))
		return rc
	}
	if fi, err := os.Stat(db); err == nil {
		rc.FileBytes = fi.Size()
	}
	if maxBytes > 0 && rc.FileBytes > maxBytes {
		rc.Problem = fmt.Sprintf("file is %d bytes, above --max-bytes %d", rc.FileBytes, maxBytes)
	}
	return rc
}

// Dump runs `kvdb dump` and parses its output into a state.
func Dump(b proc.Binary, db string, timeout time.Duration) (oracle.State, float64, error) {
	r := b.Run(timeout, nil, "dump", db)
	if !r.OK() {
		return nil, ms(r.Dur), fmt.Errorf("dump failed: %s", r.String())
	}
	st, err := ParseDump(strings.NewReader(r.Stdout))
	return st, ms(r.Dur), err
}

// ParseDump parses `kvdb dump` output: "<key> <value>" per line, or two
// Go-quoted strings when either is not plain text.
func ParseDump(rd io.Reader) (oracle.State, error) {
	out := oracle.State{}
	br := bufio.NewReaderSize(rd, 1<<20)
	n := 0
	for {
		line, err := br.ReadString('\n')
		if err == io.EOF && line == "" {
			return out, nil
		}
		if err != nil && err != io.EOF {
			return nil, err
		}
		n++
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		var k, v string
		if strings.HasPrefix(line, `"`) {
			qk, rest, e1 := cutQuoted(line)
			qv, rest2, e2 := cutQuoted(strings.TrimPrefix(rest, " "))
			if e1 != nil || e2 != nil || rest2 != "" {
				return nil, fmt.Errorf("dump line %d: bad quoted form", n)
			}
			k, v = qk, qv
		} else {
			i := strings.IndexByte(line, ' ')
			if i <= 0 {
				return nil, fmt.Errorf("dump line %d: no separator", n)
			}
			k, v = line[:i], line[i+1:]
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("dump line %d: key %q listed twice", n, k)
		}
		out[k] = oracle.HashValue([]byte(v))
		if err == io.EOF {
			return out, nil
		}
	}
}

func cutQuoted(s string) (string, string, error) {
	q, err := strconv.QuotedPrefix(s)
	if err != nil {
		return "", "", err
	}
	u, err := strconv.Unquote(q)
	return u, s[len(q):], err
}

// Files is a fingerprint of the database file and its companions.
type Files map[string]string

// Fingerprint hashes db and every companion file (<db>.compact, .old, .new);
// the lock file is excluded (check --no-repair may create it, empty).
func Fingerprint(db string) (Files, error) {
	f := Files{}
	for _, p := range append([]string{db}, companions(db)...) {
		b, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		s := sha256.Sum256(b)
		f[filepath.Base(p)] = fmt.Sprintf("%d:%s", len(b), hex.EncodeToString(s[:8]))
	}
	return f, nil
}

// Diff describes how two fingerprints differ ("" if equal).
func (a Files) Diff(b Files) string {
	var d []string
	for k, v := range a {
		if b[k] != v {
			d = append(d, fmt.Sprintf("%s %s -> %s", k, v, orNone(b[k])))
		}
	}
	for k, v := range b {
		if _, ok := a[k]; !ok {
			d = append(d, fmt.Sprintf("%s created (%s)", k, v))
		}
	}
	sort.Strings(d)
	return strings.Join(d, "; ")
}

func orNone(s string) string {
	if s == "" {
		return "deleted"
	}
	return s
}

func companions(db string) []string {
	return []string{db + ".compact", db + ".old", db + ".new"}
}

// StaleFiles lists companion files that exist.
func StaleFiles(db string) []string {
	var out []string
	for _, p := range companions(db) {
		if _, err := os.Stat(p); err == nil {
			out = append(out, filepath.Base(p))
		}
	}
	return out
}

// CopyFiles copies db and its companions into dir (for failure evidence).
func CopyFiles(db, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, p := range append([]string{db}, companions(db)...) {
		b, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(p)), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func maxBytesArgs(n int64) []string {
	if n > 0 {
		return []string{"--max-bytes", strconv.FormatInt(n, 10)}
	}
	return nil
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return strings.TrimRight(s, "\r")
}

func ms(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1e6 }
