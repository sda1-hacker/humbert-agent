package instancelock

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// 子进程使用真实系统锁，避免只测到同一进程内的互斥。
func TestLockHelper(t *testing.T) {
	mode := os.Getenv("HUMBERT_LOCK_TEST_MODE")
	if mode == "" {
		return
	}
	lock, err := Acquire(os.Getenv("HUMBERT_LOCK_TEST_ROOT"))
	if mode == "busy" {
		if !errors.Is(err, ErrInUse) {
			t.Fatalf("expected busy, got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if mode == "hold" {
		fmt.Println("locked")
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
}

func lockChild(t *testing.T, root, mode string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLockHelper$")
	cmd.Env = append(os.Environ(), "HUMBERT_LOCK_TEST_MODE="+mode, "HUMBERT_LOCK_TEST_ROOT="+root)
	return cmd
}

func TestLockExcludesProcessesAcrossDataRestore(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "data")
	lock, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	if out, err := lockChild(t, root, "busy").CombinedOutput(); err != nil {
		t.Fatalf("second process: %v %s", err, out)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, root+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := lockChild(t, root, "busy").CombinedOutput(); err != nil {
		t.Fatalf("restore replaced lock: %v %s", err, out)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if out, err := lockChild(t, root, "free").CombinedOutput(); err != nil {
		t.Fatalf("unlock: %v %s", err, out)
	}
}

func TestLockReleasedAfterProcessCrash(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	cmd := lockChild(t, root, "hold")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("child not ready: %q %v", line, err)
	}
	if lock, err := Acquire(root); !errors.Is(err, ErrInUse) {
		if lock != nil {
			_ = lock.Close()
		}
		t.Fatalf("held lock accepted: %v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	lock, err := Acquire(root)
	if err != nil {
		t.Fatalf("crash left stale lock: %v", err)
	}
	defer lock.Close()
}
