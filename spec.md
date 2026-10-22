# my-mumu-debug 需求与技术规格（spec.md）

> 版本：v0.1.0-scaffold ｜ 2026-10-22 ｜ 依据 docs/research/ 四篇实机调研综合评估产出

## 1. 背景与目标

移动端 APP（如 com.gtex.timp）的 HTTP 接口联调目前依赖手工执行 adb/mitmproxy 命令链
（连接 → root → 装证书 → 设代理 → 启 mitmdump → 看日志），步骤多、坑多、不可复用。

**目标**：做一个 Windows 桌面工具，一键完成上述链条，并实时展示目标 APP 的 HTTP 请求/响应内容。

**非目标（本期不做）**：
- 抓取不走 HTTP 代理的私有协议长连接（IM 消息通道）——UI 明示此边界
- iOS / 真机（非 MuMu）支持
- 证书绑定（pinning）绕过
- 多模拟器品牌（只支持 MuMu 12/15 nx_device 架构）

## 2. 功能需求

### F1 环境检测
- 检测 MuMu 是否安装（进程/默认路径/注册表三级探测），定位 `MuMuManager.exe` 与自带 `adb.exe`
- 读取实例列表（`MuMuManager info -v all`）：名称、serial(`ip:port`)、运行状态
- 检测宿主机 adb 可用性；检测 mitmdump 可用性（PATH / pip 默认路径 / 手动配置）
- 检测 CA 是否已生成（`~/.mitmproxy/mitmproxy-ca-cert.pem`）

### F2 连接与 Root
- `adb connect <serial>`，状态回显
- `adb root` 提权 → 重连 → `id -u == 0` 校验，结果可视化

### F3 证书安装（幂等）
- CA 不存在时：短暂拉起 mitmdump 自动生成
- 计算 `subject_hash_old`（Go 内置 MD5 实现，不依赖 openssl）→ 生成 `<hash>.0`
- push 到 `/data/local/tmp` → 备份 APEX 证书集（<50 个即中止）→ `nsenter` 进每个 zygote 挂载 tmpfs → 恢复+追加 → chmod/chown/chcon
- 校验：证书数 ≈146 且包含 `<hash>.0`
- 安装后自动重启选中的目标 APP

### F4 获取运行中 APP
- 前台应用：解析 `dumpsys activity activities` 的 `topResumedActivity`（回退 `mCurrentFocus`）→ 包名+Activity
- 三方应用列表：`pm list packages -3`（供手动选择目标）
- 辅助诊断：目标进程当前 TCP 连接列表（判断流量是否走代理）

### F5 HTTP 抓包与展示
- 一键开启：装证书（可选跳过，若已装）→ 设代理 → 按端口启动 mitmdump 副进程（addon JSON 事件流）
- 一键停止：清代理(`:0`) → 按端口杀 mitmdump → 保留 flows 文件
- 实时列表：时间、方法、URL、状态码、耗时；点击查看请求头/请求体/响应头/响应体（自动解压+JSON 美化）
- 过滤：按 URL 关键字、方法、状态码；屏蔽噪声域名（如 tongji-collector.dcloud.net.cn）
- 落盘：会话日志 + `flows.mitm`（可用 `mitmweb -r` 回放），存放于 `%APPDATA%/my-mumu-debug/logs/`
- 异常事件：TLS 握手失败（提示可能存在证书绑定）、mitmdump 异常退出

### F6 状态机

```
[未检测] → 检测 → [已安装]
   → connect → [已连接] → root → [rootOK]
   → 装证书(幂等,可跳过) → [certOK] → 设代理+启动 mitmdump → [抓包中]
任意失败停留在当前态并给出可执行建议；[抓包中] 前提：证书OK + mitmdump 运行中
```

## 3. 技术架构

