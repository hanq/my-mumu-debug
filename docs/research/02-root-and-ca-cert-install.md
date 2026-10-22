# Root 权限与抓包 CA 证书安装（调研）

> 实测环境：MuMu 15.0 / Android 15 (SDK 35)。Android 14+ 的证书机制有重大变化，本文为最终可用方案。

## 1. Root

MuMu 支持开发者 root：

```bash
adb root          # 返回 "restarting adbd as root"
# 等待 adbd 重启后重连
adb connect 127.0.0.1:16384
adb shell id -u   # 输出 0 即成功（context=u:r:su:s0）
```

## 2. Android 14+ 证书机制变化（关键）

- 系统 CA 存储从 `/system/etc/security/cacerts` 迁移到 APEX：`/apex/com.android.conscrypt/cacerts`
- **应用进程的 TLS 信任链读 APEX 目录**，只写传统路径无效
- MuMu 的 `/system` remount 为只读（`mount -o rw,remount /` 失败），无法直接写入两个路径
- 可行方案：**tmpfs 覆盖 + nsenter 进入 zygote 挂载命名空间**

## 3. 挂载命名空间隔离（最大的坑）

实测命名空间分布：

| 进程 | mnt namespace |
|---|---|
| adb shell / init | `4026532208`（相同） |
| zygote64 / zygote32 | `4026532447` / `4026532446`（**不同！**） |

在 adb shell 里做的 tmpfs 挂载，**APP 进程看不到**（APP 从 zygote fork，继承 zygote 的 ns）。
必须用 `nsenter` 进入每个 zygote 的 mnt namespace 执行挂载，之后（重）启动的 APP 才能看到证书。

## 4. 标准安装流程（已验证，顺序不能错）

```bash
# 0) 准备：mitmproxy CA 证书 → Android 证书文件名
openssl x509 -inform PEM -subject_hash_old -in ~/.mitmproxy/mitmproxy-ca-cert.pem | head -1
#   输出如 c8750f0d → 证书文件名必须是 c8750f0d.0

adb push c8750f0d.0 /data/local/tmp/

# 1)【先备份，后挂载！】顺序反了会清空原证书集，导致整机 HTTPS 全挂
adb shell "mkdir -p /data/local/tmp/cacerts-orig
cp /apex/com.android.conscrypt/cacerts/* /data/local/tmp/cacerts-orig/"

# 2) 进入 zygote 命名空间挂载 tmpfs 并恢复 + 追加
for Z in $(pidof zygote64) $(pidof zygote); do
  adb shell "nsenter -t $Z -m -- /system/bin/sh /data/local/tmp/install_cert_apex.sh"
done
```

`install_cert_apex.sh`（设备端执行，root）：

```sh
#!/system/bin/sh
mkdir -p /data/local/tmp/cacerts-orig
cp /apex/com.android.conscrypt/cacerts/* /data/local/tmp/cacerts-orig/ 2>/dev/null
N=$(ls /data/local/tmp/cacerts-orig | wc -l)
[ "$N" -lt 50 ] && { echo "backup too small: $N"; exit 2; }   # 防呆：备份不完整立即终止
mount -t tmpfs tmpfs /apex/com.android.conscrypt/cacerts || exit 1
cp /data/local/tmp/cacerts-orig/* /apex/com.android.conscrypt/cacerts/
cp /data/local/tmp/<hash>.0 /apex/com.android.conscrypt/cacerts/
chmod 644 /apex/com.android.conscrypt/cacerts/*
chown root:root /apex/com.android.conscrypt/cacerts/*
chcon u:object_r:system_file:s0 /apex/com.android.conscrypt/cacerts/* 2>/dev/null
echo "certs=$(ls /apex/com.android.conscrypt/cacerts | wc -l) mitm=$(ls /apex/com.android.conscrypt/cacerts | grep -c <hash>)"
```

**幂等性**：同一开机周期内重复执行会叠加 tmpfs（无副作用）；备份目录内容恒等于当前完整证书集，重复恢复安全。

## 5. 证书生命周期

- **tmpfs 挂载在模拟器重启后失效** → 每次重启模拟器后需重新执行安装（幂等，可自动化）
- mitmproxy CA 本身持久保存在 `~/.mitmproxy/`（首次由 mitmdump 生成），hash 文件名恒定
- 校验方式：任取一个 zygote ns `ls /apex/com.android.conscrypt/cacerts | grep <hash>` 应为 1，且总数 ≈145+

## 6. 安装后必须重启目标 APP

APP fork 自 zygote。挂载前已启动的 APP 看不到新证书视图，
必须 `am force-stop <pkg>` + `am start` 重新拉起。
