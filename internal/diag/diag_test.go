package diag

import (
	"runtime"
	"testing"
)

func TestProcessListNamesAreSane(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("诊断仅支持 Windows")
	}
	procs, err := ProcessList()
	if err != nil {
		t.Fatalf("ProcessList: %v", err)
	}
	if len(procs) < 20 {
		t.Fatalf("只列到 %d 个进程，快照或结构体偏移多半有问题", len(procs))
	}

	// 名字必须是正常的 .exe，不能有控制字符或替换字符。
	// PID=0 是合法的：[System Process]（空闲进程）本来就是 0。
	bad := 0
	for _, p := range procs {
		if p.Name == "" {
			bad++
			continue
		}
		for _, r := range p.Name {
			if r < 0x20 || r == 0xFFFD {
				bad++
				break
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d/%d 个进程名不可读，结构体偏移可能有误", bad, len(procs))
		for i, p := range procs {
			if i < 5 {
				t.Logf("  %q pid=%d threads=%d", p.Name, p.PID, p.Threads)
			}
		}
	}

	t.Logf("共 %d 个进程", len(procs))
	found := false
	for _, p := range procs {
		if equalFold(p.Name, "explorer.exe") {
			found = true
			t.Logf("  explorer.exe pid=%d threads=%d", p.PID, p.Threads)
		}
	}
	if !found {
		t.Error("没找到 explorer.exe")
	}
}

func TestSampleExplorer(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("诊断仅支持 Windows")
	}
	st, err := SampleExplorer(1500)
	if err != nil {
		t.Fatalf("SampleExplorer: %v", err)
	}
	if st.SampleMS < 1000 {
		t.Errorf("采样时长只有 %dms，太短", st.SampleMS)
	}
	if st.CPUFraction < 0 || st.CPUFraction > 16 {
		t.Errorf("CPU 占比 %.3f 不合理", st.CPUFraction)
	}
	t.Logf("explorer pid=%d 占 %.1f%%（%.2f 核） 采样 %dms",
		st.PID, st.CPUPercent, st.CPUFraction, st.SampleMS)
}

func TestRepeatedProcesses(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("诊断仅支持 Windows")
	}
	groups, err := RepeatedProcesses(6)
	if err != nil {
		t.Fatalf("RepeatedProcesses: %v", err)
	}
	for _, g := range groups {
		t.Logf("  %s x%d  CPU %.1fs", g.Name, g.Count, g.CPUSecs)
	}
}
