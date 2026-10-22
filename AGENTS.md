# my-mumu-debug 项目指令（AGENTS.md）

> MuMu 模拟器 HTTP 抓包调试工具 —— Wails 桌面端。
> 本文件约束 AI 助手在本仓库内的行为；全局规则见 `~/.pi/agent/AGENTS.md`，两者叠加生效。

---

## 项目一句话

检测 MuMu 模拟器 → root → 安装抓包 CA 证书（zygote 命名空间）→ 连接 adb → 获取前台 APP →
通过 mitmdump 副进程抓取并实时展示该 APP 的 HTTP 请求/响应内容。

## 常用命令

```bash
wails dev              # 开发模式（热重载）
wails build            # 产出 build/bin/my-mumu-debug.exe
cd frontend && npm run build   # 仅构建前端
go vet ./... && go test ./...  # 后端静态检查/测试
```

## 目录约定

```
main.go / app.go        # Wails 入口与绑定层（薄壳，不写业务逻辑）
internal/mumu/          # MuMu 安装检测 + MuMuManager 解析
internal/adb/           # adb 命令封装（connect/root/shell/包名/前台应用）
internal/certstore/     # CA 生成、subject_hash_old、zygote 命名空间证书安装
internal/capture/       # mitmdump 副进程生命周期 + addon + JSON 事件流
internal/session/       # 抓包会话状态机（检测→连接→root→证书→代理→抓包中）
frontend/src/           # Vue3 + TS 单页（状态面板 / 请求列表 / 详情）
docs/research/          # 实机调研文档（改协议/流程前必读对应篇）
```

## 硬性技术事实（勿凭记忆推翻，详见 docs/research/）

1. MuMu adb 端口 = `16384 + 32×实例号`；必须手动 `adb connect`，宿主机在模拟器内是 `10.0.2.2`
2. Android 14+ 系统 CA 在 `/apex/com.android.conscrypt/cacerts`，且 APP 只能看到 **zygote 命名空间**里的挂载 —— 证书必须 `nsenter` 进每个 zygote 安装
3. 证书安装必须 **先备份后挂载**，备份文件数 <50 立即中止（防清空系统证书集）
4. 证书是 tmpfs 挂载，**模拟器重启即失效**，"安装证书"步骤按幂等设计、可重复执行
5. 安装证书后必须重启目标 APP（fork 才继承新命名空间）
6. mitmdump 在 Windows 是 wrapper+python 双进程 → 进程管理**只能按端口反查 PID**
7. 默认抓包端口 8888（8080 易被占）；mitmdump 启动必须 `PYTHONUNBUFFERED=1`
8. IM 长连接等私有协议不走 HTTP 代理，属能力边界，UI 需明示而非静默失败

## 编码规范

- 文件名一律 **snake_case**（`adb_client.go`、`capture_service.go`）；Go 包名小写单一名
- Go：导出函数带注释；错误用 `fmt.Errorf("...: %w", err)` 包裹；所有外部命令调用必须带超时 `exec.CommandContext`
- 前端组件 **PascalCase.vue**；composable 用 `useXxx.ts`
- **UI 禁止 emoji 作图标**：统一内联线性 SVG（`viewBox="0 0 24 24"`、`stroke="currentColor"`、`stroke-width="2"`、圆头端点）
- **窗口为无边框(Frameless)**：自定义标题栏用 `--wails-draggable: drag` 标记拖拽区，窗控按钮区域必须 `no-drag`；双击标题栏切换最大化
- **全局禁用默认焦点 outline**（WebView2 点击会出现黑框闪烁）：`button:focus { outline:none }`，键盘焦点用 `:focus-visible` 自定义样式
- 提示性文案用文字；日志/调试输出可用 emoji
- 新增外部命令调用时，把命令与参数记入 `docs/research/` 对应篇

## Git 约定

- 提交身份：`user.name=hanq`，`user.email=hanq@live.cn`（仓库级已配置，勿改动）
- 提交信息：`<type>: <summary>`，type ∈ feat/fix/docs/refactor/test/chore

## 变更红线

- 不修改 `~/.mitmproxy/`（用户全局 CA）；CA 生成仅限"文件不存在时"触发
- 不静默清除用户模拟器的代理设置（只有"停止抓包"按钮走 `:0`）
- 任何杀进程操作只针对：本工具启动的 mitmdump（按端口 8888/配置端口反查），不扩大范围
