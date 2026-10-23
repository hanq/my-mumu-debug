# HTTP 抓包方案（调研）

> 结论：采用 **mitmdump 副进程 + JSON 事件流** 方案（已在实机验证），原生 Go MITM 作为二期备选。

## 1. 原理链路

```
MuMu 模拟器(Android)                     Windows 宿主机
┌─────────────────────┐   NAT(10.0.2.2)  ┌──────────────────────┐
│ APP ──► 系统代理 ────┼────────────────► │ mitmdump :8888       │
│ (settings http_proxy)│                  │  ├─ CA: ~/.mitmproxy │
│                      │                  │  ├─ addon(JSON stdout)├──► 桌面工具(Go)
│ 长连接(IM私有协议) ──┼─ 直连, 不走代理  │  └─ flows.mitm 文件   │    Wails EventsEmit
└─────────────────────┘                   └──────────────────────┘
```

- 模拟器为 NAT 网络（`10.0.2.15/24`），**宿主机地址固定 `10.0.2.2`**
- 设置全局代理：`adb shell settings put global http_proxy 10.0.2.2:<port>`
- 清除代理（恢复直连）：`adb shell settings put global http_proxy :0`
- mitmdump 监听地址用 `0.0.0.0`（实测可用）

## 2. 为什么选 mitmdump 副进程

| 方案 | 优点 | 缺点 | 结论 |
|---|---|---|---|
| **mitmdump 副进程** | 已实机验证；HTTP/2、WebSocket、gzip 解压、证书生成全内置 | 依赖 Python+mitmproxy 环境 | ✅ 一期 |
| 原生 Go MITM (elazarl/goproxy 等) | 单 exe 分发、无 Python | 需自管证书/协议边界，未验证 | 二期备选 |
| tcpdump | 无需代理 | HTTPS 加密不可读 | 仅作诊断 |
| VPN 型工具(HttpCanary) | 能抓绕过代理的流量 | 装在模拟器内、交互差 | 特殊场景 |

**mitmdump 发现顺序**（工具实现用）：
1. 用户配置路径（配置文件）
2. `exec.LookPath("mitmdump")` PATH 查找
3. `%LOCALAPPDATA%\Python\pythoncore-*\Scripts\mitmdump.exe`（pip 官方安装器默认位置）

首次运行生成 CA：短暂启动 `mitmdump --set confdir=...` 后即产生 `~/.mitmproxy/mitmproxy-ca-cert.pem`，杀掉即可（hash 文件名由此证书派生，保持 confdir 固定则 CA 稳定）。

## 3. 进程管理（重要实测结论）

pip 安装的 mitmdump 在 Windows 上是 **wrapper(mitmdump.exe) + python.exe 双进程**，
socket 归 python 子进程持有：

- ❌ 按镜像名查杀不可靠（`tasklist /FI "IMAGENAME eq mitmdump.exe"` 受控制台编码/过滤影响）
- ✅ **按监听端口反查 PID 杀**：`netstat -ano | findstr :8888` → PID → `taskkill /F /PID <pid>`
- 启动前先检测目标端口是否被占（8080 极易被占，**默认用 8888** 且可配置）
- 检测逻辑只能用"端口是否 LISTENING"判断自身是否在运行

## 4. addon 事件流设计

Python addon（`mitm_addon.py`）将每个事件输出为**一行 JSON 到 stdout**，
Go 侧 `bufio.Scanner`（缓冲 ≥1MB）逐行解析 → Wails 事件推送到前端：

```json
{"type":"response","ts":"10-22 12:17:43","method":"GET","url":"https://...","status":200,"duration_ms":142,"req_headers":{...},"req_body":"","resp_headers":{...},"resp_body":"{...}","truncated":false}
```

addon 要点（继承实测脚本）：
- 响应体 `get_text()` 自动解 gzip；JSON 再 `json.dumps(indent=2)` 美化
- 二进制 content-type（image/font/octet-stream/video/audio）只记类型+字节数
- 单 body 截断 8000 字符，`truncated:true` 标注
- WebSocket：`websocket_message` 钩子，方向 `C->S`/`S->C`
- TLS 失败：`tls_failed_client/server` 钩子（识别证书绑定/不信任）
- stdout 必须**无缓冲**（环境变量 `PYTHONUNBUFFERED=1`），否则日志延迟/丢失

## 5. 目标 APP 的实测行为（ limitations ）

- 业务 API 走系统代理 → **可抓** ✅
- IM 长连接直连腾讯 `118.89.191.1:443`（私有协议 over 裸 TCP）→ **不走代理，抓不到** ❌
  - 识别方法：`adb shell netstat -tunp | grep <pid>` 看是否出现 `10.0.2.2:8888` 连接
- DCloud/uni-app 的 JS console 日志：logcat tag `console`（与抓包互补）
- 服务端如启用证书绑定（pinning），addon 的 TLS-FAIL 事件会暴露

## 6. 验证过的完整启动序列

1. 确认 mitmdump 存在、CA 存在（无则生成）
2. 确认端口未被占（占用且非本工具的则报错/换端口）
3. root → 安装证书（见 02 文档）→ 设置代理 → **重启目标 APP**
4. 启动 mitmdump（PYTHONUNBUFFERED=1, `-s addon -p 8888 -w flows.mitm`）
5. 停止：清代理(`:0`) → 按端口杀进程 → 保留 flows 文件
