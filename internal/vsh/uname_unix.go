//go:build !windows

package vsh

import "syscall"

// unameInfo returns sysname, nodename, release, machine from the real OS.
func unameInfo() (string, string, string, string) {
	var buf syscall.Utsname
	if err := syscall.Uname(&buf); err != nil {
		return "unknown", "zephyr", "unknown", "unknown"
	}
	return charsToString(buf.Sysname[:]),
		charsToString(buf.Nodename[:]),
		charsToString(buf.Release[:]),
		charsToString(buf.Machine[:])
}

func charsToString(ca []int8) string {
	buf := make([]byte, 0, len(ca))
	for _, c := range ca {
		if c == 0 {
			break
		}
		buf = append(buf, byte(c))
	}
	return string(buf)
}
