//go:build windows

// Package execx 统一封装子进程启动: Windows 下隐藏控制台窗口。
// GUI 进程启动控制台子进程(adb/mitmdump/netstat/taskkill)时,
// 若不设置 CREATE_NO_WINDOW, 每次执行都会弹出黑色命令行窗口。
package execx

import (
	"context"
	"os/exec"
	"syscall"
)

// createNoWindow 对应 Windows 进程创建标志 CREATE_NO_WINDOW (0x08000000),
// 标准库 syscall 未定义该常量(x/sys/windows 才有), 故在此声明。
const createNoWindow = 0x08000000

// Command 创建隐藏窗口的子进程命令(仅 Windows 生效, 其余行为与 exec.CommandContext 一致)。
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	return cmd
}

// createNewConsole 对应 Windows 进程创建标志 CREATE_NEW_CONSOLE (0x00000010),
// 为控制台程序分配新的可见控制台窗口。
const createNewConsole = 0x00000010

// CommandNewConsole 创建带新控制台窗口的子进程命令(交互式 adb shell 等),
// 与 Command 相反: 会弹出可见窗口, 不设置 CREATE_NO_WINDOW。
func CommandNewConsole(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewConsole,
	}
	return cmd
}
