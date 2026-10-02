package console

import (
	"runtime"
	"testing"
)

// TestDLLProceduresResolve 确认所有 syscall 过程都能真正解析到。
//
// 这条测试来自一次真实事故：`ShowWindow` 被误写成 kernel32 里的过程
// （它其实在 user32）。`NewProc` 是惰性的，写错了不会立刻报错，
// 直到真正 `Call` 才 panic —— 而那条路径只在"双击启动"时才会走到。
//
// 结果就是：在终端里怎么测都正常，用户一双击就崩，浏览器打开时连不上，
// 表现得很像"端口冲突"。这个测试直接把"过程能不能解析"变成可测的断言。
func TestDLLProceduresResolve(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows")
	}

	procs := map[string]interface{ Find() error }{
		"kernel32.SetConsoleOutputCP":    procSetConsoleOutputCP,
		"kernel32.SetConsoleCP":          procSetConsoleCP,
		"kernel32.GetConsoleMode":        procGetConsoleMode,
		"kernel32.SetConsoleMode":        procSetConsoleMode,
		"kernel32.GetConsoleProcessList": procGetConsoleProcessList,
		"kernel32.GetConsoleWindow":      procGetConsoleWindow,
		"user32.ShowWindow":              procShowWindow,
	}

	for name, p := range procs {
		if err := p.Find(); err != nil {
			t.Errorf("%s 无法解析: %v", name, err)
		}
	}
}

// TestHideConsoleWindowDoesNotPanic 确认隐藏控制台这条路径不会 panic。
//
// 它是最容易出事的一条：只在双击启动时执行，在终端里跑不到。
// 这里直接调用它——在没有控制台的测试进程里应当是安全的空操作。
func TestHideConsoleWindowDoesNotPanic(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows")
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("HideConsoleWindow 发生了 panic: %v", r)
		}
	}()
	HideConsoleWindow()
}

// TestEnableUTF8DoesNotPanic 确认编码设置不会 panic。
func TestEnableUTF8DoesNotPanic(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows")
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("EnableUTF8 发生了 panic: %v", r)
		}
	}()
	EnableUTF8()
}
