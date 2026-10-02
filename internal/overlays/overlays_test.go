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
		if it.Builtin && it.Dead {
			t.Errorf("系统自带组件 %q 被标成了残留", it.Name)
		}
		// 残留的必须有空 DLL：否则判定依据不成立。
		if it.Dead && it.DLL != "" {
			t.Errorf("%q 标为残留但 DLL 有值（%s）", it.Name, it.DLL)
		}
		// 非残留的必须有非空 DLL。
		if !it.Dead && it.DLL == "" {
			t.Errorf("%q 未标为残留但 DLL 为空", it.Name)
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
		t.Logf("%-30s %-14s builtin=%v dead=%v", strings.TrimSpace(it.Name), it.Vendor, it.Builtin, it.Dead)
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
