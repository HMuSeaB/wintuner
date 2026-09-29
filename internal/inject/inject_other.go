//go:build !windows

package inject

import "errors"

var ErrUnsupported = errors.New("inject: Winsock 提供程序管理仅支持 Windows")

type Provider struct {
	ID       string `json:"id"`
	Bits     string `json:"bits"`
	Slot     string `json:"slot"`
	DLL      string `json:"dll"`
	Vendor   string `json:"vendor"`
	Disabled bool   `json:"disabled"`
	Builtin  bool   `json:"builtin"`
}

func List() ([]Provider, error) { return nil, ErrUnsupported }
func Disable(id string) error   { return ErrUnsupported }
func Enable(id string) error    { return ErrUnsupported }
