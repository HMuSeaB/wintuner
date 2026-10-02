//go:build !windows

package overlays

import "errors"

var ErrUnsupported = errors.New("overlays: 图标叠加管理仅支持 Windows")

type Item struct {
	Name    string `json:"name"`
	CLSID   string `json:"clsid"`
	DLL     string `json:"dll"`
	Vendor  string `json:"vendor"`
	Enabled bool   `json:"enabled"`
	Builtin bool   `json:"builtin"`
	Dead    bool   `json:"dead"`
}

func List() ([]Item, error)     { return nil, ErrUnsupported }
func Disable(name string) error { return ErrUnsupported }
func Enable(name string) error  { return ErrUnsupported }

// HasOwnerRunning 在非 Windows 上没有意义，恒为 true。
//
// 返回 true 而不是 false 是有意的：调用方用它筛"可清理项"，
// true 表示"不列入清理"，是更保守的一侧。
func HasOwnerRunning(dll string) bool { return true }
