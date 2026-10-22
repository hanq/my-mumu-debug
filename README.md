# my-mumu-debug

MuMu 模拟器 HTTP 抓包调试工具 —— 一键完成「检测模拟器 → adb 连接 → root → 安装抓包证书 → 抓取运行中 APP 的 HTTP 请求/响应」。

Wails v2 (Go + Vue3/TS) 桌面端。

## 快速开始

```bash
wails dev      # 开发
wails build    # 产出 build/bin/my-mumu-debug.exe
```

## 使用前提

| 依赖 | 说明 |
|---|---|
| MuMu 模拟器 12/15 | 设置中开启 adb 与 root 权限 |
| Python + mitmproxy | `pip install mitmproxy`（mitmdump 副进程） |
| adb | 优先用 MuMu 自带 adb，无需手动安装 |

## 使用流程

1. 点「检测」→ 选择实例 → 连接 ADB → 获取 Root
2. 点「取前台应用」确定目标 APP
3. 点「开启抓包」→ 工具自动完成装证书/设代理/重启 APP，右侧实时展示请求
4. 点「停止」恢复 APP 直连

## 文档

- [spec.md](spec.md) —— 需求与技术规格
- [AGENTS.md](AGENTS.md) —— 仓库约定与硬性技术事实
- [docs/research/](docs/research/) —— 实机调研（adb 连接 / 证书安装 / 抓包方案 / 踩坑记录）

## 已知边界

- IM 私有协议长连接（直连 TCP）不走 HTTP 代理，不在抓包范围
- 模拟器重启后证书失效（工具开启抓包时会自动重装，幂等）
- 目标服务端若启用证书绑定（pinning），对应请求会以 TLS 失败事件呈现
