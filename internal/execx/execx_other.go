//go:build !windows

// Package execx 统一封装子进程启动。
// 非 Windows 平台无控制台窗口问题, 直接透传 exec.CommandContext。
package execx

import (
	"context"
	"os/exec"
)

// Command 创建子进程命令(非 Windows 平台直接透传)。
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

// CommandNewConsole 非 Windows 平台无控制台窗口概念, 直接透传。
func CommandNewConsole(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
