//go:build windows

// Package console 处理终端输出编码与"是否被双击启动"。
package console

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleOutputCP    = kernel32.NewProc("SetConsoleOutputCP")
	procSetConsoleCP          = kernel32.NewProc("SetConsoleCP")
	procGetConsoleMode        = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode        = kernel32.NewProc("SetConsoleMode")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	procGetConsoleWindow      = kernel32.NewProc("GetConsoleWindow")

	// ShowWindow 在 user32 里，不在 kernel32。
	//
	// 这里踩过一次很隐蔽的坑：写成 kernel32 时，NewProc 是惰性的，
	// 直到真正 Call 才会去找这个函数，找不到就直接 panic。
	// 而这条路径只在"双击启动"（独占控制台）时才走到——在终端里怎么测
	// 都测不出来，用户一双击就崩，浏览器打开时连不上，看起来像"端口冲突"。
	user32         = syscall.NewLazyDLL("user32.dll")
	procShowWindow = user32.NewProc("ShowWindow")
)

const (
	cpUTF8                          = 65001
	enableVirtualTerminalProcessing = 0x0004
	swHide                          = 0
)

// EnableUTF8 让控制台按 UTF-8 输出，并打开 ANSI 转义支持。
func EnableUTF8() {
	_, _, _ = procSetConsoleOutputCP.Call(cpUTF8)
	_, _, _ = procSetConsoleCP.Call(cpUTF8)

	var mode uint32
	h, _, _ := procGetConsoleMode.Call(uintptr(0xfffffff5), uintptr(unsafe.Pointer(&mode))) // STD_OUTPUT_HANDLE
	if h != 0 {
		_, _, _ = procSetConsoleMode.Call(uintptr(0xfffffff5), uintptr(mode|enableVirtualTerminalProcessing))
	}
}

// IsExclusiveConsole 判断当前进程是否独占一个控制台。
//
// 控制台子系统程序被双击时，Windows 会新开一个只含它自己的控制台；
// 在终端里跑时 shell 本身也挂在同一个控制台上，进程数必然大于 1。
func IsExclusiveConsole() bool {
	var pids [2]uint32
	n, _, _ := procGetConsoleProcessList.Call(
		uintptr(unsafe.Pointer(&pids[0])),
		uintptr(len(pids)),
	)
	return n == 1
}

// HideConsoleWindow 隐藏控制台窗口。
//
// 必须在图形界面起来之后才调用——在此之前隐藏，启动阶段的报错会写进一个
// 看不见的窗口，用户看到的是"双击没反应"，无从排查。
func HideConsoleWindow() {
	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd == 0 {
		return
	}
	_, _, _ = procShowWindow.Call(hwnd, uintptr(swHide))
}
