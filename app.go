package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"my-mumu-debug/internal/adb"
	"my-mumu-debug/internal/capture"
	"my-mumu-debug/internal/certstore"
	"my-mumu-debug/internal/execx"
	"my-mumu-debug/internal/mumu"
)

// App Wails 绑定层: 仅做编排与事件推送, 业务在各 internal 包。
type App struct {
	ctx       context.Context
	mumuInfo  mumu.InstallInfo
	client    *adb.Client
	serial    string
	session   *capture.Session
	capturing bool
	paused    bool
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// shutdown 应用退出时清理: 停止抓包(杀 mitmdump/清代理), 避免残留进程占用端口。
func (a *App) shutdown(ctx context.Context) {
	_ = a.StopCapture()
}

// log 推送一条步骤日志到前端(步骤面板)。
func (a *App) log(format string, args ...any) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "env:log", fmt.Sprintf("[%s] %s",
			time.Now().Format("15:04:05"), fmt.Sprintf(format, args...)))
	}
}

// DetectEnvironment 环境检测: MuMu 安装/实例/mitmdump/CA。
func (a *App) DetectEnvironment() mumu.InstallInfo {
	a.log("开始环境检测...")
	a.mumuInfo = mumu.Detect()
	if !a.mumuInfo.Installed {
		a.log("未检测到 MuMu 模拟器安装")
		return a.mumuInfo
	}
	a.log("MuMu 已安装: %s", a.mumuInfo.Dir)
	if a.mumuInfo.AdbPath != "" {
		a.log("使用模拟器自带 adb: %s", a.mumuInfo.AdbPath)
	}
	a.client = &adb.Client{AdbPath: a.mumuInfo.AdbPath}
	return a.mumuInfo
}

// ListInstances 列出模拟器实例。
func (a *App) ListInstances() ([]mumu.EmuInstance, error) {
	if !a.mumuInfo.Installed {
		return nil, fmt.Errorf("请先执行环境检测")
	}
	return mumu.ListInstances(a.ctx, a.mumuInfo.ManagerPath)
}

// ConnectDevice 连接指定实例并记住 serial。
func (a *App) ConnectDevice(serial string) error {
	if a.client == nil {
		return fmt.Errorf("请先执行环境检测")
	}
	if err := a.client.Connect(a.ctx, serial); err != nil {
		a.log("连接失败: %v", err)
		return err
	}
	a.serial = serial
	st, _ := a.client.State(a.ctx, serial)
	a.log("adb 已连接 %s (state=%s)", serial, st)
	return nil
}

// EnsureRoot 提权并校验。
func (a *App) EnsureRoot(serial string) error {
	if a.client == nil {
		return fmt.Errorf("请先连接设备")
	}
	if err := a.client.EnsureRoot(a.ctx, serial); err != nil {
		a.log("root 失败: %v", err)
		return err
	}
	a.serial = serial
	a.log("root 权限 OK (uid=0)")
	return nil
}

// DisconnectDevice 断开 adb 连接。
func (a *App) DisconnectDevice(serial string) error {
	if a.client == nil {
		return fmt.Errorf("请先执行环境检测")
	}
	if err := a.client.Disconnect(a.ctx, serial); err != nil {
		return err
	}
	a.log("adb 已断开 %s", serial)
	return nil
}

// OpenAdbShell 打开新的命令行窗口并直接进入指定实例的 adb shell。
// 使用检测到的 adb(优先 MuMu 自带), 需已通过 ConnectDevice 建立连接。
//
// 启动链: 隐藏的 `cmd /c start "" cmd /k adb -s <serial> shell`:
//   - Go 的 exec 给子进程的 stdin 是 NUL 设备, 直接 CREATE_NEW_CONSOLE 启动
//     adb shell 会立即读到 EOF 而秒退(窗口一闪而过);
//   - 由 start 在新可见控制台里拉起 `cmd /k adb ...`, 子进程拿到真实控制台 stdio,
//     交互可输入, 且 shell 退出后窗口保留(能看到报错, 回到 cmd 提示符)。
func (a *App) OpenAdbShell(serial string) error {
	if a.client == nil {
		return fmt.Errorf("请先执行环境检测")
	}
	if serial == "" {
		return fmt.Errorf("未选择实例")
	}
	adbPath := a.client.AdbPath
	if adbPath == "" {
		adbPath = "adb"
	}
	a.log("打开 ADB Shell 窗口: %s (%s)", serial, adbPath)
	// 外层 cmd 隐藏执行(不闪黑框), 可见窗口由 start 创建
	return execx.Command(a.ctx, "cmd", "/c", "start", "", "cmd", "/k", adbPath, "-s", serial, "shell").Start()
}

