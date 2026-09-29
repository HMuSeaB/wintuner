//go:build !windows

package diag

import "fmt"

// IsProtected 在非 Windows 上照旧保护系统进程名。
func IsProtected(name string) bool {
	switch name {
	case "init", "systemd", "launchd", "kernel_task":
		return true
	}
	return false
}

// KillByName 在非 Windows 上不支持。
func KillByName(name string) (int, error) {
	return 0, fmt.Errorf("diag: 结束进程仅支持 Windows")
}
