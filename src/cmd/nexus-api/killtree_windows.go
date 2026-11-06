//go:build windows

package main

import (
	"os"
	"os/exec"
	"strconv"
)

// killTree ends a nexus-torture process and the kvdb children it started.
// It runs the fixed system command `taskkill /T /F /PID <pid>`; no user
// input reaches the command line. Falls back to killing the process alone.
func killTree(p *os.Process) {
	if p == nil {
		return
	}
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid)).Run(); err != nil {
		p.Kill()
	}
}
