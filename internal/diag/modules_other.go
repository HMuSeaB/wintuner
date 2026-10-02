//go:build !windows

package diag

import "errors"

// ErrNoModules 表示当前平台不支持枚举进程模块。
var ErrNoModules = errors.New("diag: 枚举进程模块仅支持 Windows")

// Module 是加载进某个进程的一个 DLL。
type Module struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Vendor  string `json:"vendor"`
	Orphan  bool   `json:"orphan"`
	Builtin bool   `json:"builtin"`
}

// ExplorerModules 在非 Windows 上不支持。
func ExplorerModules() ([]Module, error) { return nil, ErrNoModules }
