// Command kvdb is the CLI for the engine.
//
// Phase 1 status: this is a placeholder so the build can be verified on
// Windows. Every command reports "not implemented" and exits with status 2.
// The real commands (open, put, get, del, batch, dump, check, serve) are
// implemented in a later phase.
package main

import (
	"fmt"
	"os"
)

var commands = []string{"open", "put", "get", "del", "batch", "dump", "check", "serve"}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: kvdb <command> [args]\ncommands: %v\n", commands)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "kvdb: command %q is not implemented yet (Phase 1: storage format only)\n", os.Args[1])
	os.Exit(2)
}
