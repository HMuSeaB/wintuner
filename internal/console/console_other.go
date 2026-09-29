//go:build !windows

package console

// EnableUTF8 在非 Windows 上为空操作。
func EnableUTF8() {}

// IsExclusiveConsole 恒为 false：类 Unix 上没有"双击启动"这个概念。
func IsExclusiveConsole() bool { return false }

// HideConsoleWindow 在非 Windows 上为空操作。
func HideConsoleWindow() {}
