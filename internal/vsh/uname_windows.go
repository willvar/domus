//go:build windows

package vsh

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// unameInfo returns sysname, nodename, release, machine on Windows.
func unameInfo() (string, string, string, string) {
	nodename, _ := os.Hostname()
	if nodename == "" {
		nodename = "zephyr"
	}

	release := "unknown"
	if out, err := exec.Command("cmd", "/c", "ver").Output(); err == nil {
		// e.g. "Microsoft Windows [Version 10.0.22631.5039]"
		s := strings.TrimSpace(string(out))
		if i := strings.Index(s, "["); i >= 0 {
			if j := strings.Index(s[i:], "]"); j >= 0 {
				release = s[i+1 : i+j]
			}
		}
	}

	return "Windows", nodename, release, runtime.GOARCH
}
