package main

// Database selection, DUMP and the read-only log map.
//
// The adapter serves one open database at a time. POST /api/databases/open
// switches to (or creates) another database file in the SAME directory as
// --db; names are validated, so a request can never reach another path.
// Every handler that touches the open database holds s.dbMu for reading (see
// ServeHTTP), and a switch holds it for writing, so no request ever sees a
// closed handle in the middle of a switch.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/kvdb"
)

var dbNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,47}$`)

// dbFileName turns a user-supplied name into a file name inside the data
// directory, or "" if the name is not acceptable.
func dbFileName(name string) string {
	name = strings.TrimSuffix(name, ".kv")
	if !dbNameRe.MatchString(name) {
		return ""
	}
	return name + ".kv"
}

func (s *Server) dataDir() string { return filepath.Dir(s.dbAbs) }

type dbFile struct {
	Name     string `json:"name"`
	File     string `json:"file"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
	Current  bool   `json:"current"`
}

func (s *Server) handleDatabases(w http.ResponseWriter, r *http.Request) {
	ents, err := os.ReadDir(s.dataDir())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "io", err.Error(), false)
		return
	}
	cur := filepath.Base(s.dbAbs)
	files := []dbFile{}
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".kv") || dbFileName(n) == "" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, dbFile{Name: strings.TrimSuffix(n, ".kv"), File: n, Size: info.Size(),
			Modified: info.ModTime().UTC().Format(time.RFC3339Nano), Current: strings.EqualFold(n, cur)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{
		"directory": s.dataDir(),
		"current":   strings.TrimSuffix(cur, ".kv"),
		"db_path":   s.dbAbs,
		"files":     files,
		"rule":      "names are 1-48 characters [A-Za-z0-9_-]; files live in the directory of --db and end in .kv",
	})
}

// handleOpenDatabase closes the current database and opens (or, with
// create=true, creates) another one in the data directory. If the new one
// cannot be opened, the previous database is reopened.
func (s *Server) handleOpenDatabase(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name   string `json:"name"`
		Create bool   `json:"create"`
	}
	if !decodeBody(w, r, 1<<10, &b) {
		return
	}
	file := dbFileName(b.Name)
	if file == "" {
		writeError(w, http.StatusBadRequest, "invalid", "name must be 1-48 characters of A-Z, a-z, 0-9, _ or - (no paths)", false)
		return
	}
	target := filepath.Join(s.dataDir(), file)
	exists := fileExists(target)
	if !exists && !b.Create {
		writeError(w, http.StatusNotFound, "notfound", "no database named "+strings.TrimSuffix(file, ".kv")+" (set create to make it)", false)
		return
	}
	if exists && b.Create {
		writeError(w, http.StatusConflict, "invalid", "a database named "+strings.TrimSuffix(file, ".kv")+" already exists; open it instead", false)
		return
	}

	s.dbMu.Lock()
	defer s.dbMu.Unlock()
	if strings.EqualFold(target, s.dbAbs) {
		writeError(w, http.StatusConflict, "invalid", "that database is already open", false)
		return
	}
	prev := s.dbAbs
	if err := s.db.Close(); err != nil {
		s.act.record("open", file, errClass(err), 0, "closing the previous database: "+err.Error())
		engineError(w, err, false)
		return
	}
	t0 := time.Now()
	db, err := kvdb.Open(target, s.cfg.MaxBytes)
	el := time.Since(t0)
	if err != nil {
		// Put the previous database back so the adapter keeps serving.
		old, rerr := kvdb.Open(prev, s.cfg.MaxBytes)
		if rerr != nil {
			s.act.record("open", file, errClass(err), el, err.Error())
			writeError(w, http.StatusInternalServerError, "failed",
				fmt.Sprintf("opening %s failed (%v) and reopening the previous database failed too (%v); restart nexus-api", file, err, rerr), false)
			return
		}
		s.setDB(old, prev, time.Now(), 0)
		s.act.record("open", file, errClass(err), el, err.Error())
		engineError(w, err, false)
		return
	}
	s.setDB(db, target, t0, el)
	verb := "opened"
	if !exists {
		verb = "created"
	}
	s.act.record("open", file, "ok", el, verb)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "action": verb, "name": strings.TrimSuffix(file, ".kv"), "db_path": target,
		"last_open": recoveryJSON(t0, el, db.Recovery()), "stats": s.stats(),
	})
}

