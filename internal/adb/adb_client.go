// Package adb 封装 MuMu 场景下的 adb 命令(连接/root/shell/包查询)。
// 调研依据: docs/research/01-mumu-install-and-adb.md, 02-root-and-ca-cert-install.md
package adb

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Client 一个绑定 adb 可执行文件路径的客户端(线程安全, 无状态)。
type Client struct {
	AdbPath string // adb.exe 完整路径; 为空则依赖 PATH
}

const (
	tConnect  = 8 * time.Second
	tRoot     = 15 * time.Second
	tShort    = 6 * time.Second
	rootSleep = 1500 * time.Millisecond // adbd 重启等待
)

func (c *Client) bin() string {
	if c.AdbPath != "" {
		return c.AdbPath
	}
	return "adb"
}

// run 执行 adb 全局命令(不带 -s)。
func (c *Client) run(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.bin(), args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// runWithSerial 执行针对指定设备的命令(adb -s serial ...)。
func (c *Client) runWithSerial(ctx context.Context, serial string, timeout time.Duration, args ...string) (string, error) {
	full := append([]string{"-s", serial}, args...)
	return c.run(ctx, timeout, full...)
}

// Connect 连接模拟器。已连接时返回 "already connected" 也视为成功。
func (c *Client) Connect(ctx context.Context, serial string) error {
	out, err := c.run(ctx, tConnect, "connect", serial)
	if err != nil {
		return fmt.Errorf("adb connect %s: %w (%s)", serial, err, out)
	}
	l := strings.ToLower(out)
	if strings.Contains(l, "connected") && !strings.Contains(l, "cannot") && !strings.Contains(l, "failed") {
		return nil
	}
	return fmt.Errorf("adb connect %s: %s", serial, out)
}

// Disconnect 断开指定设备的 adb 连接。
func (c *Client) Disconnect(ctx context.Context, serial string) error {
	out, err := c.run(ctx, tConnect, "disconnect", serial)
	if err != nil {
		return fmt.Errorf("adb disconnect %s: %w (%s)", serial, err, out)
	}
	return nil
}

// State 设备状态(device/offline/...)
func (c *Client) State(ctx context.Context, serial string) (string, error) {
	out, err := c.runWithSerial(ctx, serial, tShort, "get-state")
	if err != nil {
		return "", fmt.Errorf("adb get-state: %w (%s)", err, out)
	}
	return strings.TrimSpace(out), nil
}

// EnsureRoot 执行 adb root 并校验 uid=0。
// 注意: adb root 会重启 adbd, 需等待后重新 connect。
func (c *Client) EnsureRoot(ctx context.Context, serial string) error {
	_, _ = c.runWithSerial(ctx, serial, tRoot, "root") // 已 root 时返回提示, 忽略错误
	time.Sleep(rootSleep)
	_ = c.Connect(ctx, serial)
	out, err := c.runWithSerial(ctx, serial, tShort, "shell", "id", "-u")
	if err != nil {
		return fmt.Errorf("校验 root: %w (%s)", err, out)
	}
	if strings.TrimSpace(out) != "0" {
		return fmt.Errorf("root 未生效 (id -u = %s), 请确认 MuMu 设置中 root 权限已开启", out)
	}
	return nil
}

// IsRoot 当前 adbd 是否为 root。
func (c *Client) IsRoot(ctx context.Context, serial string) bool {
	out, err := c.runWithSerial(ctx, serial, tShort, "shell", "id", "-u")
	return err == nil && strings.TrimSpace(out) == "0"
}

// Shell 执行 shell 命令并返回去 \r 后的输出。
func (c *Client) Shell(ctx context.Context, serial string, timeout time.Duration, cmd string) (string, error) {
	out, err := c.runWithSerial(ctx, serial, timeout, "shell", cmd)
	return strings.ReplaceAll(out, "\r", ""), err
}

var (
	reTopResumed = regexp.MustCompile(`topResumedActivity=ActivityRecord\{[^}]*\s([\w.]+)/([^\s}]+)`)
	reFocus      = regexp.MustCompile(`mCurrentFocus=Window\{[^}]*\s([\w.]+)/([^\s}]+)`)
	rePidof      = regexp.MustCompile(`\d+`)
	reNetstat    = regexp.MustCompile(`\s(\S+):(\d+)\s+(\S+)\s+(\S+)\s+(\d+)/(\S+)`)
)

// ForegroundApp 解析当前前台应用(topResumedActivity 优先, mCurrentFocus 兜底)。
func (c *Client) ForegroundApp(ctx context.Context, serial string) (pkg, activity string, err error) {
	out, err := c.Shell(ctx, serial, tShort, "dumpsys activity activities")
	if err == nil {
		if m := reTopResumed.FindStringSubmatch(out); m != nil {
			return m[1], m[2], nil
		}
	}
	out2, err2 := c.Shell(ctx, serial, tShort, "dumpsys window")
	if err2 != nil {
		return "", "", fmt.Errorf("获取前台应用: %w", err2)
	}
	if m := reFocus.FindStringSubmatch(out2); m != nil {
		return m[1], m[2], nil
	}
	return "", "", fmt.Errorf("未解析到前台应用")
}

// ThirdPartyPackages 三方包名列表(pm list packages -3)。
func (c *Client) ThirdPartyPackages(ctx context.Context, serial string) ([]string, error) {
	out, err := c.Shell(ctx, serial, tShort, "pm list packages -3")
	if err != nil {
		return nil, fmt.Errorf("pm list packages: %w", err)
	}
	var list []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package:") {
			list = append(list, strings.TrimPrefix(line, "package:"))
		}
	}
	return list, nil
}

