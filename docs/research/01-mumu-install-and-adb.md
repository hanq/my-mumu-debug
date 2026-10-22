# MuMu 模拟器安装检测与 ADB 连接（调研）

> 调研环境：MuMu 15.0（nx_device 架构）、Windows 10/11、2026-10-22 实测通过

## 1. 安装目录结构

默认安装路径：`C:\Program Files\Netease\MuMu\`

```
MuMu/
├── nx_main/MuMuManager.exe        # 官方实例管理工具（查 adb 端口等）
├── nx_device/15.0/shell/          # 模拟器本体 + 自带 adb
│   ├── MuMuNxDevice.exe           # 模拟器主进程
│   ├── adb.exe / AdbWinApi.dll
│   └── NemuShell.exe
├── nx_device/<版本>/shell/        # 多版本共存（12.0 / 15.0 ...）
├── vms/MuMuPlayer-15.0-0/         # 各实例数据
└── uninstall.exe
```

检测方式（按优先级）：
1. 运行进程探测：`MuMuNxDevice.exe` / `MuMuPlayer.exe` 的可执行文件路径
2. 默认路径探测：`C:\Program Files\Netease\MuMu`、`C:\Program Files (x86)\Netease\MuMu`
3. 注册表卸载项：`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall` 下 Netease/MuMu 相关键

## 2. MuMuManager 查询实例信息

```bat
"C:\Program Files\Netease\MuMu\nx_main\MuMuManager.exe" info -v all
```

输出 JSON（实测样例）：

```json
{
  "0": {
    "adb_host_ip": "127.0.0.1",
    "adb_port": 16384,
    "android_version": "15.0",
    "is_android_started": true,
    "is_process_started": true,
    "player_state": "start_finished",
    "pid": 56448,
    "name": "MuMu安卓设备",
    "index": "0",
    "vt_enabled": true,
    "hyperv_enabled": true
  }
}
```

**端口规则**：主实例 adb 端口 = `16384`，多开实例 = `16384 + 32×实例号`
（实例 1 = 16416，实例 2 = 16448 …）

兼容说明：模拟器进程同时监听 `5555 / 7555 / 16384`（旧版兼容端口），**只用 MuMuManager 查到的端口**。

## 3. ADB 连接

- MuMu 自带 adb：`nx_device/<版本>/shell/adb.exe`；宿主机 PATH 中若有 platform-tools 也可用
- 连接：`adb connect 127.0.0.1:16384`（**必须手动 connect，不会自动出现在设备列表**）
- 模拟器重启后需重新 connect
- 多份 adb 共存（自带 + scoop）共用同一个 adb server；若报 `adb server version doesn't match`，统一版本即可

## 4. 关键判断

| 检查项 | 命令 | 通过标准 |
|---|---|---|
| MuMu 已安装 | 目录/注册表/进程探测 | 找到 MuMuManager.exe |
| 实例已启动 | `MuMuManager info -v all` | `is_process_started=true` |
| adb 已连接 | `adb -s <serial> get-state` | `device` |
| root 可用 | `adb root` 后 `adb shell id -u` | 输出 `0` |
