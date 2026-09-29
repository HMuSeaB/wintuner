//go:build !windows

package browser

import (
	"fmt"
	"os/exec"
	"runtime"
	"syscall"
)

// detach 让浏览器脱离父进程的会话与进程组。
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// appModeCommands 按平台给出候选。
//
// macOS 上真正执行的是 /usr/bin/open，前提是应用包存在，
// 所以 need 指向 .app 目录；Linux 上浏览器装在 PATH 里，need 留空。
func appModeCommands(url string) []appModeCmd {
	if runtime.GOOS == "darwin" {
		apps := []string{
			"/Applications/Google Chrome.app",
			"/Applications/Microsoft Edge.app",
			"/Applications/Chromium.app",
		}
		var out []appModeCmd
		for _, app := range apps {
			out = append(out, appModeCmd{
				argv: []string{"open", "-na", app, "--args", "--app=" + url},
				need: app,
			})
		}
		return out
	}

	// Linux 及其它类 Unix：按 PATH 查找常见名字。
	// 用 snap / flatpak 装的变体命名各异，命中不了就退回 xdg-open，不影响功能。
	names := []string{
		"google-chrome", "google-chrome-stable", "chromium",
		"chromium-browser", "microsoft-edge", "microsoft-edge-stable",
	}
	var out []appModeCmd
	for _, n := range names {
		out = append(out, appModeCmd{argv: []string{n, "--app=" + url}})
	}
	return out
}

func openDefault(url string) error {
	exe := "xdg-open"
	if runtime.GOOS == "darwin" {
		exe = "open"
	}
	cmd := exec.Command(exe, url)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("browser: 无法打开浏览器: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
