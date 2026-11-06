// Command nexus-torture is NEXUS Torture Engine 2.0: a first-party crash
// tester that launches the real kvdb executable, drives it, kills it, restarts
// it and judges every recovered state with an independent oracle.
//
//	nexus-torture run       --mode default --seed 12345 --iterations 500
//	nexus-torture run       --mode default --seed 12345 --iteration 17 --from-db <evidence>/pre/db.kv
//	nexus-torture scenarios [--group A] [--only A1,B6] [--full]
//	nexus-torture mutants   --mutant ./kvdb-mutant.exe [--iterations 40]
//	nexus-torture modes
//
// Exit status: 0 PASS, 1 FAIL, 2 usage or tool error, 3 INCONCLUSIVE.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/engine"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/mutation"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/oracle"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/proc"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/scenario"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/workload"
)

const usage = `nexus-torture: NEXUS Torture Engine 2.0

usage:
  nexus-torture run --seed N [--mode M] [--iterations N | --iteration I] [options]
      randomized kill/recover/verify loop against the real kvdb executable
  nexus-torture scenarios [--group A..F] [--only IDS] [--full] [options]
      the 32 official crash scenarios (A1-F4), each with its own invariant
  nexus-torture mutants --mutant PATH [--iterations N] [options]
      prove the tester catches deliberately broken builds (kvdb-mutant)
  nexus-torture modes
      list the workload modes

common options:
  --kvdb PATH        kvdb executable (default: kvdb next to nexus-torture, then ./kvdb)
  --dir DIR          work directory (default ./torture-runs/v2/<command>-...)

run options:
  --mode M           workload mode (see "modes"; default: default)
  --seed N           mandatory; the same seed and iteration give the same requests
  --iterations N     number of iterations (default 100)
  --iteration I      run only iteration I (reproduce a failure); implies --iterations 1
  --from-db FILE     start from this database (the "pre" copy kept with a failure)
  --keep-going       continue after a failing iteration (default: stop)
  --reply-timeout D  max wait for one reply while serve runs (default 60s)
  --oracle-steps N   oracle search budget per iteration (default 2e9)
  --quiet            print only failures and the summary

exit status: 0 PASS, 1 FAIL, 2 usage/tool error, 3 INCONCLUSIVE
`

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Print(usage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:])
	case "scenarios":
		return cmdScenarios(args[1:])
	case "mutants":
		return cmdMutants(args[1:])
	case "modes":
		for _, m := range workload.Modes() {
			fmt.Printf("%-20s %s\n", m.Name, m.Description)
		}
		return 0
	}
	fmt.Fprintf(os.Stderr, "nexus-torture: unknown command %q\n\n%s", args[0], usage)
	return 2
}

