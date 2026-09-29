//go:build windows

package diag

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// RestartResult 是一次资源管理器重启的结果。
type RestartResult struct {
	// OK 为 true 表示最后确实有一个健康的 explorer 在跑。
	OK bool `json:"ok"`
	// Method 说明最终是靠哪条路径成功的。
	Method string `json:"method"`
	// OldPID / NewPID 便于确认真的换了实例。
	OldPID uint32 `json:"oldPid"`
	NewPID uint32 `json:"newPid"`
	// Attempts 记录每一步的成败，出问题时能看出卡在哪。
	Attempts []string `json:"attempts"`
}

// 各阶段的等待时长。
//
// 这些值是踩出来的：explorer 启动后有一小段时间会"假死"——进程在、但还没
// 挂上桌面。等太短会误判为失败，等太长用户会以为工具卡住了。
const (
	killWait   = 1200 * time.Millisecond // 等旧实例真正退出
	spawnWait  = 6 * time.Second         // 等新实例挂上桌面
	verifyWait = 1 * time.Second         // 健康检查间隔
)

// RestartExplorer 重启资源管理器，带验证与兜底。
//
// 直接"杀掉→启动"是不可靠的：在受限上下文里 fork 出来的 explorer 能成功创建
// 进程，却在几秒内自行退出（拿不到正常的桌面会话）。所以这里每一步都验证，
// 主路径失败就换下一条，而不是报告成功然后让用户对着一片空白。
//
// 实测教训：早先的版本只做"杀掉再起"，在真机上把用户桌面搞没了——
// 进程确实起来了，5 秒后自己走了，而工具返回了成功。
func RestartExplorer() (RestartResult, error) {
	res := RestartResult{}

	// 先记下旧实例，好在结果里体现"确实换了"。
	procs, _ := ProcessList()
	for _, p := range procs {
		if equalFold(p.Name, "explorer.exe") && p.PID != 0 {
			res.OldPID = p.PID
			break
		}
	}

	// 第一步：结束现有实例。本来就没有的话跳过——
	// 桌面已经没了的情况下，杀这一下没有意义。
	if res.OldPID != 0 {
		for _, p := range procs {
			if !equalFold(p.Name, "explorer.exe") || p.PID == 0 {
				continue
			}
			if err := terminate(p.PID); err != nil {
				res.Attempts = append(res.Attempts, fmt.Sprintf("结束 pid=%d 失败：%v", p.PID, err))
			}
		}
		time.Sleep(killWait)
	} else {
		res.Attempts = append(res.Attempts, "当前没有运行中的资源管理器，直接尝试启动")
	}

	// 第二步：按可靠性从高到低试几条启动路径。
	//
	// 顺序不是随便排的：实测在受限上下文里直接 fork explorer，进程会起来但
	// 几秒内自行退出；而让系统重建 shell 宿主能拿到干净的桌面会话上下文。
	for _, m := range []struct {
		name  string
		spawn func() error
	}{
		{"重建 shell 宿主", spawnViaShellHost},
		{"直接启动", spawnDirect},
	} {
		if err := m.spawn(); err != nil {
			res.Attempts = append(res.Attempts, fmt.Sprintf("%s：启动失败 %v", m.name, err))
			continue
		}
		if pid, ok := waitHealthy(spawnWait); ok {
			res.OK = true
			res.Method = m.name
			res.NewPID = pid
			res.Attempts = append(res.Attempts, fmt.Sprintf("%s：成功（pid=%d）", m.name, pid))
			return res, nil
		}
		res.Attempts = append(res.Attempts, fmt.Sprintf("%s：起来了但随后退出", m.name))
	}

	// 两条路都没成。如实报告，并给出用户自己能做的动作——
	// 这比返回一个乐观的成功、让人对着空白桌面强得多。
	return res, fmt.Errorf(
		"资源管理器未能启动。已尝试：%v。请按 Ctrl+Alt+Del 选择「注销」后重新登录",
		res.Attempts,
	)
}

