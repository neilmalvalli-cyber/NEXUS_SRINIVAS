// Command kvdb is the command-line wrapper around the engine.
//
//	kvdb open  <path> [--max-bytes N]
//	kvdb put   <path> <key> <value>
//	kvdb get   <path> <key>
//	kvdb del   <path> <key>
//	kvdb batch <path> <file>
//	kvdb dump  <path>
//	kvdb check <path>
//	kvdb serve <path>
//
// See package cli and README.md for the output formats and the serve protocol.
package main

import (
	"os"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}))
}
