//go:build windows

// Package autostart 管理开机自启动项。
//
// 设计上最重要的一条：**禁用必须可还原**。
// 因此禁用不是"删掉"，而是把原始信息写进备份区再从原位置摘除；
// 启用就是按备份原样放回去。备份区在 HKCU 下，只存本工具自己写的东西。
package autostart

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/HMuSeaB/wintuner/internal/winreg"
)

// Source 标识一条启动项的来源。
type Source string

const (
	SourceRunMachine   Source = "run-hklm"     // HKLM\...\Run
	SourceRunMachine32 Source = "run-hklm32"   // HKLM\...\WOW6432Node\...\Run
	SourceRunUser      Source = "run-hkcu"     // HKCU\...\Run
	SourceStartupUser  Source = "startup-user" // 当前用户启动文件夹
	SourceStartupAll   Source = "startup-all"  // 所有用户启动文件夹
	SourceService      Source = "service"      // 开机自启的服务
	SourceTask         Source = "task"         // 已启用的计划任务
)

// label 给用户看的来源名。
func (s Source) label() string {
	switch s {
	case SourceRunMachine:
		return "注册表·所有用户"
	case SourceRunMachine32:
		return "注册表·所有用户(32位)"
	case SourceRunUser:
		return "注册表·当前用户"
	case SourceStartupUser:
		return "启动文件夹·当前用户"
	case SourceStartupAll:
		return "启动文件夹·所有用户"
	case SourceService:
		return "服务"
	case SourceTask:
		return "计划任务"
	}
	return string(s)
}

// Item 是一条启动项。
type Item struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Source  Source `json:"source"`
	Origin  string `json:"origin"`  // 来源的可读说明
	Command string `json:"command"` // 命令、路径或任务定义
	Detail  string `json:"detail"`  // 补充信息（服务状态等）
	Enabled bool   `json:"enabled"`
	// Risk 是给用户的风险提示，不是判定依据。
	Risk string `json:"risk"`
}

// backupKey 是存放禁用记录的键。
const backupKey = `SOFTWARE\WinTuner\Disabled`

// backup 记录一条被禁用项的原始信息，用于还原。
type backup struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Source  Source `json:"source"`
	Command string `json:"command"`
	// Expand 为 true 表示原值是 REG_EXPAND_SZ，还原时必须用同一类型，
	// 否则命令里的 %APPDATA% 之类不会被展开，启动项会静默失效。
	Expand bool   `json:"expand"`
	Start  uint32 `json:"start"` // 仅服务：原来的启动类型
}

var ErrNotFound = errors.New("autostart: 找不到该启动项")