// GetForegroundApp 前台应用。
func (a *App) GetForegroundApp(serial string) (map[string]string, error) {
	if a.client == nil {
		return nil, fmt.Errorf("请先连接设备")
	}
	pkg, act, err := a.client.ForegroundApp(a.ctx, serial)
	if err != nil {
		return nil, err
	}
	a.log("前台应用: %s / %s", pkg, act)
	return map[string]string{"package": pkg, "activity": act}, nil
}

// ListThirdPartyPackages 三方应用列表。
func (a *App) ListThirdPartyPackages(serial string) ([]string, error) {
	if a.client == nil {
		return nil, fmt.Errorf("请先连接设备")
	}
	return a.client.ThirdPartyPackages(a.ctx, serial)
}

// GetProcessConnections 目标进程 TCP 连接(诊断是否走代理)。
func (a *App) GetProcessConnections(serial, pkg string) ([]adb.ProcConnection, error) {
	if a.client == nil {
		return nil, fmt.Errorf("请先连接设备")
	}
	return a.client.ProcessConnections(a.ctx, serial, pkg)
}

// InstallCertificate 安装抓包 CA(幂等), 完成后重启目标 APP 以继承证书视图。
func (a *App) InstallCertificate(serial, targetPkg string) error {
	if a.client == nil {
		return fmt.Errorf("请先连接设备")
	}
	a.log("安装 CA 证书到 zygote 命名空间...")
	mp := capture.FindMitmdump()
	if !certstore.CAExists() {
		if mp == "" {
			err := fmt.Errorf("未找到 mitmdump, 无法生成 CA; 请将官方独立版 mitmdump.exe 置于程序目录 mitmdump\\ 下, 或安装 Python + mitmproxy")
			a.log("%v", err)
			return err
		}
		a.log("CA 不存在, 调用 mitmdump 生成: %s", mp)
	}
	if err := certstore.Install(a.ctx, a.client, serial, mp); err != nil {
		a.log("证书安装失败: %v", err)
		return err
	}
	a.log("证书安装校验通过 (两 zygote 命名空间均含 mitm CA)")
	if targetPkg != "" {
		act := ""
		if p, ac, err := a.client.ForegroundApp(a.ctx, serial); err == nil && p == targetPkg {
			act = ac
		}
		if err := a.client.RestartApp(a.ctx, serial, targetPkg, act); err != nil {
			a.log("目标 APP 重启失败(可手动重启): %v", err)
		} else {
			a.log("目标 APP %s 已重启 (继承证书命名空间)", targetPkg)
		}
	}
	return nil
}

