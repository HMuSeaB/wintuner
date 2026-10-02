//go:build windows

package diag

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	psapi                   = syscall.NewLazyDLL("psapi.dll")
	procEnumProcessModules  = psapi.NewProc("EnumProcessModulesEx")
	procGetModuleFileNameEx = psapi.NewProc("GetModuleFileNameExW")
)

const (
	listModulesAll = 0x03
	maxModules     = 1024
)

// Module 是加载进某个进程的一个 DLL。
type Module struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Vendor string `json:"vendor"`
	// Orphan 为 true 表示这个 DLL 的"主人"没在运行——
	// 也就是它注册在外壳里、但对应的程序根本没开。
	//
	// 这正是拖慢 Explorer 的典型形态：扩展被加载进来了，
	// 它却要去联系一个不存在的后台进程，于是卡在等待上。
	Orphan bool `json:"orphan"`
	// Builtin 表示是 Windows 自带的，界面上要排除。
	Builtin bool `json:"builtin"`
}

// ExplorerModules 列出加载进 explorer.exe 的第三方 DLL。
//
// 这是定位"谁把资源管理器拖住了"最直接的证据：对比卡与不卡两个时刻
// 的模块清单，多出来的就是嫌疑对象。实测中卡顿时会多出云盘、办公软件的
// 外壳扩展——它们平时不加载，一旦用户右键点了文件就被拉进来，然后再不卸载。
func ExplorerModules() ([]Module, error) {
	procs, err := ProcessList()
	if err != nil {
		return nil, err
	}
	var pid uint32
	for _, p := range procs {
		if equalFold(p.Name, "explorer.exe") {
			pid = p.PID
			break
		}
	}
	if pid == 0 {
		return nil, fmt.Errorf("找不到 explorer.exe")
	}

	// 枚举模块需要比读进程时间更高的权限：
	//   PROCESS_QUERY_INFORMATION(0x0400) | PROCESS_VM_READ(0x0010)
	// 只用 QUERY_LIMITED_INFORMATION 时 EnumProcessModulesEx 会直接失败。
	const moduleAccess = 0x0400 | 0x0010

	h, _, _ := procOpenProcess.Call(uintptr(moduleAccess), 0, uintptr(pid))
	if h == 0 {
		// 退一步试受限权限：虽然枚举会失败，但错误信息更准确。
		h, _, _ = procOpenProcess.Call(uintptr(queryLimitedInfo), 0, uintptr(pid))
		if h == 0 {
			return nil, fmt.Errorf("打不开 explorer 进程 %d（枚举模块需要管理员权限）", pid)
		}
	}
	defer procCloseHandle.Call(h)

	// 先问需要多大缓冲。
	//
	// EnumProcessModulesEx 不接受 lpbNeeded 为 NULL —— 传空指针时它会直接
	// 失败并回写 0，看起来像"不需要空间"。必须给一个真变量。
	var need uint32
	ret0, _, _ := procEnumProcessModules.Call(
		h, 0, 0, uintptr(unsafe.Pointer(&need)), listModulesAll,
	)
	if ret0 == 0 || need == 0 {
		// 有些系统上 cb 传 0 也不回写大小，退回一个保守的固定缓冲重试。
		need = maxModules * 8
	}
	if need > maxModules*8 {
		return nil, fmt.Errorf("模块数量异常（需要 %d 字节）", need)
	}

	buf := make([]byte, need)
	var got uint32
	ret, _, _ := procEnumProcessModules.Call(
		h, uintptr(unsafe.Pointer(&buf[0])),
		uintptr(need), uintptr(unsafe.Pointer(&got)), listModulesAll,
	)
	if ret == 0 {
		return nil, fmt.Errorf("枚举模块失败")
	}
	if got > 0 {
		need = got
	}

	// 缓冲里是一串 HMODULE 句柄
	count := int(need) / 8
	seen := map[string]bool{}
	var out []Module

	pathBuf := make([]uint16, 1024)
	for i := 0; i < count; i++ {
		hm := *(*uintptr)(unsafe.Pointer(&buf[i*8]))
		if hm == 0 {
			continue
		}
		n, _, _ := procGetModuleFileNameEx.Call(
			h, hm, uintptr(unsafe.Pointer(&pathBuf[0])), uintptr(len(pathBuf)),
		)
		if n == 0 {
			continue
		}
		path := utf16AtBytes(pathBuf, int(n))

		name := strings.ToLower(filepath.Base(path))
		if seen[name] {
			continue
		}
		seen[name] = true

		m := Module{
			Name:    filepath.Base(path),
			Path:    path,
			Vendor:  vendorOfPath(path),
			Builtin: isSystemPath(path),
		}
		if !m.Builtin {
			m.Orphan = ownerNotRunning(name)
		}
		out = append(out, m)
	}
	return out, nil
}

