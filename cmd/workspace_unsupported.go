//go:build !linux

package cmd

import "domus/shared/logger"

func workspaceCommand(_ string, _ []string) {
	logger.Fatal("Workspace containers are currently supported on Linux only")
}
