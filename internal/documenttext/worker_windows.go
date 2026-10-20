package documenttext

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

func configureWorker(command *exec.Cmd) {
	command.Cancel = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		// 固定系统命令及 PID，不经 Shell；同时终止解析 worker 和它的页面渲染子进程。
		kill := exec.CommandContext(ctx, filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe"), "/T", "/F", "/PID", strconv.Itoa(command.Process.Pid))
		if err := kill.Run(); err != nil {
			return command.Process.Kill()
		}
		return nil
	}
}
