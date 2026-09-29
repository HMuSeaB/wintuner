//go:build !windows

// 非 Windows 平台上的空实现。
//
// 存在的理由是本项目的约定：六个目标平台组合都要能构建。注册表是 Windows
// 概念，这里返回明确的不支持错误，而不是让调用方靠 build tag 到处分叉。
package winreg

import "errors"

// ErrUnsupported 表示当前平台没有注册表。
var ErrUnsupported = errors.New("winreg: 注册表仅存在于 Windows")

type Root uintptr

const (
	CLASSES_ROOT   Root = 0
	CURRENT_USER   Root = 0
	LOCAL_MACHINE  Root = 0
	USERS          Root = 0
	CURRENT_CONFIG Root = 0
)

const (
	Read      = 0
	Write     = 0
	AllAccess = 0
)

type Key struct{ path string }

func Open(root Root, sub string) (*Key, error)   { return nil, ErrUnsupported }
func Create(root Root, sub string) (*Key, error) { return nil, ErrUnsupported }

func (k *Key) Close()                                   {}
func (k *Key) Path() string                             { return k.path }
func (k *Key) GetString(name string) (string, error)    { return "", ErrUnsupported }
func (k *Key) SetString(name, value string) error       { return ErrUnsupported }
func (k *Key) SetExpandString(name, value string) error { return ErrUnsupported }
func (k *Key) GetDword(name string) (uint32, error)     { return 0, ErrUnsupported }
func (k *Key) SetDword(name string, v uint32) error     { return ErrUnsupported }
func (k *Key) DeleteValue(name string) error            { return ErrUnsupported }
func (k *Key) ValueNames() ([]string, error)            { return nil, ErrUnsupported }
func (k *Key) SubKeyNames() ([]string, error)           { return nil, ErrUnsupported }

func DeleteTree(root Root, sub string) error { return ErrUnsupported }
