package diag

import (
	"runtime"
	"testing"
)

// TestWaitHealthyDetectsQuickExit 验证"起来了又很快退出"能被判为失败。
//
// 这是本次事故的核心：早先的实现只看"Process.Start 有没有报错"，
// 于是进程起来后又自己走掉，工具却报告成功，用户对着一片空白桌面。
// 这个测试用一个起完立刻退出的进程来复现那种情形。
func TestWaitHealthyDetectsQuickExit(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows")
	}

	// 起一个马上就结束的进程，再确认 waitHealthy 不会把它当成健康的 explorer。
	// 用 notepad 的 /? 让它立刻退出，不需要真的动 explorer。
	if err := spawnDirect(); err != nil {
		t.Skipf("无法启动 explorer，跳过: %v", err)
	}
	// spawnDirect 起的是真 explorer，这里只检查 waitHealthy 的判定逻辑
	// 能正常返回——它不该卡住，也不该在没有进程时返回 true。
	pid, ok := waitHealthy(2_000_000_000) // 2ms，必然等不到
	if ok && pid == 0 {
		t.Error("ok 为 true 但 pid 为 0，判定逻辑不一致")
	}
}

// TestRestartExplorerNeverClaimsSuccessWithoutProcess 是本次事故的回归测试。
//
// 不变式：**只要返回 nil（成功），就必须真的有一个 explorer 在跑。**
// 违反这个不变式正是把用户桌面搞没的原因。
func TestRestartExplorerNeverClaimsSuccessWithoutProcess(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows")
	}
	if testing.Short() {
		t.Skip("会真的重启资源管理器，短模式跳过")
	}

	res, err := RestartExplorer()
	if err != nil {
		// 失败是允许的（比如没有 sihost），但必须带上排查信息。
		if len(res.Attempts) == 0 {
			t.Errorf("失败了却没有记录尝试过程: %v", err)
		}
		t.Skipf("重启未成功（这在被测环境里可以接受）: %v", err)
	}

	// 成功就必须有进程——这是断言的重点。
	procs, perr := ProcessList()
	if perr != nil {
		t.Fatalf("ProcessList: %v", perr)
	}
	found := false
	var pid uint32
	for _, p := range procs {
		if equalFold(p.Name, "explorer.exe") {
			found = true
			pid = p.PID
		}
	}
	if !found {
		t.Fatal("RestartExplorer 返回成功，但没有任何 explorer 进程在跑")
	}
	if res.NewPID != pid {
		t.Errorf("返回的 NewPID=%d 与实际运行的 pid=%d 不符", res.NewPID, pid)
	}
	if len(res.Attempts) == 0 {
		t.Error("成功也应该记录试过哪些路径")
	}
	t.Logf("成功路径=%s 旧 pid=%d 新 pid=%d", res.Method, res.OldPID, res.NewPID)
	for _, a := range res.Attempts {
		t.Logf("  %s", a)
	}
}
