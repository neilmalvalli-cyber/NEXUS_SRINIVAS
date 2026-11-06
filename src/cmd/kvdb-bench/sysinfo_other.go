//go:build !windows

package main

import (
	"os"
	"os/exec"
	"strings"
)

func cpuName() string {
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "model name") {
				if _, v, ok := strings.Cut(l, ":"); ok {
					return strings.TrimSpace(v)
				}
			}
		}
	}
	if out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

func osVersion() string {
	if out, err := exec.Command("uname", "-sr").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}
