//go:build linux

package dofs

import (
	"fmt"
	"os"
	"path/filepath"
)

// ensurePrivateDirectory protects manager-owned desired state and control
// socket parents. DOFS writeback has its own equivalent inside the standalone
// module; this helper belongs to the Domus lifecycle manager only.
func ensurePrivateDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("create private directory %s: %w", directory, err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect private directory %s: %w", directory, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("private path %s must be a real directory", directory)
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return fmt.Errorf("resolve private directory %s: %w", directory, err)
	}
	if filepath.Clean(resolved) != filepath.Clean(directory) {
		return fmt.Errorf("private path %s must not contain symbolic-link components", directory)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return fmt.Errorf("protect private directory %s: %w", directory, err)
	}
	return nil
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return file.Sync()
}
