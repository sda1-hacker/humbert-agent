package commandenv

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveNameAbsoluteRelativeAndSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("测试文件使用 POSIX 可执行位")
	}
	root := t.TempDir()
	executable := filepath.Join(root, "test-tool")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	want, _ := filepath.EvalSymlinks(executable)
	for _, input := range []string{"test-tool", executable, "./test-tool", "alias"} {
		got, err := Resolve(input, root)
		if err != nil || got != want {
			t.Fatalf("%s: %s %v", input, got, err)
		}
	}
	t.Setenv("PATH", ".:relative:"+root)
	for _, item := range filepath.SplitList(Path()) {
		if !filepath.IsAbs(item) {
			t.Fatalf("不可信相对 PATH: %s", item)
		}
	}
	for _, input := range []string{"", "bad\x00path", "not-an-installed-program"} {
		if _, err := Resolve(input, root); err == nil {
			t.Fatalf("接受无效程序 %q", input)
		}
	}
}

func TestDesktopPathFindsHomebrewGo(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Finder PATH 回归只适用于 macOS")
	}
	if _, err := os.Stat("/opt/homebrew/bin/go"); err != nil {
		t.Skip("当前测试机未安装 Homebrew Go")
	}
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	got, err := Resolve("go", "")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks("/opt/homebrew/bin/go")
	if got != want || !strings.Contains(Path(), "/opt/homebrew/bin") {
		t.Fatalf("桌面 PATH 未补全: %s", got)
	}
}
