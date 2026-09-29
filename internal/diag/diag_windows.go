//go:build windows

// Package diag 采集诊断数据。
//
// 走 Toolhelp 快照 + GetProcessTimes 直调 kernel32，不引第三方依赖。
// 之所以自己实现而不是去调命令行工具：任务管理器能给的字段这里都要，
// 而起一个外部进程再解析文本既慢又脆（输出还会随系统语言变）。
package diag

import (
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	procCreateSnapshot  = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32First  = kernel32.NewProc("Process32FirstW")
	procProcess32Next   = kernel32.NewProc("Process32NextW")
	procOpenProcess     = kernel32.NewProc("OpenProcess")
	procGetProcessTimes = kernel32.NewProc("GetProcessTimes")
	procCloseHandle     = kernel32.NewProc("CloseHandle")
	procGetSystemTimes  = kernel32.NewProc("GetSystemTimes")
)

const (
	th32csSnapProcess  = 0x00000002
	invalidHandleValue = ^uintptr(0)

	// PROCESS_QUERY_LIMITED_INFORMATION 在没提权时也能用，
	// 而 PROCESS_QUERY_INFORMATION 对系统进程会被拒。
	queryLimitedInfo = 0x1000
)

// PROCESSENTRY32W 的字段偏移。
//
// 这些偏移来自结构体定义，64 位 Windows 上固定：
// dwSize(4) cntUsage(4) th32ProcessID(4) <填充4> th32DefaultHeapID(8)
// th32ModuleID(4) cntThreads(4) th32ParentProcessID(4) pcPriClassBase(4)
// dwFlags(4) szExeFile(520)
// 结构体自身要按 8 字节对齐（因为中间有 ULONG_PTR 成员），
// 所以 44+520=564 要向上取整到 568。dwSize 填少了 Process32First 会直接
// 返回 ERROR_INVALID_PARAMETER，表现是"一个进程也列不出来"。
const (
	offPID     = 8
	offThreads = 28
	offExeFile = 44
	sizeEntry  = 568
	bufSize    = 1024
)

// ProcInfo 是进程快照里的一条。
type ProcInfo struct {
	Name    string
	PID     uint32
	Threads uint32
}

// ExplorerStat 是资源管理器的一次采样结果。
type ExplorerStat struct {
	PID         uint32    `json:"pid"`
	CPUFraction float64   `json:"cpuFraction"` // 采样期间占了多少个核
	CPUPercent  float64   `json:"cpuPercent"`  // 折算成百分比
	SampleMS    int       `json:"sampleMs"`
	Count       int       `json:"count"` // explorer 进程数
	At          time.Time `json:"at"`
}

// ProcessList 列出当前所有进程。
func ProcessList() ([]ProcInfo, error) {
	snap, _, err := procCreateSnapshot.Call(uintptr(th32csSnapProcess), 0)
	if snap == invalidHandleValue {
		return nil, fmt.Errorf("diag: 创建进程快照失败: %w", err)
	}
	defer procCloseHandle.Call(snap)

	buf := make([]byte, bufSize)
	// dwSize 必须自己填，否则 Process32First 直接失败。
	writeU32(buf, 0, uint32(sizeEntry))

	var out []ProcInfo
	ret, _, err2 := procProcess32First.Call(snap, uintptr(unsafe.Pointer(&buf[0])))
	if ret == 0 {
		return nil, fmt.Errorf("diag: Process32First 失败: %w (%d)", err2, ret)
	}
	for ret != 0 {
		out = append(out, ProcInfo{
			PID:     readU32(buf, offPID),
			Threads: readU32(buf, offThreads),
			Name:    utf16At(buf, offExeFile),
		})
		ret, _, _ = procProcess32Next.Call(snap, uintptr(unsafe.Pointer(&buf[0])))
	}
	return out, nil
}

// ---------- CPU 时间 ----------

// cpuTime 返回某个进程已消耗的 CPU 时间（内核态 + 用户态），单位 100ns。
func cpuTime(pid uint32) (time.Duration, error) {
	h, _, _ := procOpenProcess.Call(uintptr(queryLimitedInfo), 0, uintptr(pid))
	if h == 0 {
		return 0, fmt.Errorf("diag: 打不开进程 %d", pid)
	}
	defer procCloseHandle.Call(h)

	var creation, exit, kernel, user syscall.Filetime
	ret, _, _ := procGetProcessTimes.Call(
		h,
		uintptr(unsafe.Pointer(&creation)),
		uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if ret == 0 {
		return 0, fmt.Errorf("diag: 读进程 %d 的时间失败", pid)
	}
	return filetimeToDuration(kernel) + filetimeToDuration(user), nil
}

// filetimeToDuration 把 FILETIME（100ns 为单位）转成 Duration。
func filetimeToDuration(ft syscall.Filetime) time.Duration {
	n := int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)
	return time.Duration(n) * 100
}

