//go:build windows

package shell

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	shell32           = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteW = shell32.NewProc("ShellExecuteW")
)

// openDir 打开一个目录。
//
// 用 ShellExecute 的 "open" 动词而不是 `cmd /c start`：后者会闪一个
// 控制台窗口，而且 cmd 在部分安全策略下会被拦。
func openDir(dir string) error {
	return shellExecute("open", dir)
}

// revealInExplorer 打开目录并选中文件。
//
// 参数拼成 /select,"路径" —— 引号是必须的：含空格的路径不加引号会被
// 拆成两段，资源管理器把第二段当成另一个参数而报错。
func revealInExplorer(path string) error {
	return shellExecute("open", "explorer.exe", `/select,"`+path+`"`)
}

// shellExecute 是 ShellExecuteW 的薄封装。params 为空时传 NULL。
func shellExecute(verb, file string, params ...string) error {
	pVerb, err := syscall.UTF16PtrFromString(verb)
	if err != nil {
		return err
	}
	pFile, err := syscall.UTF16PtrFromString(file)
	if err != nil {
		return err
	}

	var pParams uintptr
	if len(params) > 0 && params[0] != "" {
		p, err := syscall.UTF16PtrFromString(params[0])
		if err != nil {
			return err
		}
		pParams = uintptr(unsafe.Pointer(p))
	}

	// 返回值 > 32 表示成功，其余是错误码。
	ret, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(pVerb)),
		uintptr(unsafe.Pointer(pFile)),
		pParams,
		0,
		1, // SW_SHOWNORMAL
	)
	if ret <= 32 {
		return fmt.Errorf("打开失败（ShellExecute 返回 %d）", ret)
	}
	return nil
}