// Scan 列出所有来源的启动项。
//
// 单个来源读失败不影响其它来源——比如没有管理员权限时 HKLM 的计划任务
// 可能读不到，此时仍然要把能读到的列出来，而不是整体报错。
func Scan() ([]Item, error) {
	var items []Item
	var errs []string

	add := func(got []Item, err error) {
		if err != nil {
			errs = append(errs, err.Error())
			return
		}
		items = append(items, got...)
	}

	add(scanRun(winreg.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`, SourceRunMachine))
	add(scanRun(winreg.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Run`, SourceRunMachine32))
	add(scanRun(winreg.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`, SourceRunUser))
	add(scanStartupFolder(startupDirUser(), SourceStartupUser))
	add(scanStartupFolder(startupDirAll(), SourceStartupAll))
	add(scanServices())
	add(scanTasks())

	// 标出哪些已被本工具禁用：它们不在原位置了，但备份区里有记录。
	markDisabled(items)

	if len(items) == 0 && len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	return items, nil
}

// markDisabled 把备份区里记录的项补进列表，标记为已禁用。
//
// 这样界面上"我禁用过什么"仍然可见——否则禁用后它就从列表里消失了，
// 用户既看不到也没法还原，等于把东西弄丢了。
func markDisabled(items []Item) {
	saved, err := loadBackups()
	if err != nil {
		return
	}
	for _, b := range saved {
		items = append(items, Item{
			ID:      b.ID,
			Name:    b.Name,
			Source:  b.Source,
			Origin:  b.Source.label() + "（已禁用）",
			Command: b.Command,
			Enabled: false,
			Risk:    riskOf(b.Command),
		})
	}
}

func scanRun(root winreg.Root, sub string, src Source) ([]Item, error) {
	k, err := winreg.Open(root, sub)
	if err != nil {
		return nil, err
	}
	defer k.Close()

	names, err := k.ValueNames()
	if err != nil {
		return nil, err
	}
	var out []Item
	for _, n := range names {
		if n == "" {
			continue
		}
		cmd, err := k.GetString(n)
		if err != nil {
			continue // 单个值读不出来就跳过，别让一条坏数据吞掉整个列表
		}
		out = append(out, Item{
			ID:      string(src) + "|" + n,
			Name:    n,
			Source:  src,
			Origin:  src.label(),
			Command: cmd,
			Enabled: true,
			Risk:    riskOf(cmd),
		})
	}
	return out, nil
}

// startupExts 是启动文件夹里真正会被执行的扩展名。
//
// 不做这个过滤会把 desktop.ini、Textify.ini 这类配置文件也列出来——
// 它们不是启动项，列出来只会让用户对着一份假清单做决定。
var startupExts = map[string]bool{
	".lnk": true, ".exe": true, ".cmd": true, ".bat": true,
	".vbs": true, ".js": true, ".jse": true, ".wsh": true, ".wsf": true,
}

func scanStartupFolder(dir string, src Source) ([]Item, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Item
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !startupExts[ext] {
			continue
		}
		out = append(out, Item{
			ID:      string(src) + "|" + e.Name(),
			Name:    e.Name(),
			Source:  src,
			Origin:  src.label(),
			Command: filepath.Join(dir, e.Name()),
			Enabled: true,
			Risk:    riskOf(e.Name()),
		})
	}
	return out, nil
}

// scanServices 列出开机自启的服务。
//
// 只读注册表里的 Start 值而不用服务控制管理器，避免引入另一套 API；
// Start=2 是自启，3 是按需，4 是禁用。
func scanServices() ([]Item, error) {
	root, err := winreg.Open(winreg.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services`)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	names, err := root.SubKeyNames()
	if err != nil {
		return nil, err
	}
	var out []Item
	for _, n := range names {
		k, err := winreg.Open(winreg.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+n)
		if err != nil {
			continue
		}
		start, err := k.GetDword("Start")
		k.Close()
		if err != nil || start != 2 {
			continue
		}
		out = append(out, Item{
			ID:      string(SourceService) + "|" + n,
			Name:    n,
			Source:  SourceService,
			Origin:  SourceService.label(),
			Command: `HKLM\SYSTEM\CurrentControlSet\Services\` + n,
			Detail:  "Start=2（开机自启）",
			Enabled: true,
			Risk:    "服务禁用后依赖它的功能可能失效",
		})
	}
	return out, nil
}

// scanTasks 用 schtasks 列出已启用的计划任务。
//
// 走命令行而不是 COM：COM 需要额外接口，而 schtasks 不需要管理员权限
// 就能列出当前用户的任务。
//
// 刻意不用 /v（详细模式）：列数会随系统版本变化，靠下标取值很脆。
// 非详细模式固定三列：任务名 / 下次运行时间 / 状态。
func scanTasks() ([]Item, error) {
	raw, err := exec.Command("schtasks", "/query", "/fo", "CSV", "/nh").Output()
	if err != nil {
		return nil, fmt.Errorf("schtasks 列任务失败: %w", err)
	}
	// 中文系统上 schtasks 输出 GBK，Go 按 UTF-8 解码会得到乱码字符。
	// 这里只用到任务名与状态两列，任务名基本是 ASCII，
	// 因此按 latin-1 容错解码即可，不额外引入编码转换依赖。
	records, err := csv.NewReader(newReaderTolerant(raw)).ReadAll()
	if err != nil && len(records) == 0 {
		return nil, fmt.Errorf("解析 schtasks 输出失败: %w", err)
	}

	var out []Item
	for _, r := range records {
		if len(r) < 3 {
			continue
		}
		name, status := strings.TrimSpace(r[0]), strings.TrimSpace(r[2])
		if name == "" || strings.EqualFold(name, "TaskName") {
			continue
		}
		// 状态列在中文系统上是"已就绪"，英文是 "Ready"。
		if !strings.EqualFold(status, "Ready") && !strings.Contains(status, "就绪") {
			continue
		}
		out = append(out, Item{
			ID:      string(SourceTask) + "|" + name,
			Name:    name,
			Source:  SourceTask,
			Origin:  SourceTask.label(),
			Command: name,
			Enabled: true,
		})
	}
	return out, nil
}

// Disable 禁用一条启动项，并把原始信息写进备份区。
func Disable(id string) error {
	src, name, err := splitID(id)
	if err != nil {
		return err
	}
	if hasBackup(id) {
		return fmt.Errorf("该项已处于禁用状态")
	}

	b := backup{ID: id, Name: name, Source: src}

	switch src {
	case SourceRunMachine, SourceRunMachine32, SourceRunUser:
		root, sub, ok := runKeyOf(src)
		if !ok {
			return fmt.Errorf("未知的注册表来源 %q", src)
		}
		k, err := winreg.OpenWrite(root, sub)
		if err != nil {
			return err
		}
		defer k.Close()
		cmd, err := k.GetString(name)
		if err != nil {
			return fmt.Errorf("读不到该项: %w", err)
		}
		b.Command = cmd
		b.Expand = strings.Contains(cmd, "%")
		// 先写备份再删原值：顺序反了就会丢数据。
		if err := saveBackup(b); err != nil {
			return err
		}
		if err := k.DeleteValue(name); err != nil {
			// 删失败就把备份撤掉，避免留下一条还原不了的记录。
			_ = dropBackup(id)
			return err
		}

	case SourceStartupUser, SourceStartupAll:
		dir := startupDirOf(src)
		from := filepath.Join(dir, name)
		if _, err := os.Lstat(from); err != nil {
			return fmt.Errorf("找不到 %s: %w", from, err)
		}
		to := filepath.Join(ParkingDir(), name)
		b.Command = from
		if err := saveBackup(b); err != nil {
			return err
		}
		if err := os.Rename(from, to); err != nil {
			_ = dropBackup(id)
			return fmt.Errorf("移动到 %s 失败: %w", to, err)
		}

	case SourceService:
		k, err := winreg.OpenWrite(winreg.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+name)
		if err != nil {
			return err
		}
		defer k.Close()
		start, err := k.GetDword("Start")
		if err != nil {
			return err
		}
		b.Start = start
		b.Command = `HKLM\SYSTEM\CurrentControlSet\Services\` + name
		if err := saveBackup(b); err != nil {
			return err
		}
		if err := k.SetDword("Start", 4); err != nil { // 4 = 禁用
			_ = dropBackup(id)
			return err
		}

	case SourceTask:
		b.Command = name
		if err := saveBackup(b); err != nil {
			return err
		}
		if err := runSchtasks("/change", "/disable", "/tn", name); err != nil {
			_ = dropBackup(id)
			return err
		}

	default:
		return fmt.Errorf("不支持的来源 %q", src)
	}
	return nil
}

// Enable 按备份还原一条被禁用的启动项。
func Enable(id string) error {
	b, ok := getBackup(id)
	if !ok {
		return ErrNotFound
	}

	switch b.Source {
	case SourceRunMachine, SourceRunMachine32, SourceRunUser:
		root, sub, ok := runKeyOf(b.Source)
		if !ok {
			return fmt.Errorf("未知的注册表来源 %q", b.Source)
		}
		k, err := openRunKeyForWrite(root, sub)
		if err != nil {
			return err
		}
		defer k.Close()
		if b.Expand {
			err = k.SetExpandString(b.Name, b.Command)
		} else {
			err = k.SetString(b.Name, b.Command)
		}
		if err != nil {
			return err
		}

	case SourceStartupUser, SourceStartupAll:
		from := filepath.Join(ParkingDir(), b.Name)
		to := b.Command // 备份里存的就是原绝对路径
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("从 %s 还原失败: %w", from, err)
		}

	case SourceService:
		k, err := winreg.OpenWrite(winreg.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+b.Name)
		if err != nil {
			return err
		}
		defer k.Close()
		if err := k.SetDword("Start", b.Start); err != nil {
			return err
		}

	case SourceTask:
		if err := runSchtasks("/change", "/enable", "/tn", b.Command); err != nil {
			return err
		}

	default:
		return fmt.Errorf("不支持的来源 %q", b.Source)
	}

	// 还原成功才清掉备份，失败时记录还在，可以重试。
	return dropBackup(id)
}

// ---------- 备份区 ----------

func loadBackups() ([]backup, error) {
	k, err := winreg.Open(winreg.CURRENT_USER, backupKey)
	if err != nil {
		return nil, err
	}
	defer k.Close()

	names, err := k.ValueNames()
	if err != nil {
		return nil, err
	}
	var out []backup
	for _, n := range names {
		raw, err := k.GetString(n)
		if err != nil {
			continue
		}
		var b backup
		if err := json.Unmarshal([]byte(raw), &b); err != nil {
			continue
		}
		out = append(out, b)
	}
	return out, nil
}

func getBackup(id string) (backup, bool) {
	all, err := loadBackups()
	if err != nil {
		return backup{}, false
	}
	for _, b := range all {
		if b.ID == id {
			return b, true
		}
	}
	return backup{}, false
}

func hasBackup(id string) bool {
	_, ok := getBackup(id)
	return ok
}

func saveBackup(b backup) error {
	k, err := winreg.Create(winreg.CURRENT_USER, backupKey)
	if err != nil {
		return fmt.Errorf("无法写入备份区: %w", err)
	}
	defer k.Close()

	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	// 值名里不能有反斜杠，用 ID 直接做名字前先替换掉。
	name := strings.ReplaceAll(b.ID, `\`, "/")
	return k.SetString(name, string(raw))
}

func dropBackup(id string) error {
	k, err := winreg.Open(winreg.CURRENT_USER, backupKey)
	if err != nil {
		return nil // 备份区不存在说明本来就没记录，不算错误
	}
	defer k.Close()
	name := strings.ReplaceAll(id, `\`, "/")
	return k.DeleteValue(name)
}

// ---------- 辅助 ----------

func splitID(id string) (Source, string, error) {
	i := strings.Index(id, "|")
	if i <= 0 {
		return "", "", fmt.Errorf("启动项标识非法: %q", id)
	}
	return Source(id[:i]), id[i+1:], nil
}

func runKeyOf(src Source) (winreg.Root, string, bool) {
	switch src {
	case SourceRunMachine:
		return winreg.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`, true
	case SourceRunMachine32:
		return winreg.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Run`, true
	case SourceRunUser:
		return winreg.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`, true
	}
	return 0, "", false
}

// openRunKeyForWrite 打开 Run 键用于写入，不存在则创建。
//
// 不能只用 OpenWrite：全新用户配置档上 HKCU\...\Run 可能压根不存在，
// 那时还原会失败——用户禁用了却启不回来，是这个工具最不能犯的错。
func openRunKeyForWrite(root winreg.Root, sub string) (*winreg.Key, error) {
	if k, err := winreg.OpenWrite(root, sub); err == nil {
		return k, nil
	}
	return winreg.Create(root, sub)
}

func startupDirOf(src Source) string {
	if src == SourceStartupAll {
		return startupDirAll()
	}
	return startupDirUser()
}

func runSchtasks(args ...string) error {
	cmd := exec.Command("schtasks", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks %s 失败: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// newReaderTolerant 把任意字节流按 latin-1 解码成 UTF-8 再交给 csv 解析。
//
// schtasks 在中文系统上输出 GBK，直接按 UTF-8 解码会出现非法字节，
// encoding/csv 读到一半就会报错。latin-1 把每个字节映射成一个码点，
// 不会失败，也不改变 ASCII 字符——而我们只关心任务名与状态里的 ASCII 部分。
func newReaderTolerant(raw []byte) io.Reader {
	var buf bytes.Buffer
	for _, b := range raw {
		buf.WriteRune(rune(b))
	}
	return &buf
}

// riskOf 给出一句风险提示。只作提醒，不做判定。
func riskOf(cmd string) string {
	low := strings.ToLower(cmd)
	switch {
	case strings.Contains(low, `\windows\`):
		return "系统组件，禁用前请确认"
	case strings.Contains(low, "onedrive"), strings.Contains(low, "nutstore"),
		strings.Contains(low, "baidu"), strings.Contains(low, "wps"):
		return "云同步，禁用后不会影响客户端本体"
	}
	return ""
}
