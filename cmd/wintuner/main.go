// Command wintuner 用一个界面管理启动项、图标角标与进程注入。
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/HMuSeaB/wintuner/internal/browser"
	"github.com/HMuSeaB/wintuner/internal/console"
	"github.com/HMuSeaB/wintuner/internal/elevate"
	"github.com/HMuSeaB/wintuner/internal/version"
	"github.com/HMuSeaB/wintuner/internal/webui"
)

// defaultIdle 是界面无人访问后自动退出的等待时长。
//
// 页面每 60 秒发一次心跳，所以"标签页还开着"不会被算成空闲。
// 这个兜底针对的是一种残留：双击启动时进程没有控制台窗口，
// 用户关掉标签页后如果不自动退出，就只能去任务管理器里结束它。
const defaultIdle = 30 * time.Minute

func main() {
	console.EnableUTF8()

	args := os.Args[1:]
	// 双击启动时没有参数，直接开界面；从终端裸跑则打印帮助。
	if len(args) == 0 && console.IsExclusiveConsole() {
		args = []string{"gui"}
	}

	if err := dispatch(args); err != nil {
		fmt.Fprintln(os.Stderr, "× "+err.Error())
		os.Exit(1)
	}
}

func dispatch(args []string) error {
	if len(args) == 0 {
		printHelp()
		return nil
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "gui", "ui":
		return cmdGUI(rest)
	case "version", "-V", "--version":
		fmt.Println(version.String())
		return nil
	case "help", "-h", "--help":
		printHelp()
		return nil
	}
	return fmt.Errorf("未知命令 %q，试试 `wintuner help`", cmd)
}

func cmdGUI(args []string) error {
	addr := ""
	noOpen := false
	idle := ""
	showHelp := false

	// 自己解析：只有一个包、几个选项，为此引入 flag 包徒增复杂度。
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			showHelp = true
		case a == "--no-open":
			noOpen = true
		case a == "--addr":
			if i+1 >= len(args) {
				return fmt.Errorf("--addr 需要一个地址")
			}
			i++
			addr = args[i]
		case strings.HasPrefix(a, "--addr="):
			addr = strings.TrimPrefix(a, "--addr=")
		case a == "--idle":
			if i+1 >= len(args) {
				return fmt.Errorf("--idle 需要一个时长")
			}
			i++
			idle = args[i]
		case strings.HasPrefix(a, "--idle="):
			idle = strings.TrimPrefix(a, "--idle=")
		default:
			rest = append(rest, a)
		}
	}

	if showHelp {
		printGUIHelp()
		return nil
	}
	if len(rest) > 0 {
		return fmt.Errorf("gui 不接受位置参数，收到 %q", strings.Join(rest, " "))
	}

	// 非回环地址直接拒绝：这个服务能改系统配置，暴露到局域网等于把机器交出去。
	normAddr, err := webui.NormalizeAddr(addr)
	if err != nil {
		return err
	}
	idleTimeout, err := parseIdle(idle)
	if err != nil {
		return err
	}

	srv, err := webui.New(webui.Options{
		Version:     version.Version,
		Addr:        normAddr,
		IdleTimeout: idleTimeout,
	})
	if err != nil {
		return err
	}
	if err := srv.Start(); err != nil {
		return err
	}
	defer srv.Shutdown()

	fmt.Println("WinTuner 图形界面")
	fmt.Printf("  界面地址    %s\n", srv.URL())
	fmt.Printf("  监听        %s\n", srv.Addr())
	fmt.Printf("  管理员      %v\n", elevate.IsAdmin())
	if idleTimeout > 0 {
		fmt.Printf("  空闲退出    %s\n", idleTimeout)
	}
	fmt.Println()

	if noOpen {
		fmt.Println("· --no-open：请手动打开上面的地址")
	} else if err := browser.Open(srv.URL()); err != nil {
		// 打不开浏览器不致命：地址已经打印出来了。
		fmt.Println("! 无法自动打开浏览器：" + err.Error())
		fmt.Println("· 请手动打开上面的地址")
	} else {
		fmt.Println("✓ 已在浏览器中打开")
	}

	// 界面起来了才收控制台，否则启动期的报错会写进一个看不见的窗口。
	if console.IsExclusiveConsole() {
		console.HideConsoleWindow()
	}

	fmt.Println("· 按 Ctrl+C 结束（关掉界面窗口也会自动结束）")
	waitForQuit(srv)
	fmt.Println("· 已退出")
	return nil
}

func waitForQuit(srv *webui.Server) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)

	select {
	case <-srv.Done():
	case <-sig:
	}
}

// parseIdle 解析 --idle。空取默认，"0"/"off" 表示不自动退出。
func parseIdle(s string) (time.Duration, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return defaultIdle, nil
	case "0", "off", "never":
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("--idle 需要时长（如 30m、1h）或 off，收到 %q", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("--idle 不能为负数")
	}
	return d, nil
}

func printHelp() {
	fmt.Print(`WinTuner —— 启动项、图标角标、进程注入的开关

用法:
  wintuner gui              打开图形界面（双击 wintuner.exe 同此）
  wintuner version          打印版本
  wintuner help             打印本帮助

说明:
  界面是内嵌的网页，由本进程在 127.0.0.1 上提供，
  再用系统浏览器（优先 Edge / Chrome 的 --app 模式）打开。
  不联网、不监听外部地址。

  改 HKLM 下的启动项、改服务、动 Program Files 里的 DLL 需要管理员权限。
  非管理员可以查看，界面上会给出"以管理员身份重启"的按钮。
`)
}

func printGUIHelp() {
	fmt.Print(`用法: wintuner gui [选项]

打开图形界面。界面是一份内嵌的网页，由本进程在 127.0.0.1 上提供服务，
再用系统浏览器打开——不联网、不对外监听。

双击 wintuner.exe 等同于执行本命令。

选项:
  --addr <地址>    监听地址，只允许回环地址（默认 127.0.0.1:0 自动选端口）
  --no-open        只启动服务，不自动打开浏览器
  --idle <时长>    界面无人访问多久后自动退出（默认 30m，off 表示不退出）
  -h, --help       打印本帮助

示例:
  wintuner gui
  wintuner gui --addr 127.0.0.1:8080
  wintuner gui --no-open --idle off
`)
}
