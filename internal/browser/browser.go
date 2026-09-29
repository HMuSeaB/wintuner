// Package browser 用系统浏览器打开一个本地地址。
//
// 优先用 Edge / Chrome 的 --app= 模式：出来的窗口没有地址栏、没有标签页、
// 任务栏里是独立一项，看起来就是个本地程序。没有 Chromium 系浏览器时
// 退回系统默认浏览器，功能不受影响。
package browser

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// appModeCmd 是一条候选命令。
//
// argv 与 need 分开是因为两者不等价：macOS 上真正执行的是 /usr/bin/open，
// 而前提是 /Applications/Google Chrome.app 存在。只看 argv[0] 会把
// "没装 Chrome"误判成可打开，且失败发生在 Start 之后，会被当成成功。
type appModeCmd struct {
	argv []string
	need string
}

// Open 打开地址。
func Open(url string) error {
	if url == "" {
		return fmt.Errorf("browser: 地址为空")
	}

	for _, cand := range appModeCommands(url) {
		if cand.need != "" && !fileExists(cand.need) {
			continue
		}
		if !resolvable(cand.argv[0]) {
			continue
		}
		cmd := exec.Command(cand.argv[0], cand.argv[1:]...)
		detach(cmd)
		if err := cmd.Start(); err != nil {
			continue
		}
		// 不回收会在类 Unix 上留下僵尸进程。
		go func() { _ = cmd.Wait() }()
		return nil
	}
	return openDefault(url)
}

// resolvable 判断一个命令名能不能执行。
//
// 含分隔符的按绝对路径查，否则查 PATH——混用会让绝对路径意外命中
// PATH 里的同名程序。
func resolvable(name string) bool {
	if strings.ContainsAny(name, `/\`) {
		return fileExists(name)
	}
	_, err := exec.LookPath(name)
	return err == nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