// PIDOf 包名对应主进程 PID(-1 表示未运行)。
func (c *Client) PIDOf(ctx context.Context, serial, pkg string) int {
	out, err := c.Shell(ctx, serial, tShort, "pidof "+pkg)
	if err != nil {
		return -1
	}
	m := rePidof.FindString(strings.TrimSpace(out))
	n, _ := strconv.Atoi(m)
	return n
}

// RestartApp 强制停止并重新启动应用(证书安装后必须重启以继承新命名空间)。
func (c *Client) RestartApp(ctx context.Context, serial, pkg, activity string) error {
	_, _ = c.Shell(ctx, serial, tShort, "am force-stop "+pkg)
	time.Sleep(time.Second)
	startActivity := activity
	if startActivity == "" {
		startActivity = "io.dcloud.PandoraEntryActivity" // uni-app 默认入口兜底
	}
	if !strings.HasPrefix(startActivity, ".") && !strings.Contains(startActivity, ".") {
		startActivity = "." + startActivity
	}
	_, err := c.Shell(ctx, serial, tShort, "am start -n "+pkg+"/"+startActivity)
	return err
}

// ProcConnection 进程的一条 TCP 连接(netstat 解析)。
type ProcConnection struct {
	Local  string `json:"local"`
	Remote string `json:"remote"`
	State  string `json:"state"`
	PID    int    `json:"pid"`
	Proc   string `json:"proc"`
}

// ProcessConnections 指定进程的 TCP 连接(用于判断流量是否走代理/直连)。
func (c *Client) ProcessConnections(ctx context.Context, serial, pkg string) ([]ProcConnection, error) {
	pid := c.PIDOf(ctx, serial, pkg)
	if pid <= 0 {
		return nil, fmt.Errorf("进程 %s 未运行", pkg)
	}
	out, err := c.Shell(ctx, serial, tShort, "netstat -tunp")
	if err != nil {
		return nil, fmt.Errorf("netstat: %w", err)
	}
	var list []ProcConnection
	for _, line := range strings.Split(out, "\n") {
		m := reNetstat.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		p, _ := strconv.Atoi(m[4])
		if p != pid {
			continue
		}
		list = append(list, ProcConnection{
			Local:  m[1] + ":" + m[2],
			Remote: m[3],
			State:  m[4],
			PID:    p,
			Proc:   m[6],
		})
	}
	return list, nil
}

// Push 推送本地文件到设备。
func (c *Client) Push(ctx context.Context, serial, local, remote string) error {
	out, err := c.runWithSerial(ctx, serial, tConnect, "push", local, remote)
	if err != nil {
		return fmt.Errorf("adb push %s: %w (%s)", local, err, out)
	}
	return nil
}

// SetGlobalProxy 设置全局 HTTP 代理。hostPort 为空或 ":0" 表示清除。
func (c *Client) SetGlobalProxy(ctx context.Context, serial, hostPort string) error {
	v := hostPort
	if v == "" {
		v = ":0"
	}
	_, err := c.Shell(ctx, serial, tShort, "settings put global http_proxy "+v)
	return err
}

// GetGlobalProxy 读取当前全局代理。
func (c *Client) GetGlobalProxy(ctx context.Context, serial string) (string, error) {
	return c.Shell(ctx, serial, tShort, "settings get global http_proxy")
}
