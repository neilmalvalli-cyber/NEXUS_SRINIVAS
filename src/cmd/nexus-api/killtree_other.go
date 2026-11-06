//go:build !windows

package main

import "os"

// killTree ends the process. On non-Windows systems children of
// nexus-torture are not tracked and may outlive a cancelled run.
func killTree(p *os.Process) {
	if p != nil {
		p.Kill()
	}
}
