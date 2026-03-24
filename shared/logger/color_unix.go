//go:build !windows

package logger

import (
	"os"

	"golang.org/x/term"
)

// supportsColor 检测终端是否支持颜色输出。
func supportsColor() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}
