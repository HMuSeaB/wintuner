//go:build !windows

// 非 Windows 平台上没有启动项概念，返回不支持。
//
// 保留这个空壳是为了让六个目标平台组合都能构建——与 winreg 同样的约定。
package autostart

import "errors"

type Source string

const (
	SourceRunMachine   Source = "run-hklm"
	SourceRunMachine32 Source = "run-hklm32"
	SourceRunUser      Source = "run-hkcu"
	SourceStartupUser  Source = "startup-user"
	SourceStartupAll   Source = "startup-all"
	SourceService      Source = "service"
	SourceTask         Source = "task"
)

var ErrUnsupported = errors.New("autostart: 启动项管理仅支持 Windows")

type Item struct{}

func Scan() ([]Item, error)   { return nil, ErrUnsupported }
func Disable(id string) error { return ErrUnsupported }
func Enable(id string) error  { return ErrUnsupported }
func ParkingDir() string      { return "" }
