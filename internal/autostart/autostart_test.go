package autostart

import (
	"runtime"
	"strings"
	"testing"
)

func TestScanReportsItems(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("启动项管理仅支持 Windows")
	}

	items, err := Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("一条启动项都没扫到，扫描逻辑可能坏了")
	}

	// ID 必须唯一：界面靠它定位条目，重复会导致禁用时张冠李戴。
	seen := map[string]string{}
	for _, it := range items {
		if it.ID == "" {
			t.Errorf("存在空 ID 的条目: %+v", it)
			continue
		}
		if prev, ok := seen[it.ID]; ok {
			t.Errorf("ID 重复: %q（%q 与 %q）", it.ID, prev, it.Name)
		}
		seen[it.ID] = it.Name
	}

	t.Logf("扫到 %d 条启动项", len(items))
	for _, it := range items {
		t.Logf("  [%s] %s | %s", it.Source, truncate(it.Name, 40), truncate(it.Command, 60))
	}
}

func TestParkingDirIsUsable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("启动项管理仅支持 Windows")
	}
	dir := ParkingDir()
	if dir == "" {
		t.Fatal("ParkingDir 返回空")
	}
	t.Logf("禁用项存放目录: %s", dir)
}

func TestSourceLabelsCoverAll(t *testing.T) {
	for _, s := range []Source{
		SourceRunMachine, SourceRunMachine32, SourceRunUser,
		SourceStartupUser, SourceStartupAll, SourceService, SourceTask,
	} {
		if s.label() == string(s) {
			t.Errorf("来源 %q 缺少可读名称", s)
		}
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
