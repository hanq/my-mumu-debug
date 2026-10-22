// Package mumu 实现 MuMu 模拟器安装检测与实例信息读取。
// 调研依据: docs/research/01-mumu-install-and-adb.md
package mumu

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EmuInstance 一个模拟器实例(MuMuManager info 输出的子集)
type EmuInstance struct {
	Index          int    `json:"index"`
	Name           string `json:"name"`
	Serial         string `json:"serial"` // host:port, 如 127.0.0.1:16384
	Running        bool   `json:"running"`
	AndroidStarted bool   `json:"android_started"`
	AndroidVersion string `json:"android_version"`
	PID            uint32 `json:"pid"`
}

// InstallInfo MuMu 安装信息
type InstallInfo struct {
	Installed   bool   `json:"installed"`
	Dir         string `json:"dir"`          // 安装根目录, 如 C:\Program Files\Netease\MuMu
	ManagerPath string `json:"manager_path"` // MuMuManager.exe
	AdbPath     string `json:"adb_path"`     // 模拟器自带 adb (可为空)
}

// mgrTimeout MuMuManager 单次调用超时
const mgrTimeout = 5 * time.Second

// Detect 探测 MuMu 安装位置(进程/默认路径/注册表三级探测由外层组合, 此处做路径校验)。
// 依次探测常见默认安装路径, 返回第一个命中项。
func Detect() InstallInfo {
	candidates := []string{
		`C:\Program Files\Netease\MuMu`,
		`C:\Program Files (x86)\Netease\MuMu`,
	}
	// 允许环境变量覆盖(便携安装)
	if custom := os.Getenv("MUMU_HOME"); custom != "" {
		candidates = append([]string{custom}, candidates...)
	}
	for _, dir := range candidates {
		info := verifyDir(dir)
		if info.Installed {
			return info
		}
	}
	return InstallInfo{Installed: false}
}

// verifyDir 校验一个候选目录是否为有效的 MuMu 安装目录
func verifyDir(dir string) InstallInfo {
	info := InstallInfo{Dir: dir}
	mgr := filepath.Join(dir, "nx_main", "MuMuManager.exe")
	if !fileExists(mgr) {
		return InstallInfo{}
	}
	info.ManagerPath = mgr
	info.AdbPath = findBundledAdb(dir)
	info.Installed = true
	return info
}

// findBundledAdb 在 nx_device/*/shell 下查找自带 adb.exe(取版本号最大的)
func findBundledAdb(dir string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, "nx_device", "*", "shell", "adb.exe"))
	if len(matches) == 0 {
		return ""
	}
	best := matches[0]
	for _, m := range matches[1:] {
		if m > best { // 版本目录字典序即版本序(12.0/15.0)
			best = m
		}
	}
	return best
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// muMuInfoRaw MuMuManager info -v all 的原始结构
type muMuInfoRaw struct {
	AdbHostIP      string `json:"adb_host_ip"`
	AdbPort        int    `json:"adb_port"`
	AndroidVersion string `json:"android_version"`
	IsAndroidStart bool   `json:"is_android_started"`
	IsProcessStart bool   `json:"is_process_started"`
	PID            uint32 `json:"pid"`
	Name           string `json:"name"`
	Index          string `json:"index"`
}

// ListInstances 调用 MuMuManager info -v all 解析实例列表。
func ListInstances(ctx context.Context, managerPath string) ([]EmuInstance, error) {
	c, cancel := context.WithTimeout(ctx, mgrTimeout)
	defer cancel()
	out, err := runCmd(c, managerPath, "info", "-v", "all")
	if err != nil {
		return nil, fmt.Errorf("MuMuManager info 执行失败: %w", err)
	}
	var raw map[string]muMuInfoRaw
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("MuMuManager 输出解析失败: %w (output: %.200s)", err, out)
	}
	list := make([]EmuInstance, 0, len(raw))
	for k, r := range raw {
		list = append(list, EmuInstance{
			Index:          atoiSafe(k),
			Name:           r.Name,
			Serial:         fmt.Sprintf("%s:%d", r.AdbHostIP, r.AdbPort),
			Running:        r.IsProcessStart,
			AndroidStarted: r.IsAndroidStart,
			AndroidVersion: r.AndroidVersion,
			PID:            r.PID,
		})
	}
	// 按 index 排序
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if list[j].Index < list[i].Index {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
	return list, nil
}

func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	out, err := execCmd(ctx, name, args...)
	return strings.TrimSpace(string(out)), err
}
