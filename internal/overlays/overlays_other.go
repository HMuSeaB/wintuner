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
