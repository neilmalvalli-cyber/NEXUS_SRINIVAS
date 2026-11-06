package main

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Recorded validation results (docs/results/final by default), served
// read-only. Every response names the file it came from and its time, so
// the dashboard can label recorded numbers as recorded, never as live.

type evidenceFile struct {
	Name     string `json:"name"` // slash-separated, relative to the evidence directory
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

var evidenceExt = map[string]string{
	".json": "application/json; charset=utf-8",
	".md":   "text/markdown; charset=utf-8",
	".log":  "text/plain; charset=utf-8",
	".txt":  "text/plain; charset=utf-8",
}

// listEvidence walks the evidence directory two levels deep and returns the
// files with a served extension.
func (s *Server) listEvidence() []evidenceFile {
	root := s.cfg.EvidenceDir
	out := []evidenceFile{}
	if root == "" {
		return out
	}
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		if d.IsDir() {
			if strings.Count(filepath.ToSlash(rel), "/") >= 2 {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := evidenceExt[strings.ToLower(filepath.Ext(p))]; !ok {
			return nil
		}
		if st, err := d.Info(); err == nil {
			out = append(out, evidenceFile{filepath.ToSlash(rel), st.Size(), st.ModTime().UTC().Format(time.RFC3339)})
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Server) handleEvidence(w http.ResponseWriter, r *http.Request) {
	abs, _ := filepath.Abs(s.cfg.EvidenceDir)
	writeJSON(w, http.StatusOK, map[string]any{"directory": abs, "files": s.listEvidence()})
}

// handleEvidenceFile serves one file, only if it is in the listing (no path
// traversal: the name must match a listed entry exactly).
func (s *Server) handleEvidenceFile(w http.ResponseWriter, r *http.Request) {
	name := path.Clean("/" + r.PathValue("name"))[1:]
	for _, f := range s.listEvidence() {
		if f.Name != name {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.cfg.EvidenceDir, filepath.FromSlash(f.Name)))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "io", err.Error(), false)
			return
		}
		w.Header().Set("Content-Type", evidenceExt[strings.ToLower(filepath.Ext(f.Name))])
		w.Header().Set("X-Evidence-Modified", f.Modified)
		w.Write(b)
		return
	}
	writeError(w, http.StatusNotFound, "not-found", "no such evidence file", false)
}

// handleBenchmark returns the recorded kvdb-bench result (bench.json).
func (s *Server) handleBenchmark(w http.ResponseWriter, r *http.Request) {
	p := filepath.Join(s.cfg.EvidenceDir, "bench.json")
	raw := rawJSONFile(p)
	if raw == nil {
		writeError(w, http.StatusNotFound, "not-found", "no recorded benchmark (bench.json) in the evidence directory; run kvdb-bench --json", false)
		return
	}
	st, _ := os.Stat(p)
	writeJSON(w, http.StatusOK, map[string]any{
		"kind":     "recorded",
		"file":     "bench.json",
		"modified": st.ModTime().UTC().Format(time.RFC3339),
		"result":   raw,
	})
}
