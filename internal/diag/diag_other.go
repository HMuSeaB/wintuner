//go:build !windows

package diag

import (
	"errors"
	"time"
)

var ErrUnsupported = errors.New("diag: 诊断仅支持 Windows")

type ProcInfo struct {
	Name    string
	PID     uint32
	Threads uint32
}

type ExplorerStat struct {
	PID         uint32    `json:"pid"`
	CPUFraction float64   `json:"cpuFraction"`
	CPUPercent  float64   `json:"cpuPercent"`
	SampleMS    int       `json:"sampleMs"`
	Count       int       `json:"count"`
	At          time.Time `json:"at"`
}

type Group struct {
	Name     string  `json:"name"`
	Count    int     `json:"count"`
	CPUSecs  float64 `json:"cpuSeconds"`
	TotalMB  float64 `json:"totalMb"`
	MemoryMB float64 `json:"memoryMb"`
}

// ExplorerNow 是给界面用的即时读数。
type ExplorerNow struct {
	Running    bool    `json:"running"`
	PID        uint32  `json:"pid"`
	Threads    int     `json:"threads"`
	Handles    int     `json:"handles"`
	CPUPercent float64 `json:"cpuPercent"`
	CPUCores   float64 `json:"cpuCores"`
	SampleMS   int     `json:"sampleMs"`
	Verdict    string  `json:"verdict"`
}

// RestartResult 是一次资源管理器重启的结果。
type RestartResult struct {
	OK       bool     `json:"ok"`
	Method   string   `json:"method"`
	OldPID   uint32   `json:"oldPid"`
	NewPID   uint32   `json:"newPid"`
	Attempts []string `json:"attempts"`
}

func ProcessList() ([]ProcInfo, error)            { return nil, ErrUnsupported }
func SampleExplorer(ms int) (ExplorerStat, error) { return ExplorerStat{}, ErrUnsupported }
func SampleExplorerLight() (ExplorerNow, error)   { return ExplorerNow{}, ErrUnsupported }
func RestartExplorer() (RestartResult, error)     { return RestartResult{}, ErrUnsupported }
func RepeatedProcesses(min int) ([]Group, error)  { return nil, ErrUnsupported }
func Elevatable() bool                            { return false }
