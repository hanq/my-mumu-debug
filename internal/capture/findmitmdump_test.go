package capture

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFindMitmdumpBundledPriority 锁定捆绑独立版的解析优先级:
// 项目根存在 third_party\mitmdump\mitmdump.exe 时, 开发态(CWD=项目根)应优先返回该路径。
func TestFindMitmdumpBundledPriority(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bundled := filepath.Join(root, "third_party", "mitmdump", "mitmdump.exe")
	if _, err := os.Stat(bundled); err != nil {
		t.Skip("third_party 捆绑版不存在, 跳过")
	}
	old, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	want := filepath.Join("third_party", "mitmdump", "mitmdump.exe")
	if got := FindMitmdump(); got != want {
		t.Fatalf("FindMitmdump() = %q, want %q", got, want)
	}
}