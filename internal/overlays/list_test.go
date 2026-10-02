package overlays

import (
	"runtime"
	"testing"
)

// TestListIncludesDisabled 确认被禁用的处理器仍然能被列出来。
//
// 这是一个真实 bug 的回归测试：早先 List 只枚举注册表，
// 而被禁用的项恰恰是从注册表里摘掉的——结果就是"清完之后
// 界面上再也看不到它们，也就没法还原"。
func TestListIncludesDisabled(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows")
	}

	items, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	backups := loadBackups()
	t.Logf("注册表+备份合计 %d 条，备份区 %d 条", len(items), len(backups))

	if len(backups) == 0 {
		t.Skip("本机备份区是空的，这条断言没有意义")
	}

	// 备份区里每一条都必须能在 List 的结果里找到。
	listed := map[string]bool{}
	for _, it := range items {
		listed[it.Name] = true
	}
	for name := range backups {
		if !listed[name] {
			t.Errorf("备份区里的 %q 没有出现在 List 结果里——用户将无法还原它", name)
		}
	}

	off := 0
	for _, it := range items {
		if !it.Enabled {
			off++
		}
	}
	if off != len(backups) {
		t.Errorf("已禁用计数 %d 与备份区条数 %d 不一致", off, len(backups))
	}
	t.Logf("其中已禁用 %d 条", off)
}