// spawnViaShellHost 通过重建 sihost 让系统自己拉起 shell。
//
// 这条路更可靠的原因：它不是"我们启动 explorer"，而是"让系统重新初始化
// shell"，因此新实例拿到的是正常的桌面会话上下文。
func spawnViaShellHost() error {
	procs, err := ProcessList()
	if err != nil {
		return err
	}
	killed := false
	for _, p := range procs {
		if !equalFold(p.Name, "sihost.exe") || p.PID == 0 {
			continue
		}
		if err := terminate(p.PID); err == nil {
			killed = true
		}
	}
	if !killed {
		return fmt.Errorf("没有找到 sihost.exe")
	}
	return nil
}

// spawnDirect 直接启动 explorer。
func spawnDirect() error {
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000008, // DETACHED_PROCESS
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// waitHealthy 在给定时限内反复确认 explorer 真的活着。
//
// 关键在"反复"：单次检查会撞上启动瞬间，看起来像成功；
// 而故障的表现恰恰是"起来了、过几秒自己走"。
func waitHealthy(timeout time.Duration) (uint32, bool) {
	deadline := time.Now().Add(timeout)
	var lastPID uint32
	for time.Now().Before(deadline) {
		time.Sleep(verifyWait)
		procs, err := ProcessList()
		if err != nil {
			continue
		}
		found := uint32(0)
		for _, p := range procs {
			if equalFold(p.Name, "explorer.exe") && p.PID != 0 {
				found = p.PID
			}
		}
		if found == 0 {
			// 曾经起来过又不见了，说明这个实例站不住，不必等满时限。
			if lastPID != 0 {
				return 0, false
			}
			continue
		}
		lastPID = found
	}
	return lastPID, lastPID != 0
}

// terminate 结束一个进程。
func terminate(pid uint32) error {
	const processTerminate = 0x0001
	h, _, _ := procOpenProcess.Call(uintptr(processTerminate), 0, uintptr(pid))
	if h == 0 {
		return fmt.Errorf("打不开进程 %d", pid)
	}
	defer procCloseHandle.Call(h)
	ret, _, _ := procTerminateProcess.Call(h, 1)
	if ret == 0 {
		return fmt.Errorf("结束进程 %d 失败", pid)
	}
	return nil
}

// ExplorerNow 是给界面用的轻量即时读数。
type ExplorerNow struct {
	Running    bool    `json:"running"`
	PID        uint32  `json:"pid"`
	Threads    int     `json:"threads"`
	Handles    int     `json:"handles"`
	CPUPercent float64 `json:"cpuPercent"`
	CPUCores   float64 `json:"cpuCores"`
	SampleMS   int     `json:"sampleMs"`
	Verdict    string  `json:"verdict"`
}

// SampleExplorerLight 是一次短采样，用于界面上"现在卡不卡"的即时读数。
func SampleExplorerLight() (ExplorerNow, error) {
	st, err := SampleExplorer(700)
	if err != nil {
		return ExplorerNow{}, err
	}

	procs, _ := ProcessList()
	var threads int
	for _, p := range procs {
		if equalFold(p.Name, "explorer.exe") {
			threads += int(p.Threads)
		}
	}

	return ExplorerNow{
		Running:    true,
		PID:        st.PID,
		Threads:    threads,
		CPUPercent: st.CPUPercent,
		CPUCores:   st.CPUFraction,
		SampleMS:   st.SampleMS,
		Verdict:    verdictOf(st.CPUPercent, threads),
	}, nil
}

// verdictOf 把读数翻译成一句人话。
//
// 阈值来自实测：正常空闲的 explorer 是个位数百分比；
// 外壳扩展拖累时约 20%~40%；多核风暴那次超过 1000%。
func verdictOf(pct float64, threads int) string {
	switch {
	case pct >= 300:
		return "风暴：explorer 同时占满多个核，建议立刻重启资源管理器"
	case pct >= 60:
		return "严重：界面多半已经卡了，建议重启资源管理器"
	case pct >= 20:
		return "偏高：有外壳扩展在拖累，可考虑精简图标角标"
	case pct >= 8:
		return "略高：留意，但还不到要动手的程度"
	}
	if threads > 200 {
		return "正常：负载低，但线程数偏多（扩展装得有点多）"
	}
	return "正常"
}