// ownerNotRunning 判断这个 DLL 对应的程序有没有在跑。
//
// 按 DLL 名里的关键词去猜程序名，然后查进程。猜不中就返回 false——
// 宁可漏报，也不要把正在用的标成"没在跑"，那会诱导用户关掉有用的东西。
func ownerNotRunning(dllName string) bool {
	type probe struct {
		keyword string
		procs   []string
	}
	probes := []probe{
		{"yunshel", []string{"baidunetdisk", "yunguanjia"}},
		{"baidu", []string{"baidunetdisk", "yunguanjia"}},
		{"nutstore", []string{"nutstore", "nutstoreclient"}},
		{"onedrive", []string{"onedrive"}},
		{"coresync", []string{"coresync", "creative cloud"}},
		{"adobe", []string{"coresync", "creative cloud"}},
		{"dropbox", []string{"dropbox"}},
		{"googledrive", []string{"googledrivefs"}},
		{"wps", []string{"wps", "wpscloudsvr", "wpscenter"}},
		{"qing", []string{"wps", "wpscloudsvr"}},
		{"weiyun", []string{"weiyun"}},
		{"aliyundrive", []string{"aliyundrive", "aDrive"}},
		{"quark", []string{"quark"}},
		{"cnki", []string{"cnki"}},
	}

	low := strings.ToLower(dllName)
	for _, p := range probes {
		if !strings.Contains(low, p.keyword) {
			continue
		}
		if anyProcessRunning(p.procs) {
			return false
		}
		return true
	}
	return false // 认不出来就不下结论
}

func anyProcessRunning(names []string) bool {
	procs, err := ProcessList()
	if err != nil {
		return true // 查不到就当它在跑，避免误报
	}
	for _, p := range procs {
		pl := strings.ToLower(p.Name)
		for _, n := range names {
			if strings.Contains(pl, strings.ToLower(n)) {
				return true
			}
		}
	}
	return false
}

// vendorOfPath 从路径猜厂商名。
func vendorOfPath(path string) string {
	low := strings.ToLower(path)
	switch {
	case strings.Contains(low, `\baidunetdisk\`):
		return "百度网盘"
	case strings.Contains(low, `\nutstore\`):
		return "坚果云"
	case strings.Contains(low, "coresync"), strings.Contains(low, `\adobe\`):
		return "Adobe"
	case strings.Contains(low, "onedrive"):
		return "OneDrive"
	case strings.Contains(low, `\wps`), strings.Contains(low, "kingsoft"):
		return "WPS"
	case strings.Contains(low, "nvidia"), strings.Contains(low, "nvui"):
		return "NVIDIA"
	case strings.Contains(low, "7-zip"), strings.Contains(low, "7z"):
		return "7-Zip"
	case strings.Contains(low, "amd"), strings.Contains(low, "ati"):
		return "AMD"
	case isSystemPath(path):
		return "Windows"
	}
	// 退路：取上一级目录名，通常是厂商目录
	dir := filepath.Dir(path)
	if base := filepath.Base(dir); base != "" && base != "." {
		return base
	}
	return "未知"
}

func isSystemPath(path string) bool {
	low := strings.ToLower(path)
	sys := strings.ToLower(os.Getenv("SystemRoot"))
	if sys != "" && strings.HasPrefix(low, sys) {
		return true
	}
	return strings.HasPrefix(low, `c:\windows\`)
}

// utf16AtBytes 从 uint16 缓冲里取前 n 个字符。
// n 是 GetModuleFileNameEx 返回的字符数（不含结尾）。
func utf16AtBytes(buf []uint16, n int) string {
	if n > len(buf) {
		n = len(buf)
	}
	return syscall.UTF16ToString(buf[:n])
}