// StartCapture 一键开启抓包: 装证书 → 设代理 → 启动 mitmdump → 重启 APP。
func (a *App) StartCapture(serial, targetPkg string, port int, skipCert bool) error {
	if a.client == nil {
		return fmt.Errorf("请先连接设备")
	}
	if a.capturing {
		return fmt.Errorf("抓包已在进行中")
	}
	if port == 0 {
		port = 8888
	}
	a.serial = serial

	if !skipCert {
		if err := a.InstallCertificate(serial, targetPkg); err != nil {
			return err
		}
	}
	// 默认不落盘: 流量报文含 token/凭证等敏感信息, 仅内存展示, 避免泄露
	opts := capture.Options{
		Serial: serial,
		Port:   port,
	}
	sess, err := capture.Start(a.ctx, opts,
		func(ev capture.FlowEvent) {
			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "capture:flow", ev)
			}
		},
		func(err error) {
			a.capturing = false
			a.paused = false
			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "capture:exit", fmt.Sprint(err))
			}
		})
	if err != nil {
		a.log("mitmdump 启动失败: %v", err)
		return err
	}
	a.session = sess
	a.capturing = true
	a.paused = false

	proxyHost := proxyHostFor(serial)
	if err := a.client.SetGlobalProxy(a.ctx, serial, fmt.Sprintf("%s:%d", proxyHost, port)); err != nil {
		_ = sess.Stop()
		a.capturing = false
		a.log("设置代理失败: %v", err)
		return err
	}
	a.log("抓包已开启: mitmdump :%d, 代理 %s:%d", port, proxyHost, port)
	if targetPkg != "" {
		_ = a.client.RestartApp(a.ctx, serial, targetPkg, "")
		a.log("目标 APP %s 已重启", targetPkg)
	}
	return nil
}

// StopCapture 停止抓包: 清代理 + 杀 mitmdump。
func (a *App) StopCapture() error {
	if a.client != nil && a.serial != "" {
		if err := a.client.SetGlobalProxy(a.ctx, a.serial, ":0"); err != nil {
			a.log("清除代理失败: %v", err)
		} else {
			a.log("已恢复 APP 直连 (代理清除)")
		}
	}
	if a.session != nil {
		if err := a.session.Stop(); err != nil {
			a.log("停止 mitmdump 异常: %v", err)
		}
		a.session = nil
	}
	a.capturing = false
	a.paused = false
	a.log("抓包已停止")
	return nil
}

// PauseCapture 暂停记录: 写入暂停标记, addon 停止上报事件, 但流量仍经代理正常转发。
// 与 StopCapture 的区别是不清代理、不杀 mitmdump, APP 网络行为完全不受影响。
func (a *App) PauseCapture() error {
	if !a.capturing {
		return fmt.Errorf("抓包未在进行中")
	}
	if err := capture.SetPaused(true); err != nil {
		a.log("暂停失败: %v", err)
		return err
	}
	a.paused = true
	a.log("已暂停记录: 流量继续转发, 不再新增请求行")
	return nil
}

// ResumeCapture 恢复记录: 删除暂停标记, addon 继续上报后续请求。
func (a *App) ResumeCapture() error {
	if !a.capturing {
		return fmt.Errorf("抓包未在进行中")
	}
	if err := capture.SetPaused(false); err != nil {
		a.log("恢复失败: %v", err)
		return err
	}
	a.paused = false
	a.log("已恢复记录")
	return nil
}

// CapturePaused 当前是否处于暂停记录状态。
func (a *App) CapturePaused() bool { return a.paused }

// ClearFlowBodies 清空落盘的完整响应体临时文件(前端清空请求列表时调用)。
func (a *App) ClearFlowBodies() { capture.CleanFlowBodies() }

// proxyHostFor 依据实例 serial 判定网络模式, 返回模拟器可访问的宿主机代理地址。
//   - NAT(默认): serial = 127.0.0.1:port, 模拟器访问宿主机固定走 NAT 网关别名 10.0.2.2
//   - 桥接: serial = <模拟器局域网IP>:5555, 取与模拟器同网段的本机网卡 IPv4
func proxyHostFor(serial string) string {
	host, _, err := net.SplitHostPort(serial)
	if err != nil || host == "" || host == "127.0.0.1" || host == "localhost" {
		return "10.0.2.2"
	}
	guest := net.ParseIP(host)
	if guest == nil {
		return "10.0.2.2"
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "10.0.2.2"
	}
	var fallback string
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP.To4() == nil || ipnet.IP.IsLoopback() {
			continue
		}
		if fallback == "" {
			fallback = ipnet.IP.String()
		}
		if ipnet.Contains(guest) {
			return ipnet.IP.String()
		}
	}
	if fallback != "" {
		return fallback
	}
	return "10.0.2.2"
}

// CaptureRunning 当前抓包状态。
func (a *App) CaptureRunning() bool { return a.capturing }

