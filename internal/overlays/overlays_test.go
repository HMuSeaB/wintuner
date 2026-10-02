package overlays

import (
	"runtime"
	"strings"
	"testing"
)

// TestListClassifiesCorrectly 检查分类结果自洽。
//
// 这是"一键清理"的地基：如果分类错了，界面会建议用户清掉不该清的东西。
func TestListClassifiesCorrectly(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows")
	}

	items, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("一条都没读到，注册表路径或枚举逻辑多半有问题")
	}

	var builtin, dead, orphan, healthy int
	for _, it := range items {
		// 系统自带的绝不能被标成待清理——清了会出问题。
		if it.Builtin {
			if it.Dead {
				t.Errorf("系统自带组件 %q 被标成了残留", it.Name)
			}
			if !HasOwnerRunning(it.DLL) {
				t.Errorf("系统自带组件 %q 被标成了孤儿扩展", it.Name)
			}
		}
		// "残留"的判据是"CLSID 解析不出路径，或路径指向的文件不在了"。
		// 注意不能断言"残留 ⇒ DLL 为空"：注册表里可能记着一个相对路径
		// （实测 AutoCAD 的 AcSignIcon.dll 就是裸文件名），
		// 那种情况下 DLL 字段有值但文件确实找不到，仍然算残留。
		if it.Dead && it.DLL != "" && dllExists(it.DLL) {
			t.Errorf("%q 标为残留，但 %s 实际存在", it.Name, it.DLL)
		}
		// 非残留的必须真的能找到文件。
		if !it.Dead && it.DLL != "" && !dllExists(it.DLL) {
			t.Errorf("%q 未标为残留，但 %s 找不到", it.Name, it.DLL)
		}
		if !it.Dead && it.DLL == "" {
			t.Errorf("%q 未标为残留却没有 DLL", it.Name)
		}

		switch {
		case it.Builtin:
			builtin++
		case it.Dead:
			dead++
		case !HasOwnerRunning(it.DLL):
			orphan++
		default:
			healthy++
		}
		t.Logf("%-30s %-22s enabled=%-5v builtin=%-5v dead=%v",
			strings.TrimSpace(it.Name), it.Vendor, it.Enabled, it.Builtin, it.Dead)
	}
	t.Logf("系统自带 %d / 残留 %d / 主人没运行 %d / 正常 %d", builtin, dead, orphan, healthy)
}

// TestHasOwnerRunningDoesNotOverreport 确认认不出来的 DLL 不会被判成"主人没在跑"。
//
// 误报会诱导用户关掉有用的东西，比漏报严重得多。
func TestHasOwnerRunningDoesNotOverreport(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows")
	}
	// 这些都不该被判定为"主人没在运行"
	for _, dll := range []string{
		`C:\Windows\System32\EhStorShell.dll`,
		`C:\SomeVendor\UnknownShellExt.dll`,
		"",
	} {
		if !HasOwnerRunning(dll) {
			t.Errorf("%q 被误判为孤儿扩展", dll)
		}
	}
}
