//go:build windows

// Package winreg 提供注册表读写。
//
// 只用 syscall 直调 advapi32，不引第三方依赖——
// 与 wbmux 同一条约定：零依赖、可交叉编译、易审计。
package winreg

import (
	"fmt"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// Root 是预定义的根键句柄。这些 HKEY_* 值在 Windows 上固定不变。
type Root uintptr

const (
	CLASSES_ROOT   Root = 0x80000000
	CURRENT_USER   Root = 0x80000001
	LOCAL_MACHINE  Root = 0x80000002
	USERS          Root = 0x80000003
	CURRENT_CONFIG Root = 0x80000005
)

// 访问权限。
const (
	Read      = 0x00020019
	Write     = 0x00020006
	AllAccess = 0x000F003F
)

// 值类型。
const (
	regSz       = 1
	regExpandSz = 2
	regDword    = 4
)

// 错误码。
const (
	errMoreData     = 234
	errNoMoreItems  = 259
	errFileNotFound = 2
)

var (
	advapi32            = syscall.NewLazyDLL("advapi32.dll")
	procRegOpenKeyEx    = advapi32.NewProc("RegOpenKeyExW")
	procRegCreateKeyEx  = advapi32.NewProc("RegCreateKeyExW")
	procRegCloseKey     = advapi32.NewProc("RegCloseKey")
	procRegQueryValueEx = advapi32.NewProc("RegQueryValueExW")
	procRegSetValueEx   = advapi32.NewProc("RegSetValueExW")
	procRegDeleteValue  = advapi32.NewProc("RegDeleteValueW")
	procRegEnumValue    = advapi32.NewProc("RegEnumValueW")
	procRegEnumKeyEx    = advapi32.NewProc("RegEnumKeyExW")
	procRegDeleteTree   = advapi32.NewProc("RegDeleteTreeW")
)

// Key 是一个已打开的键。
type Key struct {
	h    syscall.Handle
	path string
}

// Open 打开一个已存在的键，只读。不存在则报错，不创建。
//
// 刻意只要读权限：扫描 HKLM 下的服务这类操作在未提权时也必须能跑，
// 一旦连写权限一起要，非管理员进程会直接拿到 Access is denied，
// 整个扫描就废了。需要写入时用 OpenWrite。
func Open(root Root, sub string) (*Key, error) {
	return open(root, sub, Read)
}

// OpenWrite 打开一个可写的键。HKLM 下的键需要管理员权限。
func OpenWrite(root Root, sub string) (*Key, error) {
	return open(root, sub, Read|Write)
}

func open(root Root, sub string, access uint32) (*Key, error) {
	var h syscall.Handle
	p, err := syscall.UTF16PtrFromString(sub)
	if err != nil {
		return nil, fmt.Errorf("winreg: 路径非法 %q: %w", sub, err)
	}
	ret, _, _ := procRegOpenKeyEx.Call(
		uintptr(root), uintptr(unsafe.Pointer(p)), 0, uintptr(access),
		uintptr(unsafe.Pointer(&h)),
	)
	if ret != 0 {
		return nil, fmt.Errorf("winreg: 打开 %s 失败: %s", display(root, sub), syscall.Errno(ret))
	}
	return &Key{h: h, path: display(root, sub)}, nil
}

// Create 打开键，不存在则创建。写入备份这类场景用它。
func Create(root Root, sub string) (*Key, error) {
	var h syscall.Handle
	var disposition uint32
	p, err := syscall.UTF16PtrFromString(sub)
	if err != nil {
		return nil, fmt.Errorf("winreg: 路径非法 %q: %w", sub, err)
	}
	ret, _, _ := procRegCreateKeyEx.Call(
		uintptr(root), uintptr(unsafe.Pointer(p)), 0, 0, 0,
		uintptr(AllAccess), 0,
		uintptr(unsafe.Pointer(&h)), uintptr(unsafe.Pointer(&disposition)),
	)
	if ret != 0 {
		return nil, fmt.Errorf("winreg: 创建 %s 失败: %s", display(root, sub), syscall.Errno(ret))
	}
	return &Key{h: h, path: display(root, sub)}, nil
}

func (k *Key) Close() {
	if k == nil || k.h == 0 {
		return
	}
	_, _, _ = procRegCloseKey.Call(uintptr(k.h))
	k.h = 0
}

// Path 返回可读路径，用于报错。
func (k *Key) Path() string { return k.path }

// GetString 读一个字符串值（REG_SZ / REG_EXPAND_SZ）。
func (k *Key) GetString(name string) (string, error) {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", fmt.Errorf("winreg: 值名非法: %w", err)
	}

	// 第一次调用只问长度，缓冲区传 nil。
	var typ, size uint32
	ret, _, _ := procRegQueryValueEx.Call(
		uintptr(k.h), uintptr(unsafe.Pointer(n)), 0,
		uintptr(unsafe.Pointer(&typ)), 0, uintptr(unsafe.Pointer(&size)),
	)
	if ret != 0 {
		return "", fmt.Errorf("winreg: 读 %s\\%s 失败: %s", k.path, name, syscall.Errno(ret))
	}
	if typ != regSz && typ != regExpandSz {
		return "", fmt.Errorf("winreg: %s\\%s 不是字符串（类型 %d）", k.path, name, typ)
	}

	buf := make([]uint16, size/2+1)
	ret, _, _ = procRegQueryValueEx.Call(
		uintptr(k.h), uintptr(unsafe.Pointer(n)), 0,
		uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret != 0 {
		return "", fmt.Errorf("winreg: 读 %s\\%s 内容失败: %s", k.path, name, syscall.Errno(ret))
	}
	return utf16ToString(buf), nil
}

// SetString 写一个 REG_SZ 值。
func (k *Key) SetString(name, value string) error {
	return k.setTyped(name, value, regSz)
}

// SetExpandString 写一个 REG_EXPAND_SZ。含 %VAR% 的命令必须用这个类型，
// 否则系统不会展开变量，启动项会失效。
func (k *Key) SetExpandString(name, value string) error {
	return k.setTyped(name, value, regExpandSz)
}

func (k *Key) setTyped(name, value string, typ uint32) error {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return fmt.Errorf("winreg: 值名非法: %w", err)
	}
	// 长度按字节算，且要包含结尾的 NUL。
	data := utf16.Encode([]rune(value + "\x00"))
	ret, _, _ := procRegSetValueEx.Call(
		uintptr(k.h), uintptr(unsafe.Pointer(n)), 0, uintptr(typ),
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2),
	)
	if ret != 0 {
		return fmt.Errorf("winreg: 写 %s\\%s 失败: %s", k.path, name, syscall.Errno(ret))
	}
	return nil
}

