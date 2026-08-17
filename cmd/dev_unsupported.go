//go:build !linux

package cmd

import "domus/shared/logger"

func devCommand(_ string, _ []string) {
	logger.Fatal("The complete development stack is currently supported on Linux only")
}
