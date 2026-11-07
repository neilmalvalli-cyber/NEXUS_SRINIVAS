package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

// The oracle. Everything here is computed from what the torture tool itself
// sent and received; the database's output is only the thing being judged.
//
// Model: at the start of an iteration the database must hold exactly the
// verified state of the previous iteration (the baseline). During the
// iteration each writer sends one request at a time and waits for its reply,
// so a writer has at most one request in flight when the process is killed.
// Every write request ends in one of three states:
//
//	acked    "ACK <op-id>" was read from serve's stdout: a promise.
//	rejected "ERR <op-id> <class>" with a definite class: never applied.
//	unknown  no reply (killed), "ERR ... unknown", or "ERR ... io":
//	         may be fully applied or fully absent, never partly.
//
// Check (per key k, given a choice of which unknown requests were applied):
// the recovered state of k must be produced by a candidate: the baseline, or
// an applied request whose effect on k (last op on k inside a batch) equals
// what was recovered; and no applied request on k may be known to have
// started after the candidate finished (sendT > candidate's ackT). The real
// commit order always respects that real-time order, so this never rejects a
// correct database. It rejects: a lost acknowledged write (the stale value
// has a later acked write after it), a value that no request wrote ("unknown
// value"), a value from a rejected request, and a partial batch (an unknown
// batch visible on one key but not another, under every applied/not-applied
// choice). Keys no request touched must equal the baseline exactly.

// valueHash identifies a value without keeping it (values reach 1 MiB).
func valueHash(v []byte) string {
	s := sha256.Sum256(v)
	return hex.EncodeToString(s[:12])
}

type status int

const (
	stPending status = iota
	stAcked
	stRejected
	stUnknown
)

func (s status) String() string {
	return [...]string{"pending", "acked", "rejected", "unknown"}[s]
}

// effect is what a request does to one key.
type effect struct {
	del  bool
	hash string // put: hash of the value
}

func (e effect) String() string {
	if e.del {
		return "DEL"
	}
	return "PUT#" + e.hash[:8]
}

type opRec struct {
	id      string
	writer  int
	kind    string // put, del, batch, compact, get
	nops    int
	effects map[string]effect // key -> last effect inside the request
	sendT   int64             // ns since iteration start, taken before the request was written
	endT    int64             // ns since iteration start, when the reply was read (acked)
	status  status
	errText string
	// get replies
	getKey   string
	getFound bool
	getHash  string
}

// cell is a key's state: present with a value hash, or absent.
type cell struct {
	present bool
	hash    string
}

func (c cell) String() string {
	if !c.present {
		return "absent"
	}
	return "value#" + c.hash[:8]
}

func cellOf(m map[string]string, k string) cell {
	h, ok := m[k]
	return cell{present: ok, hash: h}
}

func (e effect) produces(c cell) bool {
	if e.del {
		return !c.present
	}
	return c.present && c.hash == e.hash
}

// violation describes the first inconsistency found.
type violation struct {
	Key      string   `json:"key"`
	Expected string   `json:"expected"`
	Found    string   `json:"found"`
	Ops      []string `json:"ops"`
	Reason   string   `json:"reason"`
}

func (v *violation) Error() string {
	return fmt.Sprintf("key %q: %s\n  expected: %s\n  found:    %s\n  operations on this key:\n    %s",
		v.Key, v.Reason, v.Expected, v.Found, strings.Join(v.Ops, "\n    "))
}

