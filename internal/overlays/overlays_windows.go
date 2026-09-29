//go:build windows

// Package overlays 管理 Explorer 的图标叠加处理器。
//
// 处理器越多，Explorer 每画一个图标要问的对象就越多；而 Windows 实际只
// 采用排序最靠前的 15 个，多出来的纯属拖累。这类扩展还常常在自家后台服务
// 没运行时去连它、卡在等待上——所以"禁用不用的"既提速也治卡。
package overlays

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/HMuSeaB/wintuner/internal/winreg"
)

// baseKey 是叠加处理器的注册位置。
const baseKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\ShellIconOverlayIdentifiers`

// backupKey 存放被禁用的处理器，用于还原。
const backupKey = `SOFTWARE\WinTuner\DisabledOverlays`

// Item 是一个图标叠加处理器。
type Item struct {
	Name    string `json:"name"`
	CLSID   string `json:"clsid"`
	DLL     string `json:"dll"`
	Vendor  string `json:"vendor"`
	Enabled bool   `json:"enabled"`
	// Builtin 为 true 表示是 Windows 自带的，界面上要提示别动。
	Builtin bool `json:"builtin"`
	// Dead 为 true 表示这条注册已经指向不存在的东西：CLSID 查不到，
	// 或者 DLL 文件已经不在。它们仍然占着名额、仍然会被 Explorer 尝试加载，
	// 却永远画不出角标——是最该清理的一类。
	Dead bool `json:"dead"`
}

// List 列出所有已注册的处理器。
func List() ([]Item, error) {
	k, err := winreg.Open(winreg.LOCAL_MACHINE, baseKey)
	if err != nil {
		return nil, err
	}
	defer k.Close()

	names, err := k.SubKeyNames()
	if err != nil {
		return nil, err
	}

	disabled := disabledSet()
	var out []Item
	for _, n := range names {
		clsid, err := readDefault(winreg.LOCAL_MACHINE, baseKey+`\`+n)
		if err != nil {
			continue
		}
		dll := resolveCLSID(clsid)
		dead := dll == ""
		if !dead {
			// DLL 记在注册表里但文件已被删（软件卸载没清干净），同样是死的。
			if _, err := os.Stat(dll); err != nil {
				dead = true
			}
		}
		vendor := vendorOf(dll)
		if dead {
			vendor = "残留（已失效）"
		}
		out = append(out, Item{
			Name:    n,
			CLSID:   clsid,
			DLL:     dll,
			Vendor:  vendor,
			Enabled: !disabled[n],
			Builtin: isBuiltin(dll),
			Dead:    dead,
		})
	}
	return out, nil
}

// Disable 禁用一个处理器：先把 CLSID 写进备份区，再删掉注册项。
func Disable(name string) error {
	if isBuiltin(resolveCLSID(mustDefault(name))) {
		return fmt.Errorf("%q 是系统自带组件，不建议禁用", name)
	}
	clsid, err := readDefault(winreg.LOCAL_MACHINE, baseKey+`\`+name)
	if err != nil {
		return fmt.Errorf("读不到 %q: %w", name, err)
	}
	if err := saveBackup(name, clsid); err != nil {
		return err
	}

	// 删子键需要 HKLM 写权限；失败就把备份撤掉，
	// 免得留下一条"记了却还原不了"的记录。
	if err := winreg.DeleteTree(winreg.LOCAL_MACHINE, baseKey+`\`+name); err != nil {
		_ = dropBackup(name)
		return err
	}
	return nil
}

// Enable 按备份还原一个处理器。
func Enable(name string) error {
	clsid, ok := getBackup(name)
	if !ok {
		return fmt.Errorf("没有 %q 的禁用记录，无法还原", name)
	}
	k, err := winreg.Create(winreg.LOCAL_MACHINE, baseKey+`\`+name)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetString("", clsid); err != nil {
		return err
	}
	return dropBackup(name)
}

// ---------- 备份区 ----------

func disabledSet() map[string]bool {
	out := map[string]bool{}
	k, err := winreg.Open(winreg.CURRENT_USER, backupKey)
	if err != nil {
		return out
	}
	defer k.Close()
	names, err := k.ValueNames()
	if err != nil {
		return out
	}
	for _, n := range names {
		out[n] = true
	}
	return out
}

func getBackup(name string) (string, bool) {
	k, err := winreg.Open(winreg.CURRENT_USER, backupKey)
	if err != nil {
		return "", false
	}
	defer k.Close()
	v, err := k.GetString(name)
	if err != nil {
		return "", false
	}
	var s string
	if err := json.Unmarshal([]byte(v), &s); err != nil {
		return "", false
	}
	return s, true
}

func saveBackup(name, clsid string) error {
	k, err := winreg.Create(winreg.CURRENT_USER, backupKey)
	if err != nil {
		return fmt.Errorf("无法写入备份区: %w", err)
	}
	defer k.Close()
	raw, err := json.Marshal(clsid)
	if err != nil {
		return err
	}
	return k.SetString(name, string(raw))
}

func dropBackup(name string) error {
	k, err := winreg.Open(winreg.CURRENT_USER, backupKey)
	if err != nil {
		return nil // 备份区不存在说明本来就没记录
	}
	defer k.Close()
	return k.DeleteValue(name)
}

// ---------- 辅助 ----------

// mustDefault 读某个处理器的 CLSID，读不到返回空串。
func mustDefault(name string) string {
	v, err := readDefault(winreg.LOCAL_MACHINE, baseKey+`\`+name)
	if err != nil {
		return ""
	}
	return v
}

// readDefault 读一个键的默认值。
func readDefault(root winreg.Root, sub string) (string, error) {
	k, err := winreg.Open(root, sub)
	if err != nil {
		return "", err
	}
	defer k.Close()
	return k.GetString("")
}

// resolveCLSID 把 CLSID 换成它背后 DLL 的路径。
//
// 同时在 HKLM 与 HKCU 找：有些客户端（如 OneDrive）把 CLSID 注册在用户级，
// 只在 HKLM 查会得到空值，然后被误判成"DLL 缺失"。
func resolveCLSID(clsid string) string {
	if clsid == "" {
		return ""
	}
	for _, root := range []winreg.Root{winreg.LOCAL_MACHINE, winreg.CURRENT_USER} {
		if v, err := readDefault(root, `SOFTWARE\Classes\CLSID\`+clsid+`\InprocServer32`); err == nil && v != "" {
			return os.ExpandEnv(v)
		}
	}
	return ""
}

// isBuiltin 判断是不是 Windows 自带组件。
func isBuiltin(dll string) bool {
	if dll == "" {
		return false
	}
	low := strings.ToLower(dll)
	if strings.HasPrefix(low, "%systemroot%") {
		return true
	}
	sys := strings.ToLower(os.Getenv("SystemRoot"))
	if sys != "" && strings.HasPrefix(low, sys) {
		return true
	}
	return strings.HasPrefix(low, `c:\windows\`)
}

// vendorOf 从 DLL 路径猜一个来源名，用于界面分组与提示。
func vendorOf(dll string) string {
	if dll == "" {
		return "未知"
	}
	low := strings.ToLower(dll)
	switch {
	case strings.Contains(low, "baidunetdisk"), strings.Contains(low, "yunshell"):
		return "百度网盘"
	case strings.Contains(low, "nutstore"):
		return "坚果云"
	case strings.Contains(low, "onedrive"):
		return "OneDrive"
	case strings.Contains(low, "coresync"), strings.Contains(low, "adobe"):
		return "Adobe Creative Cloud"
	case strings.Contains(low, "autodesk"), strings.Contains(low, "acsign"):
		return "Autodesk"
	case strings.Contains(low, "wps"), strings.Contains(low, "kingsoft"):
		return "WPS"
	case strings.Contains(low, "sangfor"):
		return "深信服"
	case isBuiltin(dll):
		return "Windows"
	}
	return filepath.Base(filepath.Dir(dll))
}
