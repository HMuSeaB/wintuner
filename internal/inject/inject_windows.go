//go:build windows

// Package inject 管理 Winsock 命名空间提供程序（NSP）。
//
// NSP 是最容易被漏掉的一层注入：它在进程初始化 Winsock 时就被加载，
// 与那个软件是否在运行毫无关系。所以会出现"进程没跑、DLL 却在 Explorer 里"
// 这种看起来矛盾的现象——企业 VPN 客户端尤其爱这么干，而且常常重复注册。
package inject

import (
	"fmt"
	"os"
	"strings"

	"github.com/HMuSeaB/wintuner/internal/winreg"
)

// baseKey 是 Winsock2 的命名空间目录。
const baseKey = `SYSTEM\CurrentControlSet\Services\Winsock2\Parameters\Namespace_Catalog5`

// suffix 是禁用时给 DLL 加的后缀。
//
// 用改名而不是改注册表：Winsock 目录里的编号不能留空洞，手改容易把网络
// 搞坏；而改名是可逆的，且加载失败时 Winsock 会直接跳过该提供程序。
const suffix = ".wintuner-off"

// Provider 是一个命名空间提供程序条目。
type Provider struct {
	ID       string `json:"id"`
	Bits     string `json:"bits"` // "64" 或 "32"
	Slot     string `json:"slot"` // 目录编号
	DLL      string `json:"dll"`
	Vendor   string `json:"vendor"`
	Disabled bool   `json:"disabled"`
	Builtin  bool   `json:"builtin"`
}

// List 列出 32/64 两个目录里的全部条目。
func List() ([]Provider, error) {
	var out []Provider
	for _, bits := range []string{"64", "32"} {
		sub := baseKey + `\Catalog_Entries`
		if bits == "64" {
			sub += "64"
		}
		k, err := winreg.Open(winreg.LOCAL_MACHINE, sub)
		if err != nil {
			continue // 32 位目录在纯 64 位系统上可能不存在
		}
		slots, err := k.SubKeyNames()
		k.Close()
		if err != nil {
			continue
		}
		for _, slot := range slots {
			v, err := readDefault(winreg.LOCAL_MACHINE, sub+`\`+slot+`\LibraryPath`)
			if err != nil || v == "" {
				continue
			}
			dll := os.ExpandEnv(v)
			_, statErr := os.Stat(dll)
			out = append(out, Provider{
				ID:       bits + ":" + slot,
				Bits:     bits,
				Slot:     slot,
				DLL:      dll,
				Vendor:   vendorOf(dll),
				Disabled: statErr != nil, // 文件不在 = 已被本工具禁用
				Builtin:  isBuiltin(dll),
			})
		}
	}
	return out, nil
}

// Disable 把一个提供程序的 DLL 改名，让它加载不到。
func Disable(id string) error {
	p, err := find(id)
	if err != nil {
		return err
	}
	if p.Builtin {
		return fmt.Errorf("%q 是系统自带组件，不能禁用", p.Vendor)
	}
	if _, err := os.Stat(p.DLL); err != nil {
		return fmt.Errorf("DLL 已经不在原位置: %s", p.DLL)
	}
	if err := os.Rename(p.DLL, p.DLL+suffix); err != nil {
		return fmt.Errorf("改名失败（需要管理员权限）: %w", err)
	}
	return nil
}

// Enable 把 DLL 名字改回来。
func Enable(id string) error {
	p, err := find(id)
	if err != nil {
		return err
	}
	off := p.DLL + suffix
	if _, err := os.Stat(off); err != nil {
		return fmt.Errorf("找不到被禁用的文件: %s", off)
	}
	if _, err := os.Stat(p.DLL); err == nil {
		return fmt.Errorf("原文件已存在，无需还原: %s", p.DLL)
	}
	if err := os.Rename(off, p.DLL); err != nil {
		return fmt.Errorf("还原失败（需要管理员权限）: %w", err)
	}
	return nil
}

func find(id string) (Provider, error) {
	all, err := List()
	if err != nil {
		return Provider{}, err
	}
	for _, p := range all {
		if p.ID == id {
			return p, nil
		}
	}
	return Provider{}, fmt.Errorf("找不到提供程序 %q", id)
}

func readDefault(root winreg.Root, sub string) (string, error) {
	k, err := winreg.Open(root, sub)
	if err != nil {
		return "", err
	}
	defer k.Close()
	return k.GetString("LibraryPath")
}

func isBuiltin(dll string) bool {
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

func vendorOf(dll string) string {
	low := strings.ToLower(dll)
	switch {
	case strings.Contains(low, "sangfor"):
		return "深信服 SSL VPN"
	case isBuiltin(dll):
		return "Windows"
	}
	// 其余按所在目录名显示，通常是厂商名。
	dir := dll
	if i := strings.LastIndexAny(dll, `\/`); i >= 0 {
		dir = dll[:i]
	}
	if j := strings.LastIndexAny(dir, `\/`); j >= 0 {
		dir = dir[j+1:]
	}
	return dir
}
