//go:build windows

package autostart

import (
	"os"
	"path/filepath"
)

// parkingCandidates 是"禁用后的启动项"存放目录，按优先级排列。
//
// 第一位是用户原本就在手工使用的位置——他把不想启动的快捷方式挪到那里。
// 沿用它的意义不只是省事：用户已经知道那个文件夹里躺的是什么，
// 换地方等于又多一个要记的位置。
var parkingCandidates = []string{
	`D:\Tools\disabled-autostart`,
}

// ParkingDir 返回存放目录，必要时创建。
func ParkingDir() string {
	for _, c := range parkingCandidates {
		// D: 这类可移动盘可能不在，先看父目录是否存在，避免凭空制造路径。
		if parent := filepath.Dir(c); dirExists(parent) {
			if !dirExists(c) {
				_ = os.MkdirAll(c, 0o755)
			}
			if dirExists(c) {
				return c
			}
		}
	}
	// 退路：本工具自己的目录。
	fallback := filepath.Join(os.Getenv("LOCALAPPDATA"), "WinTuner", "disabled-autostart")
	_ = os.MkdirAll(fallback, 0o755)
	return fallback
}

func startupDirUser() string {
	if v := os.Getenv("APPDATA"); v != "" {
		return filepath.Join(v, `Microsoft\Windows\Start Menu\Programs\Startup`)
	}
	return ""
}

func startupDirAll() string {
	if v := os.Getenv("ProgramData"); v != "" {
		return filepath.Join(v, `Microsoft\Windows\Start Menu\Programs\Startup`)
	}
	return ""
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
