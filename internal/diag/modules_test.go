package diag

import (
	"runtime"
	"strings"
	"testing"
)

func TestExplorerModulesListsThirdParty(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows")
	}

	mods, err := ExplorerModules()
	if err != nil {
		t.Fatalf("ExplorerModules: %v", err)
	}
	if len(mods) < 20 {
		t.Fatalf("只拿到 %d 个模块，EnumProcessModules 的调用或缓冲大小多半有问题", len(mods))
	}

	// 路径必须可读：出现乱码说明 GetModuleFileNameEx 的缓冲或长度用法不对。
	bad := 0
	for _, m := range mods {
		if m.Name == "" || strings.ContainsRune(m.Name, 0xFFFD) {
			bad++
		}
		if !strings.Contains(m.Path, `\`) {
			bad++
		}
	}
	if bad > 0 {
		t.Errorf("%d/%d 个模块名或路径不可读", bad, len(mods))
	}

	third := 0
	var orphans []string
	for _, m := range mods {
		if m.Builtin {
			continue
		}
		third++
		if m.Orphan {
			orphans = append(orphans, m.Vendor+" / "+m.Name)
		}
	}

	t.Logf("共 %d 个模块，其中第三方 %d 个", len(mods), third)
	for _, m := range mods {
		if m.Builtin {
			continue
		}
		mark := ""
		if m.Orphan {
			mark = "  <- 主人没在运行"
		}
		t.Logf("  %-38s %-12s%s", m.Name, m.Vendor, mark)
	}
	if len(orphans) > 0 {
		t.Logf("孤儿扩展 %d 个: %s", len(orphans), strings.Join(orphans, ", "))
	}
}

func TestOwnerNotRunningDoesNotOverreport(t *testing.T) {
	// 认不出来的 DLL 不该被标成"主人没在跑"——误报会诱导用户关掉有用的东西。
	cases := []string{
		"kernel32.dll",
		"some-random-shell-ext.dll",
		"nvui.dll",
	}
	for _, c := range cases {
		if ownerNotRunning(c) {
			t.Errorf("%s 被误判为孤儿扩展", c)
		}
	}
}
