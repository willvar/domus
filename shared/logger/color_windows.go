//go:build windows

package logger

import (
	"os"

	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

// supportsColor 检测终端是否支持颜色输出，并在 Windows 上启用虚拟终端处理。
func supportsColor() bool {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return false
	}

	// 尝试启用 Windows 虚拟终端处理 (Windows 10 1511+)
	handle := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return false
	}

	// ENABLE_VIRTUAL_TERMINAL_PROCESSING = 0x0004
	if err := windows.SetConsoleMode(handle, mode|0x0004); err != nil {
		return false
	}

	return true
}