// setDB installs a newly opened handle. The caller holds s.dbMu for writing.
func (s *Server) setDB(db *kvdb.DB, abs string, at time.Time, d time.Duration) {
	s.db, s.dbAbs, s.openAt, s.openDur, s.openRec = db, abs, at, d, db.Recovery()
	s.mu.Lock()
	s.lastCompact, s.verifies = nil, nil
	s.mu.Unlock()
}

// ---- DUMP ----

const maxDump = 5000

// handleDump returns every current record in key order (kvdb.Range), up to
// limit records, with values cut at 4 KiB for display.
func (s *Server) handleDump(w http.ResponseWriter, r *http.Request) {
	limit := intParam(r, "limit", 1000, 1, maxDump)
	q := strings.ToLower(r.URL.Query().Get("q"))
	t0 := time.Now()
	type rec struct {
		Key       encodedBytes `json:"key"`
		Value     encodedBytes `json:"value"`
		Size      int          `json:"size"`
		Truncated bool         `json:"value_truncated"`
	}
	items := []rec{}
	total, matched := 0, 0
	err := s.db.Range(func(k, v []byte) error {
		total++
		if q != "" && !strings.Contains(strings.ToLower(string(k)), q) {
			return nil
		}
		matched++
		if len(items) < limit {
			p, cut := preview(v, 4096)
			items = append(items, rec{encodeBytes(append([]byte(nil), k...)), p, len(v), cut})
		}
		return nil
	})
	if err != nil {
		engineError(w, err, false)
		return
	}
	el := time.Since(t0)
	s.act.record("dump", q, "ok", el, plural(len(items), "record"))
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "total_keys": total, "matched": matched, "returned": len(items),
		"truncated": matched > len(items), "elapsed_ms": ms(el),
		"method": "kvdb.Range over the in-memory index, reading each value from the log (the same data as `kvdb dump`)",
	})
}

// ---- read-only log map ----

type sbInfo struct {
	Magic       string `json:"magic"`
	Version     uint16 `json:"version"`
	FirstSeq    uint64 `json:"first_seq"`
	SnapshotEnd uint64 `json:"snapshot_end"`
	Valid       bool   `json:"valid"`
	Error       string `json:"error,omitempty"`
}

type entryInfo struct {
	Op       string       `json:"op"`
	Offset   uint64       `json:"offset"` // absolute file offset of the entry header
	Size     uint64       `json:"size"`
	KeyLen   uint16       `json:"key_len"`
	ValueLen uint32       `json:"value_len"`
	Key      encodedBytes `json:"key"`
	Value    encodedBytes `json:"value_preview"`
	Cut      bool         `json:"value_truncated"`
	CRC      uint32       `json:"entry_crc"`
}

type frameInfo struct {
	Offset     uint64      `json:"offset"`
	Size       uint64      `json:"size"`
	Seq        uint64      `json:"seq"`
	OpCount    uint16      `json:"op_count"`
	PayloadLen uint64      `json:"payload_len"`
	PayloadCRC uint32      `json:"payload_crc"`
	Snapshot   bool        `json:"in_snapshot"`
	Entries    []entryInfo `json:"entries"`
	More       int         `json:"more_entries"`
}

type logStop struct {
	Kind      string `json:"kind"` // clean, damaged, corrupt, io, sequence, limit
	Offset    uint64 `json:"offset"`
	Remaining uint64 `json:"bytes_after"`
	Message   string `json:"message,omitempty"`
}

type indexEntry struct {
	Key      encodedBytes `json:"key"`
	Offset   uint64       `json:"entry_offset"`
	Size     uint64       `json:"entry_size"`
	FrameSeq uint64       `json:"frame_seq"`
}

