//go:build windows

package overlays

import (
	"encoding/binary"
	"syscall"
	"unsafe"
)

var (
	k32                  = syscall.NewLazyDLL("kernel32.dll")
	procCreateToolhelp32 = k32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW  = k32.NewProc("Process32FirstW")
	procProcess32NextW   = k32.NewProc("Process32NextW")
	procCloseHandle32    = k32.NewProc("CloseHandle")
)

const (
	th32csSnapProcess32 = 0x00000002
	invalidHandle32     = ^uintptr(0)
	// PROCESSENTRY32W 的字段偏移，64 位 Windows 上固定。
	// 结构体自身按 8 字节对齐，所以 44+520=564 要取整到 568；
	// dwSize 填少了 Process32First 会直接失败，表现为"一个进程都列不出来"。
	sizeEntry32  = 568
	offExeFile32 = 44
)

// processNames 返回当前所有进程名。
//
// 这里自己走一次 Toolhelp 而不是复用 diag 包：overlays 是底层模块，
// 让它依赖 diag 会把依赖关系倒过来。二十行代码的事，不值得为它引入耦合。
func processNames() ([]string, error) {
	snap, _, _ := procCreateToolhelp32.Call(uintptr(th32csSnapProcess32), 0)
	if snap == invalidHandle32 {
		return nil, syscall.EINVAL
	}
	defer procCloseHandle32.Call(snap)

	buf := make([]byte, sizeEntry32)
	// dwSize 必须自己填，否则 Process32First 直接失败。
	binary.LittleEndian.PutUint32(buf[0:4], sizeEntry32)

	var out []string
	ret, _, _ := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&buf[0])))
	for ret != 0 {
		out = append(out, utf16At(buf, offExeFile32))
		ret, _, _ = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&buf[0])))
	}
	return out, nil
}

func utf16At(buf []byte, off int) string {
	var runes []uint16
	for i := off; i+1 < len(buf); i += 2 {
		v := uint16(buf[i]) | uint16(buf[i+1])<<8
		if v == 0 {
			break
		}
		runes = append(runes, v)
	}
	return syscall.UTF16ToString(runes)
}
