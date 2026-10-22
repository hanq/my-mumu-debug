// Package certstore 实现抓包 CA 证书的生成与安装(Android 14+ APEX + zygote 命名空间)。
// 调研依据: docs/research/02-root-and-ca-cert-install.md —— 顺序与防呆不可调整。
package certstore

import (
	"context"
	"crypto/md5"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"my-mumu-debug/internal/adb"
	"my-mumu-debug/internal/capture"
)

// CAPath mitmproxy 默认 CA 位置(CA 必须跨会话稳定, 勿改)。
func CAPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mitmproxy", "mitmproxy-ca-cert.pem")
}

// CAExists CA 是否已生成。
func CAExists() bool {
	_, err := os.Stat(CAPath())
	return err == nil
}

// SubjectHashOld 计算 Android 证书文件名所需的 subject_hash_old(MD5 基旧算法)。
// 结果应为 8 位十六进制(如 c8750f0d)。
func SubjectHashOld(pemPath string) (string, error) {
	data, err := os.ReadFile(pemPath)
	if err != nil {
		return "", fmt.Errorf("读取 CA: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", fmt.Errorf("CA 文件不是有效的 PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("解析 CA 证书: %w", err)
	}
	sum := md5.Sum(cert.RawSubject)
	hash := binary.LittleEndian.Uint32(sum[:4])
	return fmt.Sprintf("%08x", hash), nil
}

// ensureBackupDir 生成设备端安装脚本(顺序红线: 先备份→校验→挂载→恢复→追加)。
func buildInstallScript(hash string) string {
	return `#!/system/bin/sh
# 1) 备份真实系统证书(挂载前); 已有备份则用当前视图刷新
mkdir -p /data/local/tmp/cacerts-orig
cp /apex/com.android.conscrypt/cacerts/* /data/local/tmp/cacerts-orig/ 2>/dev/null
N=$(ls /data/local/tmp/cacerts-orig | wc -l)
[ "$N" -lt 50 ] && { echo "backup too small: $N"; exit 2; }
# 2) tmpfs 覆盖 APEX 证书目录
mount -t tmpfs tmpfs /apex/com.android.conscrypt/cacerts || exit 1
# 3) 恢复原证书集 + 追加抓包证书
cp /data/local/tmp/cacerts-orig/* /apex/com.android.conscrypt/cacerts/
cp /data/local/tmp/` + hash + `.0 /apex/com.android.conscrypt/cacerts/
chmod 644 /apex/com.android.conscrypt/cacerts/*
chown root:root /apex/com.android.conscrypt/cacerts/*
chcon u:object_r:system_file:s0 /apex/com.android.conscrypt/cacerts/* 2>/dev/null
echo "certs=$(ls /apex/com.android.conscrypt/cacerts | wc -l) mitm=$(ls /apex/com.android.conscrypt/cacerts | grep -c ` + hash + `)"
`
}

var reInstallResult = regexp.MustCompile(`certs=(\d+) mitm=(\d+)`)

// Install 将抓包 CA 安装为系统级证书(幂等, 可在每次开启抓包前重复执行)。
// mitmdumpPath 用于在 CA 不存在时生成; 传空且 CA 不存在则报错。
func Install(ctx context.Context, client *adb.Client, serial, mitmdumpPath string) error {
	caPath := CAPath()
	if !CAExists() {
		if mitmdumpPath == "" {
			return fmt.Errorf("CA 不存在且未提供 mitmdump 路径, 无法生成")
		}
		if err := capture.GenerateCA(ctx, mitmdumpPath); err != nil {
			return fmt.Errorf("生成 CA: %w", err)
		}
	}
	hash, err := SubjectHashOld(caPath)
	if err != nil {
		return err
	}
	// 1) 生成 Android 格式证书文件并推送
	certName := hash + ".0"
	tmp, err := os.CreateTemp("", "cert-*.0")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := copyFile(caPath, tmp.Name()); err != nil {
		return err
	}
	if err := client.Push(ctx, serial, tmp.Name(), "/data/local/tmp/"+certName); err != nil {
		return err
	}
	// 2) 生成安装脚本并推送
	script := buildInstallScript(hash)
	tmpScript, err := os.CreateTemp("", "install_cert_apex-*.sh")
	if err != nil {
		return err
	}
	defer os.Remove(tmpScript.Name())
	if err := os.WriteFile(tmpScript.Name(), []byte(script), 0644); err != nil {
		return err
	}
	if err := client.Push(ctx, serial, tmpScript.Name(), "/data/local/tmp/install_cert_apex.sh"); err != nil {
		return err
	}
	// 3) 进入每个 zygote 命名空间执行安装(64/32 位 APP 各继承对应 zygote)
	zygotes, err := client.Shell(ctx, serial, 6*time.Second, "pidof zygote64; pidof zygote")
	if err != nil {
		return fmt.Errorf("查找 zygote: %w", err)
	}
	found := 0
	for _, z := range strings.Fields(zygotes) {
		found++
		out, err := client.Shell(ctx, serial, 15*time.Second,
			fmt.Sprintf("nsenter -t %s -m -- /system/bin/sh /data/local/tmp/install_cert_apex.sh", z))
		if err != nil {
			return fmt.Errorf("zygote(%s) 安装失败: %w (%s)", z, err, out)
		}
		m := reInstallResult.FindStringSubmatch(out)
		if m == nil || m[2] != "1" {
			return fmt.Errorf("zygote(%s) 安装校验失败: %s", z, out)
		}
		if len(m[1]) < 3 { // certs 位数防呆: 正常应为三位数(≈146)
			return fmt.Errorf("zygote(%s) 证书集异常: %s", z, out)
		}
	}
	if found == 0 {
		return fmt.Errorf("未找到 zygote 进程, 模拟器可能未完全启动")
	}
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
