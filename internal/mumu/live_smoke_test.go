// 针对实机的集成冒烟测试: 需要 MuMu 运行中, 否则跳过。
// 验收基准见 spec.md 第 7 节。
package mumu

import (
	"context"
	"testing"
	"time"

	"my-mumu-debug/internal/adb"
)

func TestLiveDetectAndCaptureChain(t *testing.T) {
	info := Detect()
	if !info.Installed {
		t.Skip("MuMu 未安装, 跳过实机测试")
	}
	t.Logf("MuMu 目录: %s", info.Dir)
	t.Logf("Manager:   %s", info.ManagerPath)
	if info.AdbPath == "" {
		t.Log("未找到自带 adb, 将回退 PATH")
	}

	instances, err := ListInstances(context.Background(), info.ManagerPath)
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(instances) == 0 {
		t.Skip("无实例")
	}
	inst := instances[0]
	t.Logf("实例: #%d %s serial=%s running=%v android=%s",
		inst.Index, inst.Name, inst.Serial, inst.Running, inst.AndroidVersion)
	if inst.Serial != "127.0.0.1:16384" {
		t.Errorf("serial = %s, want 127.0.0.1:16384", inst.Serial)
	}

	// adb 链路
	client := &adb.Client{AdbPath: info.AdbPath}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := client.Connect(ctx, inst.Serial); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := client.EnsureRoot(ctx, inst.Serial); err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	pkg, act, err := client.ForegroundApp(ctx, inst.Serial)
	if err != nil {
		t.Fatalf("ForegroundApp: %v", err)
	}
	t.Logf("前台应用: %s / %s", pkg, act)
	if pkg == "" {
		t.Error("前台包名为空")
	}

	pkgs, err := client.ThirdPartyPackages(ctx, inst.Serial)
	if err != nil || len(pkgs) == 0 {
		t.Fatalf("ThirdPartyPackages: %v (%d)", err, len(pkgs))
	}
	t.Logf("三方应用 %d 个", len(pkgs))

	conns, err := client.ProcessConnections(ctx, inst.Serial, pkg)
	if err != nil {
		t.Logf("ProcessConnections(非致命): %v", err)
	} else {
		t.Logf("进程 TCP 连接 %d 条", len(conns))
	}
}
