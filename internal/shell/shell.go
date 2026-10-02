// Package shell 用系统资源管理器打开目录、或在其中定位一个文件。
//
// 与 browser 包分开：那个负责开浏览器，这个负责开文件管理器。
// 两者都要调外部程序，但目的和行为不同，混在一起容易写错。
package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OpenDir 用资源管理器打开一个目录。
func OpenDir(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return fmt.Errorf("路径为空")
	}
	// 允许用户手输路径，这里做一次清理，避免把 "..\.." 之类原样传下去。
	dir = filepath.Clean(dir)

	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("目录不存在或无法访问：%s", dir)
	}
	if !info.IsDir() {
		// 给的是文件就打开它所在的目录，这比报错有用。
		return RevealFile(dir)
	}
	return openDir(dir)
}

// RevealFile 打开文件所在目录，并选中该文件。
//
// 选中是重点：目录里几十个文件，只把目录打开等于让用户自己找。
func RevealFile(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("路径为空")
	}
	path = filepath.Clean(path)

	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("文件不存在或无法访问：%s", path)
	}
	return revealInExplorer(path)
}