// checkState verifies the recovered state against the baseline and the
// operation history. It returns nil when some choice of applied/not-applied
// for the unknown requests explains every key.
func checkState(baseline, final map[string]string, ops []*opRec) *violation {
	byKey := map[string][]*opRec{}
	var unknown []*opRec
	for _, o := range ops {
		if o.kind == "get" || o.kind == "compact" {
			continue
		}
		for k := range o.effects {
			byKey[k] = append(byKey[k], o)
		}
		if o.status == stUnknown && len(o.effects) > 0 {
			unknown = append(unknown, o)
		}
	}
	// Keys no request touched must be exactly as in the baseline.
	for _, m := range []map[string]string{baseline, final} {
		for k := range m {
			if _, touched := byKey[k]; touched {
				continue
			}
			if b, f := cellOf(baseline, k), cellOf(final, k); b != f {
				return &violation{Key: k, Expected: b.String() + " (no operation touched this key)", Found: f.String(),
					Reason: "key changed although no operation in this iteration wrote it (lost or invented data)"}
			}
		}
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(unknown) > 16 {
		return &violation{Reason: fmt.Sprintf("internal: %d unknown requests (one per writer expected)", len(unknown))}
	}
	var firstFail *violation
	for mask := 0; mask < 1<<len(unknown); mask++ {
		applied := map[*opRec]bool{}
		for i, u := range unknown {
			if mask&(1<<i) != 0 {
				applied[u] = true
			}
		}
		var fail *violation
		for _, k := range keys {
			if fail = checkKey(k, baseline, final, byKey[k], applied); fail != nil {
				break
			}
		}
		if fail == nil {
			return nil
		}
		// Prefer reporting the choice that applies exactly the visible
		// unknown requests (the most natural explanation).
		if firstFail == nil || mask == visibleMask(unknown, final) {
			firstFail = fail
		}
	}
	if len(unknown) > 0 {
		var ids []string
		for _, u := range unknown {
			ids = append(ids, u.id)
		}
		firstFail.Reason += fmt.Sprintf(" (checked every applied/not-applied choice for unknown requests %s)", strings.Join(ids, ", "))
	}
	return firstFail
}

func visibleMask(unknown []*opRec, final map[string]string) int {
	m := 0
	for i, u := range unknown {
		for k, e := range u.effects {
			if !e.del && e.produces(cellOf(final, k)) {
				m |= 1 << i
				break
			}
		}
	}
	return m
}

func checkKey(k string, baseline, final map[string]string, ops []*opRec, applied map[*opRec]bool) *violation {
	f := cellOf(final, k)
	var writes []*opRec // acked or applied unknown
	for _, o := range ops {
		if o.status == stAcked || applied[o] {
			writes = append(writes, o)
		}
	}
	end := func(o *opRec) int64 {
		if o.status == stAcked {
			return o.endT
		}
		return math.MaxInt64 // unknown: may have committed at any time up to the kill
	}
	defeated := func(oEnd int64) *opRec {
		for _, b := range writes {
			if b.sendT > oEnd {
				return b
			}
		}
		return nil
	}
	// Candidate: the baseline.
	if b := cellOf(baseline, k); b == f && defeated(math.MinInt64) == nil {
		return nil
	}
	for _, o := range writes {
		if o.effects[k].produces(f) && defeated(end(o)) == nil {
			return nil
		}
	}
	// Failure: describe what was expected.
	v := &violation{Key: k, Found: f.String()}
	var exp []string
	if defeated(math.MinInt64) == nil {
		exp = append(exp, "baseline "+cellOf(baseline, k).String())
	}
	for _, o := range writes {
		if defeated(end(o)) == nil {
			exp = append(exp, fmt.Sprintf("%s from %s", o.effects[k], o.id))
		}
	}
	v.Expected = "one of: " + strings.Join(exp, " | ")
	for _, o := range ops {
		v.Ops = append(v.Ops, fmt.Sprintf("%-22s %-7s %-8s %s sent=%.3fms done=%s", o.id, o.kind, o.status, o.effects[k], ms(o.sendT), doneStr(o)))
	}
	v.Ops = append(v.Ops, "baseline: "+cellOf(baseline, k).String())
	switch {
	case producedByStatus(ops, f, k, stRejected):
		v.Reason = "recovered value was written by a request that was REJECTED"
	case !producedByAny(ops, f, k) && cellOf(baseline, k) != f:
		v.Reason = "recovered value was never written to this key (unknown or torn value)"
	default:
		v.Reason = "an acknowledged write is missing or a batch is partially applied"
	}
	return v
}

func producedByStatus(ops []*opRec, c cell, k string, s status) bool {
	for _, o := range ops {
		if o.status == s && o.effects[k].produces(c) {
			return true
		}
	}
	return false
}

func producedByAny(ops []*opRec, c cell, k string) bool {
	for _, o := range ops {
		if o.effects[k].produces(c) {
			return true
		}
	}
	return false
}

func ms(ns int64) float64 { return float64(ns) / 1e6 }

func doneStr(o *opRec) string {
	if o.status == stAcked {
		return fmt.Sprintf("%.3fms", ms(o.endT))
	}
	return "-"
}

// checkGets verifies get replies: a value returned by get must be the
// baseline value or a value some request of this iteration put on that key,
// and never one from a rejected request.
func checkGets(baseline map[string]string, ops []*opRec) *violation {
	for _, g := range ops {
		if g.kind != "get" || g.status != stAcked || !g.getFound {
			continue
		}
		c := cell{present: true, hash: g.getHash}
		if cellOf(baseline, g.getKey) == c {
			continue
		}
		ok := false
		for _, o := range ops {
			if o.status != stRejected && o.effects[g.getKey].produces(c) && o.sendT <= g.endT {
				ok = true
				break
			}
		}
		if !ok {
			return &violation{Key: g.getKey, Expected: "baseline value or a value written to this key before the reply",
				Found: c.String() + " returned by " + g.id, Reason: "get returned a value never written to this key (torn, foreign or rejected value)"}
		}
	}
	return nil
}

// parseDump parses `kvdb dump` output into key -> value hash. Lines are
// "<key> <value>" or, for keys/values that are not plain text, two Go-quoted
// strings.
func parseDump(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	br := bufio.NewReaderSize(r, 1<<20)
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
			qk, rest, err2 := cutQuoted(line)
			if err2 != nil {
				return nil, fmt.Errorf("dump line %d: %v", n, err2)
			}
			qv, rest2, err2 := cutQuoted(strings.TrimPrefix(rest, " "))
			if err2 != nil || rest2 != "" {
				return nil, fmt.Errorf("dump line %d: bad quoted value", n)
			}
			k, v = qk, qv
		} else {
			i := strings.IndexByte(line, ' ')
			if i <= 0 {
				return nil, fmt.Errorf("dump line %d: no separator in %q", n, truncate(line, 60))
			}
			k, v = line[:i], line[i+1:]
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("dump line %d: key %q listed twice", n, k)
		}
		out[k] = valueHash([]byte(v))
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