// GetDword 读一个 REG_DWORD。
func (k *Key) GetDword(name string) (uint32, error) {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, fmt.Errorf("winreg: 值名非法: %w", err)
	}
	var typ, val, size uint32
	size = 4
	ret, _, _ := procRegQueryValueEx.Call(
		uintptr(k.h), uintptr(unsafe.Pointer(n)), 0,
		uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&val)),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret != 0 {
		return 0, fmt.Errorf("winreg: 读 %s\\%s 失败: %s", k.path, name, syscall.Errno(ret))
	}
	if typ != regDword {
		return 0, fmt.Errorf("winreg: %s\\%s 不是 DWORD（类型 %d）", k.path, name, typ)
	}
	return val, nil
}

// SetDword 写一个 REG_DWORD。
func (k *Key) SetDword(name string, value uint32) error {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return fmt.Errorf("winreg: 值名非法: %w", err)
	}
	val := value
	ret, _, _ := procRegSetValueEx.Call(
		uintptr(k.h), uintptr(unsafe.Pointer(n)), 0, uintptr(regDword),
		uintptr(unsafe.Pointer(&val)), 4,
	)
	if ret != 0 {
		return fmt.Errorf("winreg: 写 %s\\%s 失败: %s", k.path, name, syscall.Errno(ret))
	}
	return nil
}

