//go:build windows

// Package elevate 判断当前是否管理员，并在需要时以管理员身份重启自己。
//
// 为什么必须做这件事：改 HKLM 下的启动项、改服务启动类型、动 Program Files
// 里的 DLL，全都需要提权。如果工具只是报一句"权限不足"，那用户除了自己去
// 找 exe 右键之外没有别的办法——而本工具是双击启动的，用户根本不知道 exe 在哪。
package elevate

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

var (
	shell32           = syscall.NewLazyDLL("shell32.dll")
	procIsUserAnAdmin = shell32.NewProc("IsUserAnAdmin")
	procShellExecuteW = shell32.NewProc("ShellExecuteW")
)

// IsAdmin 判断当前进程是否以管理员身份运行。
func IsAdmin() bool {
	ret, _, _ := procIsUserAnAdmin.Call()
	return ret != 0
}

// Relaunch 以管理员身份重新启动当前程序。
//
// 走 ShellExecute 的 runas 动词：由系统负责弹 UAC，
// 不自己去拼 manifest 或降权令牌，也就不需要引入额外的依赖。
// 成功时新的实例已经起来，调用方应当随即退出自己。
func Relaunch(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("elevate: 拿不到自身路径: %w", err)
	}

	verb, err := syscall.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}

	// 参数要拼成一个字符串，且含空格的项要用引号包起来——
	// ShellExecute 不像 CreateProcess 那样吃参数数组。
	params, err := syscall.UTF16PtrFromString(joinArgs(args))
	if err != nil {
		return err
	}

	// 返回值 > 32 表示成功；否则是错误码。
	ret, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)),
		0,
		1, // SW_SHOWNORMAL
	)
	if ret <= 32 {
		return fmt.Errorf("elevate: 请求管理员权限失败（代码 %d），可能是被 UAC 拒绝", ret)
	}
	return nil
}

// joinArgs 把参数拼成命令行，必要时加引号。
func joinArgs(args []string) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if a == "" {
			continue
		}
		if strings.ContainsAny(a, " \t\"") {
			parts = append(parts, `"`+a+`"`)
			continue
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}