type logMap struct {
	Path        string       `json:"path"`
	FileSize    uint64       `json:"file_size"`
	Superblock  sbInfo       `json:"superblock"`
	Frames      []frameInfo  `json:"frames"`
	FirstFrame  int          `json:"first_listed_frame"` // index of Frames[0] among all frames scanned
	TotalFrames int          `json:"total_frames"`
	Entries     int          `json:"total_entries"`
	LastSeq     uint64       `json:"last_seq"`
	ValidEnd    uint64       `json:"valid_end"`
	Stop        logStop      `json:"stop"`
	Index       []indexEntry `json:"index_sample"`
	IndexKeys   int          `json:"index_keys"`
	ElapsedMs   float64      `json:"elapsed_ms"`
	Method      string       `json:"method"`
}

const (
	maxScanBytes   = 64 << 20
	maxFrameList   = 500
	entriesPerShow = 8
)

// scanLog reads a NEXUS data file with the engine's own decoders
// (internal/format) without modifying it. It lists the last `keep` frames,
// derives which entry each live key points to (what the index holds), and
// says where and why the scan stopped. It does not decide what recovery would
// do with damage: kvdb.CheckReadOnly does that.
func scanLog(path string, keep int) (*logMap, error) {
	t0 := time.Now()
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	m := &logMap{Path: path, FileSize: uint64(st.Size()), Frames: []frameInfo{},
		Method: "read-only scan with the engine's format decoders (DecodeSuperblock, DecodeFrameHeader, ScanPayload); nothing is written"}
	br := bufio.NewReaderSize(f, 1<<20)
	stop := func(kind string, off uint64, msg string) {
		m.Stop = logStop{Kind: kind, Offset: off, Remaining: m.FileSize - off, Message: msg}
	}
	var sbuf [format.SuperblockSize]byte
	if _, err := io.ReadFull(br, sbuf[:]); err != nil {
		m.Superblock.Error = "file shorter than the 32-byte superblock"
		stop("damaged", 0, m.Superblock.Error)
		m.ElapsedMs = ms(time.Since(t0))
		return m, nil
	}
	m.Superblock.Magic = strings.TrimRight(string(sbuf[:8]), "\x00")
	m.Superblock.Version = uint16(sbuf[8]) | uint16(sbuf[9])<<8
	sb, err := format.DecodeSuperblock(sbuf[:])
	if err != nil {
		m.Superblock.Error = err.Error()
		stop(format.Classify(err).String(), 0, "superblock: "+err.Error())
		m.ElapsedMs = ms(time.Since(t0))
		return m, nil
	}
	m.Superblock.Valid, m.Superblock.FirstSeq, m.Superblock.SnapshotEnd = true, sb.FirstSeq, sb.SnapshotEnd

	type loc struct{ off, size, seq uint64 }
	latest := map[string]loc{}
	pos := uint64(format.SuperblockSize)
	m.ValidEnd = pos
	ring := make([]frameInfo, 0, keep)
	want := sb.FirstSeq
	var hbuf [format.FrameHeaderSize]byte
	for {
		if pos == m.FileSize {
			stop("clean", pos, "the file ends exactly on a frame boundary")
			break
		}
		if pos > maxScanBytes {
			stop("limit", pos, fmt.Sprintf("scan stopped after %d MiB (display limit); the rest of the file was not read", maxScanBytes>>20))
			break
		}
		if !format.HeaderFits(pos, m.FileSize) {
			stop("damaged", pos, fmt.Sprintf("%d bytes after the last complete frame: less than a frame header (an incomplete write)", m.FileSize-pos))
			break
		}
		if _, err := io.ReadFull(br, hbuf[:]); err != nil {
			stop("io", pos, err.Error())
			break
		}
		h, err := format.DecodeFrameHeader(hbuf[:])
		if err != nil {
			stop(format.Classify(err).String(), pos, "frame header: "+err.Error())
			break
		}
		if h.Seq != want {
			stop("sequence", pos, fmt.Sprintf("frame seq %d where %d was expected", h.Seq, want))
			break
		}
		if !format.PayloadFits(pos, m.FileSize, h.PayloadLen) {
			stop("damaged", pos, fmt.Sprintf("frame %d declares a %d-byte payload but only %d bytes remain (an incomplete write)", h.Seq, h.PayloadLen, m.FileSize-pos-format.FrameHeaderSize))
			break
		}
		size := format.FrameHeaderSize + h.PayloadLen
		fi := frameInfo{Offset: pos, Size: size, Seq: h.Seq, OpCount: h.OpCount, PayloadLen: h.PayloadLen,
			PayloadCRC: h.PayloadCRC, Snapshot: pos < sb.SnapshotEnd, Entries: []entryInfo{}}
		type tentative struct {
			key string
			l   loc
			del bool
		}
		var tent []tentative
		err = format.ScanPayload(br, h, func(e format.EntryView) error {
			abs := pos + format.FrameHeaderSize + e.Offset
			tent = append(tent, tentative{string(e.Key), loc{abs, e.Header.Size(), h.Seq}, e.Header.Op == format.OpDelete})
			if len(fi.Entries) < entriesPerShow {
				kp, _ := preview(e.Key, 64)
				vp, cut := preview(e.Value, 48)
				fi.Entries = append(fi.Entries, entryInfo{Op: e.Header.Op.String(), Offset: abs, Size: e.Header.Size(),
					KeyLen: e.Header.KeyLen, ValueLen: e.Header.ValueLen, Key: kp, Value: vp, Cut: cut, CRC: e.Header.CRC})
			} else {
				fi.More++
			}
			return nil
		})
		if err != nil {
			stop(format.Classify(err).String(), pos, fmt.Sprintf("frame %d payload: %v", h.Seq, err))
			break
		}
		// The frame is valid as a whole: apply its entries (all-or-nothing).
		for _, t := range tent {
			if t.del {
				delete(latest, t.key)
			} else {
				latest[t.key] = t.l
			}
		}
		m.TotalFrames++
		m.Entries += int(h.OpCount)
		m.LastSeq = h.Seq
		if len(ring) == keep {
			copy(ring, ring[1:])
			ring = ring[:keep-1]
		}
		ring = append(ring, fi)
		pos += size
		m.ValidEnd = pos
		if next, err := format.NextSeq(h.Seq); err == nil {
			want = next
		}
	}
	m.Frames = ring
	m.FirstFrame = m.TotalFrames - len(ring)
	m.IndexKeys = len(latest)
	keys := make([]string, 0, len(latest))
	for k := range latest {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		if i == 50 {
			break
		}
		l := latest[k]
		kp, _ := preview([]byte(k), 64)
		m.Index = append(m.Index, indexEntry{Key: kp, Offset: l.off, Size: l.size, FrameSeq: l.seq})
	}
	m.ElapsedMs = ms(time.Since(t0))
	return m, nil
}

// handleFrames maps the open main database. Writes through the adapter are
// held off while the file is read, so the scan sees a consistent prefix.
func (s *Server) handleFrames(w http.ResponseWriter, r *http.Request) {
	keep := intParam(r, "limit", 100, 1, maxFrameList)
	s.writeMu.Lock()
	m, err := scanLog(s.dbAbs, keep)
	s.writeMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "io", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// handleDemoFrames maps the crash-demo database (closed between drill steps),
// which is where torn tails can be seen.
func (s *Server) handleDemoFrames(w http.ResponseWriter, r *http.Request) {
	keep := intParam(r, "limit", 100, 1, maxFrameList)
	d := s.demo
	d.mu.Lock()
	defer d.mu.Unlock()
	if !fileExists(d.path()) {
		writeError(w, http.StatusConflict, "invalid", "create the demo database first (reset)", false)
		return
	}
	m, err := scanLog(d.path(), keep)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "io", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// existed reports whether key is present, for PUT/DELETE results. The caller
// holds s.writeMu, and nexus-api is the only writer (the engine's lock file
// keeps other processes out), so the answer cannot change before the write.
func (s *Server) existed(key []byte) (bool, error) {
	_, err := s.db.Get(key)
	if errors.Is(err, kvdb.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}
