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

func ProcessList() ([]ProcInfo, error)            { return nil, ErrUnsupported }
func SampleExplorer(ms int) (ExplorerStat, error) { return ExplorerStat{}, ErrUnsupported }
func RepeatedProcesses(min int) ([]Group, error)  { return nil, ErrUnsupported }
func Elevatable() bool                            { return false }
