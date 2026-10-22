// Package capture 管理 mitmdump 副进程生命周期与 JSON 事件流。
// 调研依据: docs/research/03-http-capture.md, 04-pitfalls-and-decisions.md
package capture

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

//go:embed mitm_addon.py
var addonScript []byte

// Options 启动抓包的参数。
type Options struct {
	Serial   string // adb serial
	Port     int    // mitmdump 监听端口(默认 8888)
	FlowPath string // flows.mitm 落盘路径(空则不落)
	LogPath  string // mitmdump stderr 日志路径(空则丢弃)
}

// FlowEvent addon 输出的 JSON 事件(与 mitm_addon.py 保持一致)。
type FlowEvent struct {
	Type        string            `json:"type"` // flow|request|ws|tls_fail
	Ts          string            `json:"ts"`
	Method      string            `json:"method"`
	URL         string            `json:"url"`
	Status      int               `json:"status"`
	DurationMs  int64             `json:"duration_ms"`
	ReqHeaders  map[string]string `json:"req_headers"`
	RespHeaders map[string]string `json:"resp_headers"`
	ReqBody     string            `json:"req_body"`
	RespBody    string            `json:"resp_body"`
	Truncated   bool              `json:"truncated"`
	Direction   string            `json:"direction"`
	Content     string            `json:"content"`
	Side        string            `json:"side"`
	SNI         string            `json:"sni"`
	Error       string            `json:"error"`
}

// FindMitmdump 按优先级查找 mitmdump: PATH → pip 官方安装器默认目录。
func FindMitmdump() string {
	if p, err := exec.LookPath("mitmdump"); err == nil {
		return p
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(local, "Python", "pythoncore-*", "Scripts", "mitmdump.exe"))
	if len(matches) > 0 {
		return matches[len(matches)-1] // 取最新版本目录
	}
	return ""
}

// freePort 找一个空闲 TCP 端口(生成 CA 时避免 8080 冲突)。
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// GenerateCA 若 CA 不存在, 短暂启动 mitmdump 生成后按 PID 停止。
// mitmdump 启动即写证书文件; 端口用随机空闲端口避免占用冲突。
func GenerateCA(ctx context.Context, mitmdump string) error {
	if mitmdump == "" {
		return fmt.Errorf("mitmdump 未找到")
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, mitmdump, "--listen-host", "127.0.0.1", "--listen-port", fmt.Sprint(port))
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 mitmdump 生成 CA: %w", err)
	}
	ca := CAPathOf(mitmdump)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ca); err == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			killByPort(port) // pip 双进程: 按端口杀实际持有者(python 子进程)
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	killByPort(port)
	return fmt.Errorf("等待 CA 生成超时 (%s)", ca)
}

// CAPathOf mitmdump 对应的 CA 路径(默认 confdir=~/.mitmproxy)。
func CAPathOf(_ string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mitmproxy", "mitmproxy-ca-cert.pem")
}

// PortListening 检查本地端口是否有 LISTENING。
func PortListening(port int) (bool, int) {
	out, err := exec.Command("netstat", "-ano").Output()
	if err != nil {
		return false, 0
	}
	re := regexp.MustCompile(fmt.Sprintf(`(?m)^\s*TCP\s+\S+:%d\s+\S+\s+LISTENING\s+(\d+)`, port))
	m := re.FindSubmatch(out)
	if m == nil {
		return false, 0
	}
	var pid int
	fmt.Sscanf(string(m[1]), "%d", &pid)
	return true, pid
}

// killByPort 按监听端口反查 PID 并强杀(Windows 上 pip 版 mitmdump 的可靠停止方式)。
func killByPort(port int) error {
	ok, pid := PortListening(port)
	if !ok {
		return nil
	}
	return exec.Command("taskkill", "/F", "/PID", fmt.Sprint(pid)).Run()
}

// Session 一次抓包会话。
type Session struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	Opts   Options
}

// writeAddon 将内嵌 addon 写入临时文件(mitmdump 需要真实文件路径)。
func writeAddon() (string, error) {
	p := filepath.Join(os.TempDir(), "my-mumu-debug_mitm_addon.py")
	if err := os.WriteFile(p, addonScript, 0644); err != nil {
		return "", err
	}
	return p, nil
}

// Start 启动 mitmdump 副进程并开始解析事件。
// onEvent 在独立 goroutine 回调; onExit 在进程退出时回调(含异常退出)。
func Start(ctx context.Context, opts Options,
	onEvent func(FlowEvent), onExit func(err error)) (*Session, error) {

	if opts.Port == 0 {
		opts.Port = 8888
	}
	if ok, pid := PortListening(opts.Port); ok {
		return nil, fmt.Errorf("端口 %d 已被占用 (PID %d), 请换端口或先停止", opts.Port, pid)
	}
	addonPath, err := writeAddon()
	if err != nil {
		return nil, err
	}
	args := []string{
		"-s", addonPath,
		"--listen-host", "0.0.0.0",
		"--listen-port", fmt.Sprint(opts.Port),
		"--set", "flow_detail=1",
	}
	if opts.FlowPath != "" {
		args = append(args, "-w", opts.FlowPath)
	}
	c, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(c, FindMitmdump(), args...)
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if opts.LogPath != "" {
		f, err := os.Create(opts.LogPath)
		if err == nil {
			cmd.Stderr = f
		}
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("启动 mitmdump: %w", err)
	}
	s := &Session{cmd: cmd, cancel: cancel, Opts: opts}

	go func() { // 事件解析循环
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 单行上限 1MB(addon 已截断 body)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if !strings.HasPrefix(line, "{") {
				continue
			}
			var ev FlowEvent
			if err := json.Unmarshal([]byte(line), &ev); err == nil && onEvent != nil {
				onEvent(ev)
			}
		}
		if onExit != nil {
			onExit(c.Err())
		}
	}()
	return s, nil
}

// Stop 停止会话: 先杀子进程树, 再按端口兜底清理(pip 双进程)。
func (s *Session) Stop() error {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	_ = s.cmd.Process.Kill()
	s.cancel()
	_ = s.cmd.Wait()
	// pip wrapper 会在 python 子进程持有端口, 必须按端口兜底
	return killByPort(s.Opts.Port)
}
