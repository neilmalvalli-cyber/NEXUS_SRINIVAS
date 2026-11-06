package powercut

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/recovery"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/storage"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/xray"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/kvdb"
)

// DBName is the database file name used inside the run directory.
const DBName = "pc.kv"

// Config is a Power-Cut Explorer run. Every field is part of the
// reproducibility record: the same Config on the same build replays the
// same workload choices and the same image variants (the interleaving of
// concurrent writers is recorded, not reproduced; see the document).
type Config struct {
	Seed      int64 `json:"seed"`
	Writers   int   `json:"writers"`
	OpsPerW   int   `json:"ops_per_writer"`
	MaxPoints int   `json:"max_crash_points"`
	MaxVar    int   `json:"max_variants_per_point"`
	// Dir is the work directory; it must not exist or be empty.
	Dir string `json:"dir"`
	// KeepPassed keeps every image on disk (default: only failures).
	KeepPassed bool `json:"keep_passed"`
	// Controls runs the negative controls (states outside the model).
	Controls bool `json:"controls"`
}

// DefaultConfig is the configuration used by `nexus-powercut` without flags.
var DefaultConfig = Config{Seed: 1, Writers: 4, OpsPerW: 60, MaxPoints: 400, MaxVar: 8, Controls: true}

// OpKind is a workload operation type.
type OpKind string

const (
	KPut     OpKind = "put"     // unique key, written once
	KHot     OpKind = "hot"     // overwrite of the writer's own hot key
	KBatch   OpKind = "batch"   // 2–5 unique keys in one atomic batch
	KDelete  OpKind = "delete"  // delete of a unique key the writer PUT earlier
	KCompact OpKind = "compact" // explicit compaction (writer 0 only)
)

// WorkOp is one workload operation and when it happened in the trace.
type WorkOp struct {
	ID     int      `json:"id"`
	Writer int      `json:"writer"`
	Seq    int      `json:"seq"` // per-writer program order
	Kind   OpKind   `json:"kind"`
	Keys   []string `json:"keys,omitempty"`
	Values []string `json:"-"`
	Issue  int      `json:"issue"` // trace index when issued
	Ack    int      `json:"ack"`   // trace index when acknowledged; -1 = not acknowledged
	Err    string   `json:"err,omitempty"`
}

// Failure is an image that NEXUS did not handle correctly under the model.
type Failure struct {
	Image   string       `json:"image"`
	Point   int          `json:"crash_point"`
	Variant string       `json:"variant"`
	Kind    string       `json:"kind"`
	Detail  string       `json:"detail"`
	Dir     string       `json:"dir,omitempty"` // kept image (pre-recovery copy)
	XRay    *XRaySummary `json:"xray,omitempty"`
}

// XRaySummary links a failure to its X-RAY diagnosis.
type XRaySummary struct {
	Health   string   `json:"health"`
	Engine   string   `json:"engine_outcome"`
	Findings []string `json:"findings"` // "SEVERITY CODE @offset: title"
	Report   string   `json:"report"`   // path of the full JSON report
}

// Control is one negative control: a state OUTSIDE the failure model,
// generated to prove the checks can detect real damage.
type Control struct {
	Name     string       `json:"name"`
	Point    int          `json:"crash_point"`
	Model    string       `json:"violates"`
	Detected bool         `json:"detected"`
	How      string       `json:"how"`
	XRay     *XRaySummary `json:"xray,omitempty"`
}

