// Command nexus-powercut runs the NEXUS Power-Cut Explorer
// (docs/POWER_CUT_EXPLORER.md): it records the real engine's writes and
// syncs, generates modeled power-loss images, and validates each one with
// the real recovery. The result is a finite, seeded sample, not an
// exhaustive proof.
//
//	nexus-powercut [--seed N] [--writers N] [--ops N] [--max-points N]
//	               [--max-variants N] [--dir DIR] [--json FILE]
//	               [--keep] [--no-controls] [--progress]
//
// Exit status: 0 every image recovered correctly, 1 failures (or a negative
// control was not detected), 2 usage or harness error.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/powercut"
)

func main() { os.Exit(run()) }

func run() int {
	cfg := powercut.DefaultConfig
	fs := flag.NewFlagSet("nexus-powercut", flag.ContinueOnError)
	fs.Int64Var(&cfg.Seed, "seed", cfg.Seed, "seed for workload choices, crash-point sampling and image variants")
	fs.IntVar(&cfg.Writers, "writers", cfg.Writers, "concurrent writers (1-16)")
	fs.IntVar(&cfg.OpsPerW, "ops", cfg.OpsPerW, "operations per writer")
	fs.IntVar(&cfg.MaxPoints, "max-points", cfg.MaxPoints, "maximum crash points to test")
	fs.IntVar(&cfg.MaxVar, "max-variants", cfg.MaxVar, "maximum images per crash point")
	fs.StringVar(&cfg.Dir, "dir", "", "work directory (must not exist or be empty; default powercut-runs/seed-N-TIME)")
	jsonOut := fs.String("json", "", "write the JSON report to this file ('-' for stdout)")
	fs.BoolVar(&cfg.KeepPassed, "keep", false, "keep every image, not only failures")
	noControls := fs.Bool("no-controls", false, "skip the negative controls")
	progress := fs.Bool("progress", false, "print one JSON progress line per crash point on stderr")
	if err := fs.Parse(os.Args[1:]); err != nil || fs.NArg() != 0 {
		return 2
	}
	cfg.Controls = !*noControls
	if cfg.Dir == "" {
		cfg.Dir = filepath.Join("powercut-runs", fmt.Sprintf("seed-%d-%s", cfg.Seed, time.Now().UTC().Format("20060102T150405Z")))
	}
	var prog func(done, total, failed int)
	if *progress {
		prog = func(done, total, failed int) {
			fmt.Fprintf(os.Stderr, "{\"progress\":{\"crash_points_done\":%d,\"crash_points_total\":%d,\"failed\":%d}}\n", done, total, failed)
		}
	}
	rep, err := powercut.Run(cfg, prog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nexus-powercut:", err)
		return 2
	}
	b, _ := json.MarshalIndent(rep, "", "  ")
	reportPath := filepath.Join(cfg.Dir, "report.json")
	if err := os.WriteFile(reportPath, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "nexus-powercut: writing report:", err)
		return 2
	}
	switch *jsonOut {
	case "":
	case "-":
		os.Stdout.Write(append(b, '\n'))
	default:
		if err := os.WriteFile(*jsonOut, b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "nexus-powercut:", err)
			return 2
		}
	}
	if *jsonOut != "-" {
		summary(rep, reportPath)
	}
	undetected := 0
	for _, c := range rep.Controls {
		if !c.Detected {
			undetected++
		}
	}
	if rep.Failed > 0 || undetected > 0 {
		return 1
	}
	return 0
}

func summary(r *powercut.Report, path string) {
	fmt.Printf("NEXUS Power-Cut Explorer  seed=%d writers=%d ops/writer=%d\n", r.Config.Seed, r.Config.Writers, r.Config.OpsPerW)
	fmt.Printf("trace: %d events (%d writes, %d syncs, %d dir syncs, %d creates, %d moves, %d removes, %d truncates), %d bytes, %d frames, %d/%d ops acknowledged\n",
		r.Trace.Events, r.Trace.ByOp[powercut.OpWrite], r.Trace.ByOp[powercut.OpSync], r.Trace.ByOp[powercut.OpSyncDir],
		r.Trace.ByOp[powercut.OpCreate], r.Trace.ByOp[powercut.OpMove], r.Trace.ByOp[powercut.OpRemove], r.Trace.ByOp[powercut.OpTruncate],
		r.Trace.BytesWritten, r.Trace.Frames, r.Trace.Acked, r.Trace.Ops)
	fmt.Printf("tested: %d of %d crash points, %d modeled images\n", r.CrashPoints, r.CrashSpace, r.Images)
	keys := make([]string, 0, len(r.Outcomes))
	for k := range r.Outcomes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %-40s %d\n", k, r.Outcomes[k])
	}
	for _, f := range r.Failures {
		fmt.Printf("FAILURE %s: %s: %s\n", f.Image, f.Kind, f.Detail)
		if f.XRay != nil {
			for _, s := range f.XRay.Findings {
				fmt.Printf("    x-ray: %s\n", s)
			}
		}
	}
	det := 0
	for _, c := range r.Controls {
		if c.Detected {
			det++
		} else {
			fmt.Printf("CONTROL NOT DETECTED: %s at point %d (%s)\n", c.Name, c.Point, c.Model)
		}
	}
	fmt.Printf("negative controls (outside the model): %d of %d detected\n", det, len(r.Controls))
	fmt.Println(r.Verdict)
	fmt.Printf("report: %s  (%.1f s)\nreproduce: %s\n", path, r.DurationMS/1000, r.Reproduce)
}
