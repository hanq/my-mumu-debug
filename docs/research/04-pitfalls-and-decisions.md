# 踩坑记录与工程决策

> 2026-10-22 实机调试全过程沉淀。开发时遇到"明明应该对却不对"的问题，先查这里。

## P1 Git-Bash/MSYS 路径转换（桌面工具用 Go 后可绕开，但脚本调试时会遇到）

- `MSYS_NO_PATHCONV=1` 禁用转换后，`/data/local/tmp` 不再被转成 `C:/Program Files/Git/data/...`
- **但副作用**：传给原生 exe（openssl / adb push 本地参数 / python）的参数必须自己给 `C:/...` 风格
- `//F`（taskkill）依赖路径转换变成 `/F`；`MSYS_NO_PATHCONV=1` 下必须直接写 `/F`
- Go 的 `exec.Command` 无此问题（参数原样传递）→ 工具化后此坑消失

## P2 Windows 进程/编码

- pip 的 mitmdump = wrapper.exe + python.exe 双进程，socket 在 python 子进程 → **按端口查杀**
- `tasklist /FI` 过滤输出受控制台代码页（GBK）影响，`grep` 大文件日志（mitmdump 输出）需 `iconv -f GBK`
- 端口 8080 极易被本地开发服务占用 → 默认端口选 8888，并做"端口被非本工具进程占用"的检测与提示
- Python 子进程 stdout 重定向到文件时**块缓冲** → 必须 `PYTHONUNBUFFERED=1`

## P3 证书安装

- 备份必须在挂载前（顺序反了会清空原证书集 → 整机 HTTPS 失效）；脚本内置备份文件数 <50 即中止的防呆
- shell ns ≠ zygote ns，nsenter 进 **每个 zygote** 都要装（64 位和 32 位 APP 各继承对应 zygote）
- Android 14+ 只认 APEX 路径；MuMu `/system` remount 只读，别浪费时间在传统路径
- 证书 hash 文件名 = `subject_hash_old` 的 8 位十六进制 + `.0`（注意是 **old** 算法，MD5 基）
- 重复挂载 tmpfs 会叠加但不破坏内容，脚本按幂等设计

## P4 网络与代理

- MuMu NAT 下宿主机 = `10.0.2.2`；代理设置是全局的（`settings put global`），重启模拟器**仍保留**（注意 on/off 配对）
- APP 层面可能无视系统代理（如 Cronet/自定义 socket/IM 长连接）→ 工具要能展示"APP 当前 TCP 连接"辅助判断
- 浏览器请求是最佳的链路自检样本（先浏览器后 APP，二分定位问题在链路还是 APP）

## P5 工程决策记录

| 决策 | 选择 | 理由 |
|---|---|---|
| 抓包引擎 | mitmdump 副进程 | 实机已验证；原生 Go MITM 留二期 |
| 事件通道 | addon stdout JSON lines → Wails EventsEmit | 低耦合、好调试、文件日志同时保留 |
| 默认端口 | 8888 | 8080 占用率高 |
| CA 目录 | `~/.mitmproxy`（mitmproxy 默认） | CA 跨会话稳定，hash 文件名不变 |
| adb 来源 | 优先 MuMu 自带 adb，回退 PATH | 减少环境依赖 |
| root 方式 | `adb root`（MuMu 官方支持） | 无需 su/superuser |
| UI 框架 | Vue3 + TS（wails vue-ts 模板） | 团队熟悉度、模板成熟 |