// DeleteValue 删除一个值。
func (k *Key) DeleteValue(name string) error {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return fmt.Errorf("winreg: 值名非法: %w", err)
	}
	ret, _, _ := procRegDeleteValue.Call(uintptr(k.h), uintptr(unsafe.Pointer(n)))
	if ret != 0 {
		return fmt.Errorf("winreg: 删除 %s\\%s 失败: %s", k.path, name, syscall.Errno(ret))
	}
	return nil
}

// ValueNames 列出该键下的所有值名。键的"默认值"以空字符串表示。
func (k *Key) ValueNames() ([]string, error) {
	var out []string
	buf := make([]uint16, 256)
	for i := uint32(0); ; i++ {
		nameLen := uint32(len(buf))
		var typ, size uint32
		ret, _, _ := procRegEnumValue.Call(
			uintptr(k.h), uintptr(i),
			uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&nameLen)),
			0, uintptr(unsafe.Pointer(&typ)), 0, uintptr(unsafe.Pointer(&size)),
		)
		if ret == errNoMoreItems {
			break
		}
		if ret == errMoreData {
			// 名字缓冲区不够：扩大后重试同一个下标。
			buf = make([]uint16, len(buf)*4)
			i--
			continue
		}
		if ret != 0 {
			return out, fmt.Errorf("winreg: 枚举 %s 的值失败: %s", k.path, syscall.Errno(ret))
		}
		out = append(out, utf16ToString(buf[:nameLen]))
	}
	return out, nil
}

// SubKeyNames 列出该键下的所有子键名。
func (k *Key) SubKeyNames() ([]string, error) {
	var out []string
	buf := make([]uint16, 256)
	for i := uint32(0); ; i++ {
		nameLen := uint32(len(buf))
		ret, _, _ := procRegEnumKeyEx.Call(
			uintptr(k.h), uintptr(i),
			uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&nameLen)),
			0, 0, 0, 0,
		)
		if ret == errNoMoreItems {
			break
		}
		if ret != 0 {
			return out, fmt.Errorf("winreg: 枚举 %s 的子键失败: %s", k.path, syscall.Errno(ret))
		}
		out = append(out, utf16ToString(buf[:nameLen]))
	}
	return out, nil
}

// DeleteTree 删除一个子键及其全部内容。只用于清理本工具自己创建的备份键。
func DeleteTree(root Root, sub string) error {
	p, err := syscall.UTF16PtrFromString(sub)
	if err != nil {
		return fmt.Errorf("winreg: 路径非法: %w", err)
	}
	ret, _, _ := procRegDeleteTree.Call(uintptr(root), uintptr(unsafe.Pointer(p)))
	if ret != 0 {
		return fmt.Errorf("winreg: 删除 %s 失败: %s", display(root, sub), syscall.Errno(ret))
	}
	return nil
}

// utf16ToString 截到第一个 NUL 为止。
//
// 不直接用 syscall.UTF16ToString：它要求切片正好以 NUL 结尾，
// 而 RegEnumValue 回写的长度是"不含结尾"的字符数，直接传会多带一个字符。
func utf16ToString(buf []uint16) string {
	for i, v := range buf {
		if v == 0 {
			return string(utf16.Decode(buf[:i]))
		}
	}
	return string(utf16.Decode(buf))
}

func display(root Root, sub string) string {
	var name string
	switch root {
	case CLASSES_ROOT:
		name = "HKCR"
	case CURRENT_USER:
		name = "HKCU"
	case LOCAL_MACHINE:
		name = "HKLM"
	case USERS:
		name = "HKU"
	case CURRENT_CONFIG:
		name = "HKCC"
	default:
		name = fmt.Sprintf("HKEY(%#x)", uintptr(root))
	}
	if sub == "" {
		return name
	}
	return name + `\` + strings.Trim(sub, `\`)
}