// CAExists CA 是否已生成。
func (a *App) CAExists() bool { return certstore.CAExists() }

// CopyText 文本写入系统剪贴板。
func (a *App) CopyText(text string) error {
	if a.ctx == nil {
		return fmt.Errorf("应用未就绪")
	}
	return runtime.ClipboardSetText(a.ctx, text)
}

// SaveResponseBody 将 addon 落盘的完整响应体另存到用户选择的位置。
// srcPath 为事件中的 resp_body_file(仅接受本工具临时目录内的 <id>.body, 防误读任意路径)。
func (a *App) SaveResponseBody(srcPath, suggestedName string) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("应用未就绪")
	}
	if srcPath == "" || !capture.IsFlowBodyPath(srcPath) {
		return "", fmt.Errorf("该请求无完整响应体(pending/空 body/超过 256MB 未落盘)")
	}
	if _, err := os.Stat(srcPath); err != nil {
		return "", fmt.Errorf("响应体临时文件已不存在: %v", err)
	}
	if suggestedName == "" {
		suggestedName = "response"
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:                "另存响应体",
		DefaultFilename:      suggestedName,
		CanCreateDirectories: true,
		Filters:              []runtime.FileFilter{{DisplayName: "所有文件", Pattern: "*.*"}},
	})
	if err != nil {
		return "", err
	}
	if path == "" { // 用户取消
		return "", nil
	}
	in, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer out.Close()
	n, err := io.Copy(out, in)
	if err != nil {
		return "", err
	}
	a.log("响应体已另存: %s (%s)", path, humanSize(n))
	return path, nil
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// HexDumpResult HEX 查看器分页结果。
type HexDumpResult struct {
	Text       string `json:"text"`        // hexdump 文本(xxd 风格, 每行末尾带回车)
	NextOffset int64  `json:"next_offset"` // 下一段起始偏移
	HasMore    bool   `json:"has_more"`    // 是否还有后续内容
	Total      int64  `json:"total"`        // 文件总字节数
}

// HexDump 读取完整响应体临时文件, 生成 xxd/hexdump -C 风格文本。
// 从 offset 起最多 maxBytes 字节(前端 64KB 分页加载)。
func (a *App) HexDump(srcPath string, offset, maxBytes int) (HexDumpResult, error) {
	if !capture.IsFlowBodyPath(srcPath) {
		return HexDumpResult{}, fmt.Errorf("该请求无完整响应体文件")
	}
	if offset < 0 {
		offset = 0
	}
	if maxBytes <= 0 || maxBytes > 1<<20 {
		maxBytes = 1 << 20
	}
	st, err := os.Stat(srcPath)
	if err != nil {
		return HexDumpResult{}, fmt.Errorf("响应体临时文件已不存在: %v", err)
	}
	total := st.Size()
	f, err := os.Open(srcPath)
	if err != nil {
		return HexDumpResult{}, err
	}
	defer f.Close()
	if _, err := f.Seek(int64(offset), io.SeekStart); err != nil {
		return HexDumpResult{}, err
	}
	buf := make([]byte, maxBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return HexDumpResult{}, err
	}
	buf = buf[:n]
	var b strings.Builder
	for i := 0; i < n; i += 16 {
		end := i + 16
		if end > n {
			end = n
		}
		fmt.Fprintf(&b, "%08x  ", offset+i)
		for j := 0; j < 16; j++ {
			if j == 8 {
				b.WriteByte(' ')
			}
			if i+j < n {
				fmt.Fprintf(&b, "%02x ", buf[i+j])
			} else {
				b.WriteString("   ")
			}
		}
		b.WriteString(" |")
		for j := i; j < end; j++ {
			if c := buf[j]; c >= 0x20 && c < 0x7f {
				b.WriteByte(c)
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteString("|\n")
	}
	next := int64(offset + n)
	return HexDumpResult{
		Text:       b.String(),
		NextOffset: next,
		HasMore:    next < total,
		Total:      total,
	}, nil
}

func configDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "my-mumu-debug")
}