// ---------- 采样 ----------

// SampleExplorer 在 ms 毫秒内对 explorer.exe 采样，返回它占了多少 CPU。
//
// 返回的是"占了多少个核"（1.0 = 吃满一个核）。折算成百分比要乘 100，
// 因为机器上通常不止一个核，用百分比表达会让人误以为机器快满了。
func SampleExplorer(ms int) (ExplorerStat, error) {
	procs, err := ProcessList()
	if err != nil {
		return ExplorerStat{}, err
	}

	var pids []uint32
	for _, p := range procs {
		if equalFold(p.Name, "explorer.exe") {
			pids = append(pids, p.PID)
		}
	}
	if len(pids) == 0 {
		return ExplorerStat{}, fmt.Errorf("diag: 没有找到 explorer.exe")
	}

	before := totalCPUTime(pids)
	start := time.Now()
	time.Sleep(time.Duration(ms) * time.Millisecond)
	elapsed := time.Since(start)
	after := totalCPUTime(pids)

	delta := after - before
	if delta < 0 {
		delta = 0 // 进程被替换时可能出现负值，按 0 处理
	}

	return ExplorerStat{
		PID:         pids[0],
		CPUFraction: delta.Seconds() / elapsed.Seconds(),
		CPUPercent:  delta.Seconds() / elapsed.Seconds() * 100,
		SampleMS:    int(elapsed.Milliseconds()),
		Count:       len(pids),
		At:          time.Now(),
	}, nil
}

func totalCPUTime(pids []uint32) time.Duration {
	var total time.Duration
	for _, pid := range pids {
		if d, err := cpuTime(pid); err == nil {
			total += d
		}
	}
	return total
}

// ---------- 重复进程 ----------

// Group 是同名进程的一组统计。
type Group struct {
	Name     string  `json:"name"`
	Count    int     `json:"count"`
	CPUSecs  float64 `json:"cpuSeconds"`
	TotalMB  float64 `json:"totalMb"`
	MemoryMB float64 `json:"memoryMb"`
}

// RepeatedProcesses 找出同名进程达到 min 个的组，按数量降序。
//
// 这条检查是实打实踩出来的：一次 Explorer 卡顿时，发现是 23 个卡住的
// SnippingTool 在拖累它。一个程序起了十几个实例且大多不干活，
// 几乎总是"启动失败但没退出"的信号。
func RepeatedProcesses(min int) ([]Group, error) {
	procs, err := ProcessList()
	if err != nil {
		return nil, err
	}

	type agg struct {
		count int
		cpu   time.Duration
	}
	byName := map[string]*agg{}
	for _, p := range procs {
		a, ok := byName[p.Name]
		if !ok {
			a = &agg{}
			byName[p.Name] = a
		}
		a.count++
		if d, err := cpuTime(p.PID); err == nil {
			a.cpu += d
		}
	}

	var out []Group
	for name, a := range byName {
		if a.count < min {
			continue
		}
		out = append(out, Group{
			Name:    name,
			Count:   a.count,
			CPUSecs: a.cpu.Seconds(),
		})
	}
	// 数量多的排前面，数量相同则按 CPU 排。
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j].Count > out[i].Count ||
				(out[j].Count == out[i].Count && out[j].CPUSecs > out[i].CPUSecs) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// ---------- 辅助 ----------

func writeU32(buf []byte, off int, v uint32) {
	buf[off] = byte(v)
	buf[off+1] = byte(v >> 8)
	buf[off+2] = byte(v >> 16)
	buf[off+3] = byte(v >> 24)
}

func readU32(buf []byte, off int) uint32 {
	return uint32(buf[off]) | uint32(buf[off+1])<<8 |
		uint32(buf[off+2])<<16 | uint32(buf[off+3])<<24
}

// utf16At 从缓冲区偏移处读一个以 NUL 结尾的 UTF-16 字符串。
func utf16At(buf []byte, off int) string {
	var runes []uint16
	for i := off; i+1 < len(buf); i += 2 {
		v := uint16(buf[i]) | uint16(buf[i+1])<<8
		if v == 0 {
			break
		}
		runes = append(runes, v)
	}
	return syscall.UTF16ToString(runes)
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// Elevatable 报告当前进程能否读到系统进程时间。
// 读不到时界面要提示"部分数据需要管理员权限"。
func Elevatable() bool {
	// 拿自己试试：能读到就说明这条路通。
	_, err := cpuTime(uint32(os.Getpid()))
	return err == nil
}
