//go:build !windows

package documenttext

import (
	"os/exec"
	"syscall"
)

// 解析器可能启动页面渲染器；取消必须覆盖整个进程组，避免只杀 worker 留下渲染任务。
func configureWorker(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
}
