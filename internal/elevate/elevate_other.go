//go:build !windows

package elevate

import "errors"

// IsAdmin 在类 Unix 上等价于 root。
func IsAdmin() bool { return false }

// Relaunch 在非 Windows 上没有 UAC 概念。
func Relaunch(args ...string) error { return errors.New("elevate: 仅支持 Windows") }
