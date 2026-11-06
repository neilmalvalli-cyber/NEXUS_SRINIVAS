// Package cli implements the kvdb commands: open, put, get, del, batch, dump,
// check and serve (README "Command-line interface", DESIGN §18).
//
// Run is the whole program; cmd/kvdb only passes os.Args and the standard
// streams. Every command opens the database (running recovery), does its
// work and closes it; serve keeps it open until stdin ends or the process is
// killed.
package cli
