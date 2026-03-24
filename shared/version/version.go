// Package version 提供统一的版本信息
package version

import (
	"fmt"
	"os"
	"runtime"
)

// Version 编译时通过 -ldflags 注入
var Version = "dev"

// Print 打印版本信息并退出
func Print(appName string) {
	fmt.Printf("%s %s (%s %s/%s)\n", appName, Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	os.Exit(0)
}

// Check 检查命令行参数是否为版本请求
// 支持: --version, -V, version
func Check(appName string) {
	if len(os.Args) < 2 {
		return
	}
	switch os.Args[1] {
	case "--version", "-V", "version":
		Print(appName)
	}
}