// Report is the result of a run.
type Report struct {
	Tool        string    `json:"tool"`
	Version     int       `json:"version"`
	Config      Config    `json:"config"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
	DurationMS  float64   `json:"duration_ms"`
	Environment struct {
		GOOS      string `json:"goos"`
		GOARCH    string `json:"goarch"`
		GoVersion string `json:"go_version"`
		CPUs      int    `json:"cpus"`
	} `json:"environment"`
	Model      []string `json:"model"`
	NotModeled []string `json:"not_modeled"`
	Disclaimer string   `json:"disclaimer"`
	Trace      struct {
		Events       int            `json:"events"`
		ByOp         map[Op]int     `json:"by_op"`
		BytesWritten int64          `json:"bytes_written"`
		Ops          int            `json:"workload_ops"`
		Acked        int            `json:"acknowledged_ops"`
		ByKind       map[OpKind]int `json:"by_kind"`
		Frames       int            `json:"frames_written"`
	} `json:"trace"`
	CrashPoints int            `json:"crash_points_tested"`
	CrashSpace  int            `json:"crash_points_in_trace"`
	Images      int            `json:"images_validated"`
	ByVariant   map[string]int `json:"images_by_variant_family"`
	Outcomes    map[string]int `json:"outcomes"`
	Checks      map[string]int `json:"checks"`
	Passed      int            `json:"passed"`
	Failed      int            `json:"failed"`
	Failures    []Failure      `json:"failures"`
	Controls    []Control      `json:"negative_controls"`
	Verdict     string         `json:"verdict"`
	Reproduce   string         `json:"reproduce"`
}

// ModelText is the failure model in words (also in the document).
var ModelText = []string{
	"M1 synced data is durable: writes to a file issued before a completed fsync (FlushFileBuffers) survive",
	"M2 512-byte sectors are atomic: each aligned sector is entirely old or entirely new",
	"M3 unsynced writes may be lost, kept, or kept partially (whole sectors), in any combination and order",
	"M4 directory operations are atomic and ordered; durable after SyncDir, otherwise any ordered prefix survives",
	"M5 file data and directory entries survive independently of each other",
}

// NotModeledText lists what the generated images never contain.
var NotModeledText = []string{
	"disks or controllers that acknowledge a flush they did not perform (violates M1; exercised only as a negative control)",
	"torn or garbage sectors (violates M2), bit rot, misdirected or phantom writes (one bit flip is exercised as a negative control)",
	"filesystem metadata corruption or journaling bugs; Windows does not expose directory fsync (SyncDir is a no-op there), so M4 is an assumption about NTFS",
	"crashes during recovery itself (covered by the torture framework's recovery-kill modes, not here)",
	"every possible interleaving of concurrent writers: one recorded interleaving per run",
}

// Disclaimer is printed with every result.
const Disclaimer = "A finite, seeded sample of modeled power-loss states. Passing it is evidence, not an exhaustive proof that NEXUS survives every possible power loss."

func isEmptyOrMissing(dir string) error {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(ents) > 0 {
		return fmt.Errorf("powercut: %s is not empty; refusing to write there", dir)
	}
	return nil
}

// Run records a workload, generates modeled images and validates them.
// progress, if not nil, is called after each crash point (done, total).
func Run(cfg Config, progress func(done, total, failed int)) (*Report, error) {
	if cfg.Writers < 1 || cfg.Writers > 16 || cfg.OpsPerW < 1 || cfg.OpsPerW > 10000 || cfg.MaxPoints < 1 || cfg.MaxVar < 1 {
		return nil, fmt.Errorf("%w: writers 1–16, ops_per_writer 1–10000, max_crash_points and max_variants_per_point >= 1", dberr.ErrInvalidArgument)
	}
	if cfg.Dir == "" {
		return nil, fmt.Errorf("%w: empty dir", dberr.ErrInvalidArgument)
	}
	if err := isEmptyOrMissing(cfg.Dir); err != nil {
		return nil, err
	}
	rep := &Report{Tool: "nexus-powercut", Version: 1, Config: cfg, StartedAt: time.Now().UTC(),
		Model: ModelText, NotModeled: NotModeledText, Disclaimer: Disclaimer,
		ByVariant: map[string]int{}, Outcomes: map[string]int{}, Checks: map[string]int{}, Failures: []Failure{}, Controls: []Control{}}
	rep.Environment.GOOS, rep.Environment.GOARCH, rep.Environment.GoVersion, rep.Environment.CPUs = runtime.GOOS, runtime.GOARCH, runtime.Version(), runtime.NumCPU()
	rep.Reproduce = fmt.Sprintf("nexus-powercut --seed %d --writers %d --ops %d --max-points %d --max-variants %d", cfg.Seed, cfg.Writers, cfg.OpsPerW, cfg.MaxPoints, cfg.MaxVar)

	live := filepath.Join(cfg.Dir, "workload")
	if err := os.MkdirAll(live, 0o755); err != nil {
		return nil, err
	}
	ops, events, err := record(cfg, filepath.Join(live, DBName))
	if err != nil {
		return nil, err
	}
	rep.Trace.Events, rep.Trace.Ops = len(events), len(ops)
	rep.Trace.ByOp, rep.Trace.ByKind = map[Op]int{}, map[OpKind]int{}
	for _, e := range events {
		rep.Trace.ByOp[e.Op]++
		rep.Trace.BytesWritten += int64(e.Len)
		if e.Op == OpWrite && e.Len >= format.FrameHeaderSize && isFrameStart(e.Data) {
			rep.Trace.Frames++
		}
	}
	for _, o := range ops {
		rep.Trace.ByKind[o.Kind]++
		if o.Ack >= 0 {
			rep.Trace.Acked++
		}
	}

	points := choosePoints(events, cfg.MaxPoints, rand.New(rand.NewSource(cfg.Seed)))
	rep.CrashSpace, rep.CrashPoints = len(events)+1, len(points)
	rng := rand.New(rand.NewSource(cfg.Seed ^ 0x5eed))
	imgRoot := filepath.Join(cfg.Dir, "images")
	d := newDisk()
	pi := 0
	var prevSynced map[int][]byte // durable contents before the latest sync, per inode (controls)
	controlsDone := map[string]bool{}
	for k := 0; k <= len(events); k++ {
		if pi < len(points) && points[pi] == k {
			for _, img := range d.images(k, rng, cfg.MaxVar) {
				rep.Images++
				rep.ByVariant[family(img.Variant)]++
				f := validate(img, ops, imgRoot, rep, cfg.KeepPassed)
				if f != nil {
					rep.Failures = append(rep.Failures, *f)
					rep.Failed++
				} else {
					rep.Passed++
				}
			}
			pi++
			if progress != nil {
				progress(pi, len(points), rep.Failed)
			}
			if cfg.Controls {
				runControls(d, k, ops, prevSynced, imgRoot, rep, controlsDone)
			}
		}
		if k < len(events) {
			e := events[k]
			if e.Op == OpSync {
				if prevSynced == nil {
					prevSynced = map[int][]byte{}
				}
				prevSynced[e.Inode] = append([]byte(nil), d.inodes[e.Inode].durable...)
			}
			d.apply(k, e)
		}
	}
	switch {
	case rep.Failed > 0:
		rep.Verdict = fmt.Sprintf("FAIL: %d of %d modeled images were not recovered correctly", rep.Failed, rep.Images)
	default:
		rep.Verdict = fmt.Sprintf("PASS: all %d modeled images from %d crash points recovered correctly (%s)", rep.Images, rep.CrashPoints, Disclaimer)
	}
	rep.FinishedAt = time.Now().UTC()
	rep.DurationMS = float64(rep.FinishedAt.Sub(rep.StartedAt).Microseconds()) / 1000
	return rep, nil
}

func isFrameStart(b []byte) bool {
	_, err := format.DecodeFrameHeader(b)
	return err == nil
}

func family(v string) string {
	if i := strings.IndexAny(v, "-0123456789"); i > 0 {
		for _, p := range []string{"prefix", "random-subset", "dir-prefix"} {
			if strings.HasPrefix(v, p) {
				return p
			}
		}
	}
	return v
}

// record runs the workload on the real engine through a Recorder.
func record(cfg Config, path string) ([]WorkOp, []Event, error) {
	rec := NewRecorder()
	l, err := storage.OpenWith(rec, path, storage.Config{Recovery: recovery.DefaultOptions, AutoCompact: true})
	if err != nil {
		return nil, nil, err
	}
	var mu sync.Mutex
	var ops []WorkOp
	var wg sync.WaitGroup
	errc := make(chan error, cfg.Writers)
	for w := 0; w < cfg.Writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(cfg.Seed*1000 + int64(w)))
			var mine []string // unique keys this writer PUT and has not deleted
			for i := 0; i < cfg.OpsPerW; i++ {
				op := WorkOp{Writer: w, Seq: i, Ack: -1}
				var entries []format.Entry
				r := rng.Intn(100)
				switch {
				case r < 5 && w == 0 && i > 0:
					op.Kind = KCompact
				case r < 15 && len(mine) > 0:
					j := rng.Intn(len(mine))
					op.Kind, op.Keys = KDelete, []string{mine[j]}
					mine = append(mine[:j], mine[j+1:]...)
					entries = []format.Entry{{Op: format.OpDelete, Key: []byte(op.Keys[0])}}
				case r < 35:
					op.Kind = KHot
					k, v := fmt.Sprintf("hot-w%d", w), fmt.Sprintf("h-w%d-%d", w, i)
					op.Keys, op.Values = []string{k}, []string{v}
					entries = []format.Entry{{Op: format.OpPut, Key: []byte(k), Value: []byte(v)}}
				case r < 55:
					op.Kind = KBatch
					n := 2 + rng.Intn(4)
					for j := 0; j < n; j++ {
						k, v := fmt.Sprintf("b-w%d-%d-%d", w, i, j), value(rng, w, i, j)
						op.Keys, op.Values = append(op.Keys, k), append(op.Values, v)
						entries = append(entries, format.Entry{Op: format.OpPut, Key: []byte(k), Value: []byte(v)})
					}
				default:
					op.Kind = KPut
					k, v := fmt.Sprintf("u-w%d-%d", w, i), value(rng, w, i, 0)
					op.Keys, op.Values = []string{k}, []string{v}
					entries = []format.Entry{{Op: format.OpPut, Key: []byte(k), Value: []byte(v)}}
				}
				op.Issue = rec.Mark()
				var err error
				if op.Kind == KCompact {
					err = l.Compact()
				} else {
					err = l.Commit(entries)
				}
				if err == nil {
					op.Ack = rec.Mark()
					if op.Kind == KPut {
						mine = append(mine, op.Keys[0])
					}
				} else {
					op.Err = err.Error()
					if op.Kind != KCompact {
						errc <- fmt.Errorf("writer %d op %d: %w", w, i, err)
						return
					}
				}
				mu.Lock()
				op.ID = len(ops)
				ops = append(ops, op)
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	close(errc)
	cerr := l.Close()
	for err := range errc {
		return nil, nil, err
	}
	if cerr != nil {
		return nil, nil, cerr
	}
	return ops, rec.Events(), nil
}

// value returns a value whose size varies from a few bytes to several
// sectors, so frames cross 512-byte boundaries in many ways.
func value(rng *rand.Rand, w, i, j int) string {
	sizes := []int{8, 100, 500, 513, 1500, 4000}
	n := sizes[rng.Intn(len(sizes))]
	p := fmt.Sprintf("v-w%d-%d-%d-", w, i, j)
	b := make([]byte, n)
	for k := range b {
		b[k] = p[k%len(p)]
	}
	return string(b)
}

// choosePoints picks crash points: every point around a sync, directory
// sync, move or remove first (the moments durability changes), then a
// seeded sample of the rest, up to max.
func choosePoints(events []Event, max int, rng *rand.Rand) []int {
	n := len(events) + 1
	sel := map[int]bool{0: true, n - 1: true}
	for i, e := range events {
		switch e.Op {
		case OpSync, OpSyncDir, OpMove, OpRemove, OpCreate:
			sel[i] = true
			sel[i+1] = true
		}
	}
	var keys []int
	for k := range sel {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	if len(keys) > max {
		rng.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
		keys = keys[:max]
	} else {
		perm := rng.Perm(n)
		for _, k := range perm {
			if len(keys) >= max {
				break
			}
			if !sel[k] {
				sel[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Ints(keys)
	return keys
}

func materialize(dir string, files map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// validate opens one image with the real engine and checks it against the
// oracle. It returns nil when the image passed.
func validate(img Image, ops []WorkOp, root string, rep *Report, keep bool) *Failure {
	id := fmt.Sprintf("p%05d-%s", img.Point, img.Variant)
	dir := filepath.Join(root, id)
	if err := materialize(dir, img.Files); err != nil {
		return &Failure{Image: id, Point: img.Point, Variant: img.Variant, Kind: "harness-error", Detail: err.Error()}
	}
	fail := func(kind, detail string) *Failure {
		f := &Failure{Image: id, Point: img.Point, Variant: img.Variant, Kind: kind, Detail: detail}
		// Keep the pre-recovery image and diagnose it with X-RAY.
		pre := filepath.Join(root, "failures", id)
		if err := materialize(pre, img.Files); err == nil {
			f.Dir = pre
			f.XRay = diagnose(filepath.Join(pre, DBName))
		}
		return f
	}
	defer func() {
		if !keep {
			os.RemoveAll(dir)
		}
	}()
	db, err := kvdb.Open(filepath.Join(dir, DBName), 0)
	if err != nil {
		switch {
		case errors.Is(err, dberr.ErrCorruption):
			rep.Outcomes["refused-corruption"]++
		case errors.Is(err, dberr.ErrBadFormat):
			rep.Outcomes["refused-bad-format"]++
		default:
			rep.Outcomes["open-error"]++
		}
		return fail("recovery-refused", err.Error())
	}
	defer db.Close()
	ri := db.Recovery()
	switch {
	case ri.Created:
		rep.Outcomes["opened-new-empty-database"]++
	case ri.Tail != "":
		rep.Outcomes["opened-torn-tail-truncated-"+ri.Tail]++
	case len(ri.Rules) > 1:
		rep.Outcomes["opened-authority-rule-"+ri.Rules[0]]++
	default:
		rep.Outcomes["opened-clean"]++
	}
	if _, err := db.Verify(); err != nil {
		return fail("verify-failed", err.Error())
	}
	rep.Checks["verify"]++
	got := map[string]string{}
	if err := db.Range(func(k, v []byte) error { got[string(k)] = string(v); return nil }); err != nil {
		return fail("range-failed", err.Error())
	}
	if kind, detail := check(img.Point, ops, got, rep.Checks); kind != "" {
		return fail(kind, detail)
	}
	return nil
}

// check is the oracle. point: events [0, point) happened.
func check(point int, ops []WorkOp, got map[string]string, counts map[string]int) (string, string) {
	acked := func(o WorkOp) bool { return o.Ack >= 0 && o.Ack <= point }
	issued := func(o WorkOp) bool { return o.Issue < point }
	known := map[string]map[string]WorkOp{} // key -> value -> op that wrote it
	deleted := map[string]WorkOp{}
	lastHot := map[int]WorkOp{} // writer -> latest acked hot op
	for _, o := range ops {
		for i, k := range o.Keys {
			if o.Kind == KDelete {
				deleted[k] = o
				continue
			}
			if known[k] == nil {
				known[k] = map[string]WorkOp{}
			}
			known[k][o.Values[i]] = o
		}
		if o.Kind == KHot && acked(o) {
			if p, ok := lastHot[o.Writer]; !ok || o.Seq > p.Seq {
				lastHot[o.Writer] = o
			}
		}
	}
	// 1. No phantom or foreign data: every key/value present was written by
	// an operation issued before the crash.
	for k, v := range got {
		o, ok := known[k][v]
		if !ok {
			return "value-never-written", fmt.Sprintf("key %q holds a value no operation wrote (%d bytes)", k, len(v))
		}
		if !issued(o) {
			return "value-from-the-future", fmt.Sprintf("key %q holds the value of op %d issued at event %d, after the crash point", k, o.ID, o.Issue)
		}
	}
	counts["no-phantom-values"]++
	for _, o := range ops {
		switch o.Kind {
		case KPut:
			k := o.Keys[0]
			d, hasDel := deleted[k]
			_, present := got[k]
			if hasDel && acked(d) && present {
				return "acknowledged-delete-lost", fmt.Sprintf("key %q deleted by op %d (acked at event %d) is present", k, d.ID, d.Ack)
			}
			if acked(o) && !present && !(hasDel && issued(d)) {
				return "acknowledged-write-lost", fmt.Sprintf("key %q of op %d (acked at event %d) is missing", k, o.ID, o.Ack)
			}
			counts["durability-put"]++
		case KBatch:
			n := 0
			for _, k := range o.Keys {
				if _, ok := got[k]; ok {
					n++
				}
			}
			if n != 0 && n != len(o.Keys) {
				return "batch-not-atomic", fmt.Sprintf("op %d: %d of %d batch keys present", o.ID, n, len(o.Keys))
			}
			if acked(o) && n == 0 {
				return "acknowledged-write-lost", fmt.Sprintf("batch op %d (acked at event %d) is missing", o.ID, o.Ack)
			}
			counts["batch-atomicity"]++
		}
	}
	for w, last := range lastHot {
		v, ok := got[last.Keys[0]]
		if !ok {
			return "acknowledged-write-lost", fmt.Sprintf("hot key of writer %d missing; op %d was acked at event %d", w, last.ID, last.Ack)
		}
		if o := known[last.Keys[0]][v]; o.Seq < last.Seq {
			return "stale-value", fmt.Sprintf("hot key of writer %d holds op %d's value, older than acked op %d", w, o.ID, last.ID)
		}
		counts["ordering-overwrite"]++
	}
	return "", ""
}

func diagnose(path string) *XRaySummary {
	r, err := xray.Scan(path, xray.Options{})
	if err != nil {
		return nil
	}
	s := &XRaySummary{Health: r.Health, Engine: r.Engine.Outcome}
	for _, f := range r.Findings {
		at := ""
		if f.Offset != nil {
			at = fmt.Sprintf(" @%d", *f.Offset)
		}
		s.Findings = append(s.Findings, fmt.Sprintf("%s %s%s: %s", f.Severity, f.Code, at, f.Title))
	}
	if b, err := jsonMarshal(r); err == nil {
		p := filepath.Join(filepath.Dir(path), "xray.json")
		if os.WriteFile(p, b, 0o644) == nil {
			s.Report = p
		}
	}
	return s
}
