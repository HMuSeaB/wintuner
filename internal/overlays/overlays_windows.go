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
	"os/exec"
	"path/filepath"
	"sort"
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

// HasOwnerRunning 报告"这个处理器背后的程序在不在跑"。
//
// 用它来判断要不要建议清理：残留（Dead）是明确该清的；
// 而 DLL 存在、主人却没运行的那些，属于"注册着但用不上"——
// 它们照样会被 Explorer 加载，照样要等一个不存在的进程。
//
// 猜不出来时返回 true（当作主人还在），避免把有用的东西标成可清理。
func HasOwnerRunning(dll string) bool {
	if dll == "" {
		return true
	}
	name := strings.ToLower(filepath.Base(dll))
	return !ownerIdle(name)
}

// ownerIdle 按 DLL 名猜它属于哪个软件，再查那个软件在不在跑。
//
// 只对"需要常驻后台"的那类软件下判断。像 AutoCAD 的签名角标这种
// 装了就常驻的功能，主程序本来就不会一直开着——拿"进程没在跑"
// 去判它没用，只会误伤。
func ownerIdle(dllBase string) bool {
	probes := []struct {
		keyword string
		procs   []string
	}{
		// 这些是"有同步/云盘功能的客户端"，正常使用时会常驻托盘。
		// 没在跑基本等于没在用。
		{"yunshell", []string{"baidunetdisk", "yunguanjia"}},
		{"baidu", []string{"baidunetdisk", "yunguanjia"}},
		{"nutstore", []string{"nutstore"}},
		{"onedrive", []string{"onedrive"}},
		{"coresync", []string{"coresync", "creative cloud"}},
		{"adobe", []string{"coresync", "creative cloud"}},
		{"dropbox", []string{"dropbox"}},
		{"wps", []string{"wps", "wpscloudsvr"}},
		{"weiyun", []string{"weiyun"}},
		{"aliyundrive", []string{"aliyundrive", "adrive"}},
		{"quark", []string{"quark"}},
		// 注意：这里**不包含** acsign / autodesk 之类。
		// 它们是"随软件安装常驻"的功能型扩展，主程序不开也该在。
	}

	for _, p := range probes {
		if !strings.Contains(dllBase, p.keyword) {
			continue
		}
		return !anyProcRunning(p.procs)
	}
	return false // 认不出来就当它在跑
}

var procCache []string

func anyProcRunning(names []string) bool {
	if procCache == nil {
		procs, err := processNames()
		if err != nil {
			// 查不到就当它在跑，宁可漏报也不误报
			return true
		}
		procCache = procs
	}
	for _, p := range procCache {
		pl := strings.ToLower(p)
		for _, n := range names {
			if strings.Contains(pl, strings.ToLower(n)) {
				return true
			}
		}
	}
	return false
}

// List 列出所有已注册的处理器，外加已禁用的那些。
//
// 已禁用的必须一并返回：它们的存在形式是"注册表项已被摘掉 + 备份区里有记录"，
// 只枚举注册表的话根本看不见它们——用户清完就找不到还原入口了。
func List() ([]Item, error) {
	k, err := winreg.Open(winreg.LOCAL_MACHINE, baseKey)
	if err != nil {
		return nil, err
	}
	names, err := k.SubKeyNames()
	k.Close()
	if err != nil {
		return nil, err
	}

	disabled := loadBackups()
	var out []Item
	seen := map[string]bool{}

	for _, n := range names {
		seen[n] = true
		clsid, err := readDefault(winreg.LOCAL_MACHINE, baseKey+`\`+n)
		if err != nil {
			continue
		}
		out = append(out, buildItem(n, clsid, true))
	}

	// 补上只存在于备份区的（也就是被本工具禁用的）。
	for name, clsid := range disabled {
		if seen[name] {
			continue // 注册表里也有，以注册表为准
		}
		it := buildItem(name, clsid, false)
		it.Enabled = false
		out = append(out, it)
	}

	// 排序让输出稳定：启用的在前，同类按名字。
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Enabled != out[j].Enabled {
			return out[i].Enabled
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// buildItem 组装一条记录，并判定它是不是"死的"。
func buildItem(name, clsid string, enabled bool) Item {
	dll := resolveCLSID(clsid)
	dead := dll != "" && !dllExists(dll)
	vendor := vendorOf(dll)
	if dll == "" || dead {
		vendor = "残留（已失效）"
	}
	return Item{
		Name:    name,
		CLSID:   clsid,
		DLL:     dll,
		Vendor:  vendor,
		Enabled: enabled,
		Builtin: isBuiltin(dll),
		Dead:    dll == "" || dead,
	}
}

// dllExists 判断注册表里记的路径是否真的指向一个文件。
//
// 不能直接 os.Stat：有些扩展（实测 AutoCAD 的 AcSignIcon.dll）在注册表里
// 只写了**文件名**，没有目录。os.Stat 会在当前工作目录下找，必然找不到，
// 于是把好好的扩展误判成"残留"。裸文件名要走 PATH 查找。
func dllExists(p string) bool {
	if p == "" {
		return false
	}
	// 含分隔符的是完整路径，直接查。
	if strings.ContainsAny(p, `\/`) {
		_, err := os.Stat(p)
		return err == nil
	}
	// 裸文件名：在 PATH 与系统目录里找。
	if _, err := exec.LookPath(p); err == nil {
		return true
	}
	for _, dir := range []string{
		filepath.Join(os.Getenv("SystemRoot"), "System32"),
		os.Getenv("SystemRoot"),
		filepath.Join(os.Getenv("ProgramFiles"), "Common Files", "Adobe", "Acrobat"),
	} {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			return true
		}
	}
	return false
}

// loadBackups 读出备份区里所有被禁用的处理器。
func loadBackups() map[string]string {
	out := map[string]string{}
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
		v, err := k.GetString(n)
		if err != nil {
			continue
		}
		var s string
		if err := json.Unmarshal([]byte(v), &s); err == nil {
			out[n] = s
		}
	}
	return out
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
