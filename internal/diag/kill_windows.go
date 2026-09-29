//go:build windows

package diag

import (
	"fmt"
	"syscall"
)

// protected 是打死也不能通过界面结束的进程。
var protected = map[string]bool{
	"system":             true,
	"smss.exe":           true,
	"csrss.exe":          true,
	"wininit.exe":        true,
	"winlogon.exe":       true,
	"services.exe":       true,
	"lsass.exe":          true,
	"lsaiso.exe":         true,
	"svchost.exe":        true,
	"fontdrvhost.exe":    true,
	"dwm.exe":            true,
	"registry":           true,
	"memory compression": true,
	"secure system":      true,
	"ntoskrnl.exe":       true,
}

// IsProtected 判断进程名是否属于系统关键进程。
func IsProtected(name string) bool {
	return protected[lower(name)]
}

// KillByName 结束所有同名进程，返回实际结束的数量。
//
// 之所以提供这个能力：实测中一次 Explorer 卡顿的元凶是 23 个"启动了却没有
// 窗口"的 SnippingTool 进程。它们不占多少 CPU，却能让 shell 一直等下去。
// 这种情况下杀进程是精确解，而重启代价大得多。
func KillByName(name string) (int, error) {
	if IsProtected(name) {
		return 0, fmt.Errorf("%s 是系统关键进程，不允许结束", name)
	}

	procs, err := ProcessList()
	if err != nil {
		return 0, err
	}

	const processTerminate = 0x0001
	n := 0
	var lastErr error
	for _, p := range procs {
		if !equalFold(p.Name, name) || p.PID == 0 {
			continue
		}
		h, _, _ := procOpenProcess.Call(uintptr(processTerminate), 0, uintptr(p.PID))
		if h == 0 {
			lastErr = fmt.Errorf("打不开进程 %d", p.PID)
			continue
		}
		ret, _, _ := procTerminateProcess.Call(h, 1)
		procCloseHandle.Call(h)
		if ret != 0 {
			n++
		} else {
			lastErr = syscall.Errno(ret)
		}
	}
	if n == 0 && lastErr != nil {
		return 0, fmt.Errorf("结束 %s 失败: %w", name, lastErr)
	}
	return n, nil
}

func lower(s string) string {
	out := []byte(s)
	for i := range out {
		if 'A' <= out[i] && out[i] <= 'Z' {
			out[i] += 'a' - 'A'
		}
	}
	return string(out)
}

var procTerminateProcess = kernel32.NewProc("TerminateProcess")
