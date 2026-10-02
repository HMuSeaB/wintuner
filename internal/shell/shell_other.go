//go:build !windows

package shell

import (
	"fmt"
	"os/exec"
	"runtime"
)

// openDir 在非 Windows 上用系统默认方式打开目录。
func openDir(dir string) error {
	exe := "xdg-open"
	if runtime.GOOS == "darwin" {
		exe = "open"
	}
	return run(exe, dir)
}

// revealInExplorer 在非 Windows 上没有"选中文件"的统一做法，退化为打开所在目录。
func revealInExplorer(path string) error {
	dir := path
	if i := lastSep(path); i >= 0 {
		dir = path[:i]
	}
	return openDir(dir)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("打开失败: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func lastSep(p string) int {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return i
		}
	}
	return -1
}
