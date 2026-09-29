//go:build windows

package browser

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

// detach 让浏览器脱离父进程的控制台。
//
// 图形界面可能由双击启动（父进程本身没有控制台），也可能从终端里跑起来、
// 用户随后把终端关掉。两种情况都不该把浏览器一起带走。
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | detachedProcess,
	}
}

// appModeCommands 返回候选浏览器，先 Chrome 后 Edge。
//
// 基目录为空时必须整项跳过：filepath.Join 会把空基目录当成相对路径，
// 拼出来的相对路径万一撞上当前目录下的同名文件就会被误判为可用。
func appModeCommands(url string) []appModeCmd {
	type entry struct{ base, rel string }
	entries := []entry{
		{os.Getenv("ProgramFiles"), `Google\Chrome\Application\chrome.exe`},
		{os.Getenv("ProgramFiles(x86)"), `Google\Chrome\Application\chrome.exe`},
		{os.Getenv("LOCALAPPDATA"), `Google\Chrome\Application\chrome.exe`},

		{os.Getenv("ProgramFiles(x86)"), `Microsoft\Edge\Application\msedge.exe`},
		{os.Getenv("ProgramFiles"), `Microsoft\Edge\Application\msedge.exe`},
		{os.Getenv("LOCALAPPDATA"), `Microsoft\Edge\Application\msedge.exe`},
	}

	var out []appModeCmd
	for _, e := range entries {
		if e.base == "" {
			continue
		}
		exe := filepath.Join(e.base, e.rel)
		out = append(out, appModeCmd{argv: []string{exe, "--app=" + url}, need: exe})
	}
	return out
}

// openDefault 交给系统注册的默认浏览器。
//
// 用 rundll32 而不是 `cmd /c start`：后者会闪一个控制台窗口，
// 在双击启动（无控制台）的场景里尤其突兀。
func openDefault(url string) error {
	cmd := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("browser: 无法打开浏览器: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
