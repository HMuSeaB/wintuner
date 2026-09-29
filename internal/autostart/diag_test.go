package autostart

import (
	"runtime"
	"testing"

	"github.com/HMuSeaB/wintuner/internal/winreg"
)

func TestDiagServices(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip()
	}
	root, err := winreg.Open(winreg.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services`)
	if err != nil {
		t.Fatalf("打开 Services 失败: %v", err)
	}
	defer root.Close()

	names, err := root.SubKeyNames()
	t.Logf("Services 子键数=%d err=%v", len(names), err)
	if len(names) > 0 {
		t.Logf("  前 5 个: %v", names[:min(5, len(names))])
	}

	ok, bad := 0, 0
	for i, n := range names {
		if i >= 30 {
			break
		}
		k, err := winreg.Open(winreg.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+n)
		if err != nil {
			bad++
			continue
		}
		start, err := k.GetDword("Start")
		k.Close()
		if err != nil {
			bad++
			if bad <= 3 {
				t.Logf("  GetDword 失败 %s: %v", n, err)
			}
			continue
		}
		ok++
		if ok <= 5 {
			t.Logf("  %s Start=%d", n, start)
		}
	}
	t.Logf("前 30 个里: 读到 %d 个 Start，失败 %d 个", ok, bad)

	items, err := scanServices()
	t.Logf("scanServices 返回 %d 条, err=%v", len(items), err)
}

func TestDiagTasks(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip()
	}
	items, err := scanTasks()
	t.Logf("scanTasks 返回 %d 条, err=%v", len(items), err)
	for i, it := range items {
		if i >= 5 {
			break
		}
		t.Logf("  %s", it.Name)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
