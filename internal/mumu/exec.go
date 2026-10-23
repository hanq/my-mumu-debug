package mumu

import (
	"context"
	"strconv"

	"my-mumu-debug/internal/execx"
)

// execCmd 执行外部命令并返回 stdout(与 adb 包解耦的底层执行器)
func execCmd(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := execx.Command(ctx, name, args...)
	return cmd.Output()
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
