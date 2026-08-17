//go:build !linux

package cmd

import "domus/shared/logger"

func dofsCommand(_ string, _ []string) {
	logger.Fatal("DOFS mounts are currently supported on Linux only")
}