func exitFor(result string) int {
	switch result {
	case "PASS":
		return 0
	case "FAIL":
		return 1
	case "INCONCLUSIVE":
		return 3
	}
	return 2
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	mode := fs.String("mode", "default", "workload mode")
	seed := fs.String("seed", "", "random seed (mandatory)")
	iters := fs.Int("iterations", 100, "iterations")
	one := fs.Int("iteration", 0, "run only this iteration")
	kv := fs.String("kvdb", "", "kvdb executable")
	dir := fs.String("dir", "", "work directory")
	from := fs.String("from-db", "", "start from this database file")
	keep := fs.Bool("keep-going", false, "continue after a failure")
	rt := fs.Duration("reply-timeout", 60*time.Second, "reply timeout")
	steps := fs.Int64("oracle-steps", 0, "oracle step budget")
	quiet := fs.Bool("quiet", false, "quiet")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *seed == "" {
		fmt.Fprintln(os.Stderr, "nexus-torture run: --seed is mandatory (for example --seed 12345)")
		return 2
	}
	var s int64
	if _, err := fmt.Sscan(*seed, &s); err != nil {
		fmt.Fprintf(os.Stderr, "nexus-torture run: --seed %q is not an integer\n", *seed)
		return 2
	}
	m, err := workload.ByName(*mode)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nexus-torture run:", err)
		return 2
	}
	b, err := proc.Locate(*kv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nexus-torture run:", err)
		return 2
	}
	b.Env = envFromOS()
	first := 1
	if *one > 0 {
		first, *iters = *one, 1
	}
	if *dir == "" {
		*dir = filepath.Join("torture-runs", "v2", fmt.Sprintf("%s-seed%d", m.Name, s))
		if *one > 0 {
			*dir += fmt.Sprintf("-iter%d", *one)
		}
	}
	lim := oracle.DefaultLimits()
	if *steps > 0 {
		lim.MaxSteps = *steps
	}
	sum, err := engine.Run(engine.Config{Mode: m, Seed: s, FirstIter: first, Iterations: *iters, Kvdb: b, Dir: *dir,
		FromDB: *from, ReplyTimeout: *rt, Limits: lim, Quiet: *quiet, StopOnFail: !*keep})
	if err != nil {
		fmt.Fprintln(os.Stderr, "nexus-torture run: tool error (not a verdict on NEXUS):", err)
		return 2
	}
	return exitFor(sum.Result)
}

// envFromOS passes KVDB_MUTANT through to the executable under test, so a
// mutant build can be driven by `run` too.
func envFromOS() []string {
	if v := os.Getenv("KVDB_MUTANT"); v != "" {
		return []string{"KVDB_MUTANT=" + v}
	}
	return nil
}

func cmdScenarios(args []string) int {
	fs := flag.NewFlagSet("scenarios", flag.ContinueOnError)
	kv := fs.String("kvdb", "", "kvdb executable")
	dir := fs.String("dir", filepath.Join("torture-runs", "v2", "scenarios"), "work directory")
	group := fs.String("group", "", "only this group (A-F)")
	only := fs.String("only", "", "comma-separated scenario IDs")
	full := fs.Bool("full", false, "full sizes (500 kills for F1, 1,000,000 keys for B6/F3, more iterations)")
	seed := fs.Int64("seed", 20261106, "seed for the randomized parts")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	b, err := proc.Locate(*kv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nexus-torture scenarios:", err)
		return 2
	}
	var ids []string
	if *only != "" {
		ids = strings.Split(*only, ",")
	}
	rep, err := scenario.RunAll(scenario.Options{Kvdb: b, Dir: *dir, Group: strings.ToUpper(*group), Only: ids, Full: *full, Seed: *seed, Out: os.Stdout})
	if err != nil {
		fmt.Fprintln(os.Stderr, "nexus-torture scenarios: tool error:", err)
		return 2
	}
	return exitFor(rep.Result)
}

func cmdMutants(args []string) int {
	fs := flag.NewFlagSet("mutants", flag.ContinueOnError)
	mut := fs.String("mutant", "", "kvdb-mutant executable (go build -o . ./torture2/mutants/kvdb-mutant)")
	dir := fs.String("dir", filepath.Join("torture-runs", "v2", "mutants"), "work directory")
	iters := fs.Int("iterations", 40, "max iterations per mutation")
	seed := fs.Int64("seed", 777, "seed")
	only := fs.String("only", "", "comma-separated mutation names")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *mut == "" {
		fmt.Fprintln(os.Stderr, "nexus-torture mutants: --mutant is required")
		return 2
	}
	var names []string
	if *only != "" {
		names = strings.Split(*only, ",")
	}
	rep, err := mutation.Run(mutation.Options{Mutant: *mut, Dir: *dir, Iterations: *iters, Seed: *seed, Only: names, Out: os.Stdout})
	if err != nil {
		fmt.Fprintln(os.Stderr, "nexus-torture mutants: tool error:", err)
		return 2
	}
	if rep.AllDetected {
		return 0
	}
	return 1
}