```
┌─ frontend (Vue3+TS+Vite) ──────────────────────┐
│ 状态面板 │ APP选择 │ 请求列表/详情(虚拟滚动)    │
└──────────────▲─────────────────────────────────┘
   wails 绑定调用 / EventsEmit("capture:flow" 等)
┌──────────────┴─ Go 后端 (Wails v2) ────────────┐
│ app.go(薄绑定)                                  │
│ internal/session   状态机编排                    │
│ internal/mumu      安装检测/MuMuManager         │
│ internal/adb       命令封装(serial/超时)        │
│ internal/certstore subject_hash_old + zygote安装│
│ internal/capture   mitmdump 副进程 + addon      │
└──────────────┬─────────────────────────────────┘
               │ spawn(PYTHONUNBUFFERED=1)
        mitmdump -s addon.py -p 8888 -w flows.mitm
               │ stdout JSON lines
        adb → MuMu 模拟器(Android 15, NAT 10.0.2.2)
```

**关键实现约束**（源自调研，红线级）：
- 所有 adb/MuMuManager 调用带 `context.WithTimeout`（connect 5s / root 10s / dumpsys 5s / MuMuManager 5s）
- subject_hash_old 用 Go 实现：`MD5(RawSubject)` 前 4 字节小端 uint32 → `%08x`（需用实机 CA 对拍 `c8750f0d` 校验，见 docs/research/02）
- mitmdump 启停：启动前查端口占用；停止按端口反查 PID；不按镜像名杀
- 事件单行 JSON ≤1MB（addon 侧已截断 body），Go Scanner 缓冲 1MB

## 4. 数据结构（核心）

```go
type EnvStatus struct {                       // F1
    MuMuInstalled   bool
    MuMuDir         string
    ManagerPath     string
    AdbPath         string                    // 优先 MuMu 自带
    MitmdumpPath    string                    // 空=未找到
    CAExists        bool
    CASSHA256       string                    // 证书指纹，前端展示一致性
    Instances       []EmuInstance
}
type EmuInstance struct {
    Index int; Name string; Serial string     // 127.0.0.1:16384
    Running bool; AndroidVersion string; PID  uint32
}
type AppInfo  struct { Package, Activity string }
type FlowEvent struct {                       // addon JSON
    Type string        // request|response|ws|tls_fail
    Ts, Method, URL string
    Status int; DurationMs int64
    ReqHeaders, RespHeaders map[string]string
    ReqBody, RespBody  string
    Truncated bool; Direction string          // ws: C->S / S->C
}
```

## 5. 里程碑

| 阶段 | 内容 | 验收 |
|---|---|---|
| M1 | F1+F2 检测/连接/root | 对实机 MuMu15 输出正确状态面板 |
| M2 | F3 证书安装 | `certs≈146, mitm=1`；APP 内 HTTPS 正常且信任 mitmproxy |
| M3 | F4 前台 APP/进程连接 | 与 `dumpsys` 手工结果一致 |
| M4 | F5 抓包全链路 | 操作 APP 列表实时滚动；body 可读；停止后 APP 直连恢复 |
| M5 | 打磨 | 过滤/详情/落盘/异常提示；`wails build` 单 exe + 使用说明 |

## 6. 风险与边界

| 风险 | 影响 | 对策 |
|---|---|---|
| IM 长连接不走代理 | 消息类流量缺失 | UI 明示 + TCP 连接诊断面板 |
| 模拟器重启证书失效 | 抓包失败(HTTPS 全挂) | 开抓包前默认重跑证书安装(幂等) |
| mitmdump 未安装 | 无法启动抓包 | 检测+安装指引文档；二期内置原生 MITM |
| 服务端证书绑定 | TLS-FAIL、无数据 | 事件上报+提示，属目标 APP 行为 |
| 端口冲突 | 启动失败 | 可配置端口+占用检测 |

## 7. 验收基准（来自实机调研数据）

- `MuMuManager info -v all` 实例 0：`127.0.0.1:16384`，Android 15，`start_finished`
- 证书安装后：zygote64/zygote32 两个 ns 均 `certs=146 mitm=1`
- 已验证抓到：`GET https://gtex-uat.grectronics.cn/api/base/app/user/auth/query_user_info` 200（含 gtex-token 头与 JSON body）
