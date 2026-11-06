package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/xray"
)

var errXRayFindings = errors.New("x-ray found critical or error findings")

// cmdXRay is `kvdb xray <path> [--json] [--verbose] [--repair --yes
// [--backup-dir DIR]]`. Read-only unless --repair --yes is given.
//
// Exit status: 0 nothing needs action (INFO/WARNING only, nothing to
// repair); 4 a normal open would repair something (nothing changed);
// 1 ERROR or CRITICAL findings, or a repair that was refused or not
// verified.
func cmdXRay(a args, env Env) error {
	path := a.pos[0]
	var r *xray.Report
	var err error
	if a.repair {
		if !a.yes {
			return usagef("--repair modifies the database (after backing it up); add --yes to confirm")
		}
		dir := a.backupDir
		if dir == "" {
			dir = filepath.Join(filepath.Dir(path), "xray-backups")
		}
		r, err = xray.Repair(path, dir, xray.Options{})
	} else {
		if a.yes || a.backupDir != "" {
			return usagef("--yes and --backup-dir are only used with --repair")
		}
		r, err = xray.Scan(path, xray.Options{})
	}
	if err != nil {
		return err
	}
	if a.json {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(r); err != nil {
			return err
		}
	} else {
		RenderXRay(env.Stdout, r, a.verbose)
	}
	switch {
	case r.Repair != nil && !r.Repair.Verified:
		return errNeedsAttention(r)
	case r.Counts[xray.Critical] > 0 || r.Counts[xray.Error] > 0:
		return errNeedsAttention(r)
	case r.Repair == nil && len(r.Engine.Repairs) > 0:
		return errNeedsRepair
	}
	return nil
}

// errNeedsAttention is reported as exit 1 without a second error line: the
// report already said everything.
func errNeedsAttention(r *xray.Report) error { return quietError{errXRayFindings} }

type quietError struct{ error }

// RenderXRay prints a report for people. Default: the simple explanation of
// each finding. verbose adds every technical field.
func RenderXRay(w io.Writer, r *xray.Report, verbose bool) {
	fmt.Fprintf(w, "NEXUS X-RAY  %s\n", r.Path)
	fmt.Fprintf(w, "mode: %s   health: %s   engine: %s   findings: %d critical, %d error, %d warning, %d info\n",
		r.Mode, strings.ToUpper(r.Health), r.Engine.Outcome, r.Counts[xray.Critical], r.Counts[xray.Error], r.Counts[xray.Warning], r.Counts[xray.Info])
	if r.Walk != nil {
		fmt.Fprintf(w, "file: %s, %d bytes; %d valid frames, %d operations; valid up to byte %d\n",
			r.Walk.File, r.Walk.Size, r.Walk.Frames, r.Walk.Entries, r.Walk.ValidEnd)
	}
	rc := r.Recovery
	fmt.Fprintf(w, "recovery: %s; open would succeed: %v; possible data loss: %s\n", rc.Status, rc.OpenSucceeds, rc.DataLoss)
	if rc.ScanStoppedAt != nil {
		fmt.Fprintf(w, "          scan stopped at byte %d (frame %d)\n", *rc.ScanStoppedAt, deref(rc.FailedFrame))
	}
	if rc.WouldTruncateBytes != nil {
		fmt.Fprintf(w, "          a normal open would cut %d bytes\n", *rc.WouldTruncateBytes)
	}
	fmt.Fprintln(w)
	for i, f := range r.Findings {
		fmt.Fprintf(w, "%d. [%s] %s  (%s, %s)\n", i+1, f.Severity, f.Title, f.Category, f.Code)
		fmt.Fprintf(w, "   %s\n", f.Simple)
		loc := []string{}
		if f.Offset != nil {
			loc = append(loc, fmt.Sprintf("byte %d", *f.Offset))
		}
		if f.Frame != nil {
			loc = append(loc, fmt.Sprintf("frame %d", *f.Frame))
		}
		if f.Seq != nil {
			loc = append(loc, fmt.Sprintf("seq %d", *f.Seq))
		}
		if f.Rule != "" {
			loc = append(loc, "rule "+f.Rule)
		}
		if len(loc) > 0 {
			fmt.Fprintf(w, "   where: %s\n", strings.Join(loc, ", "))
		}
		fmt.Fprintf(w, "   impact: %s\n   recovery: %s\n   action: %s\n", f.DataImpact, f.Recovery, f.Action)
		if verbose {
			fmt.Fprintf(w, "   technical: %s\n", f.Technical)
			if f.Expected != "" || f.Actual != "" {
				fmt.Fprintf(w, "   expected: %s\n   actual:   %s\n", f.Expected, f.Actual)
			}
			fmt.Fprintf(w, "   file: %s\n   component: %s\n   id: %s\n   time: %s\n", f.File, f.Component, f.ID, f.Timestamp.Format("2006-01-02T15:04:05.000Z07:00"))
			if f.OriginalError != "" {
				fmt.Fprintf(w, "   original error: %s\n", f.OriginalError)
			}
		}
		fmt.Fprintln(w)
	}
	if len(r.Engine.Repairs) > 0 && r.Repair == nil {
		fmt.Fprintln(w, "a normal open would:")
		for _, s := range r.Engine.Repairs {
			fmt.Fprintf(w, "  - %s\n", s)
		}
		fmt.Fprintln(w, "run `kvdb xray <path> --repair --yes` to back up and apply this, then re-verify.")
	}
	if rp := r.Repair; rp != nil {
		fmt.Fprintf(w, "repair: verified=%v\n", rp.Verified)
		for _, b := range rp.Backups {
			fmt.Fprintf(w, "  backup: %s\n", b)
		}
		for _, s := range rp.Applied {
			fmt.Fprintf(w, "  applied: %s\n", s)
		}
		if rp.OpenError != "" {
			fmt.Fprintf(w, "  %s\n", rp.OpenError)
		}
		if rp.VerifyError != "" {
			fmt.Fprintf(w, "  verify: %s\n", rp.VerifyError)
		}
		if rp.After != nil {
			fmt.Fprintf(w, "  after: health %s, engine %s\n", rp.After.Health, rp.After.Outcome)
		}
	}
	if !r.Modified {
		fmt.Fprintln(w, "file not modified (fingerprint before = after)")
	}
}

func deref(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}
