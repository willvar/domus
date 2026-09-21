//go:build !linux

package cmd

import "domus/shared/logger"

func workerCommand(_ string, _ []string) {
	logger.Fatal("The content worker requires a Linux DOFS FUSE mount")
}
