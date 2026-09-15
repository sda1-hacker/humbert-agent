package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ProcessSpec 描述一个不经过 shell 的本地进程。
type ProcessSpec struct {
	Executable string
	Args       []string
	Dir        string
	Env        []string
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

// ProcessResult 是统一 ProcessRunner 的终态。
type ProcessResult struct {
	ExitCode int
	TimedOut bool

	// TerminationReason 只描述 Runner 实际观察到的进程终态，不由上层根据 ExitCode 猜测。
	// 当前可能值：exited、timeout、signaled。
	TerminationReason string
	TerminationDetail string

	Duration   time.Duration
	NativeUsed bool
}

// Runner 是 run_command、Skill script、stdio MCP 共用的本地进程安全入口。
type Runner struct{ manager *Manager }

func NewRunner(manager *Manager) *Runner { return &Runner{manager: manager} }

func prepareRuntimeTemp(workspaceRoot string) (string, func(), error) {
	parent := filepath.Join(workspaceRoot, ".humbert", "runtime-tmp")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", nil, fmt.Errorf("创建 Sandbox Runtime 临时目录失败: %w", err)
	}
	dir, err := os.MkdirTemp(parent, "proc-*")
	if err != nil {
		return "", nil, fmt.Errorf("创建 Sandbox Runtime 私有临时目录失败: %w", err)
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

func withRuntimeTempEnvironment(values []string, runtimeTemp string) []string {
	result := make([]string, 0, len(values)+4)
	overrides := map[string]string{
		"TMPDIR": runtimeTemp,
		"TMP":    runtimeTemp,
		"TEMP":   runtimeTemp,
		// 避免 Python 从只读 Runtime/Library 路径导入模块时尝试写 __pycache__。
		"PYTHONDONTWRITEBYTECODE": "1",
	}
	seen := make(map[string]struct{}, len(overrides))
	for _, value := range values {
		key := value
		if index := strings.IndexByte(value, '='); index >= 0 {
			key = value[:index]
		}
		upper := strings.ToUpper(strings.TrimSpace(key))
		if replacement, ok := overrides[upper]; ok {
			if _, exists := seen[upper]; !exists {
				result = append(result, upper+"="+replacement)
				seen[upper] = struct{}{}
			}
			continue
		}
		result = append(result, value)
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP", "PYTHONDONTWRITEBYTECODE"} {
		if _, exists := seen[key]; exists {
			continue
		}
		result = append(result, key+"="+overrides[key])
	}
	return result
}

func (r *Runner) Run(ctx context.Context, policy EffectivePolicy, spec ProcessSpec) (ProcessResult, error) {
	if r == nil || r.manager == nil {
		return ProcessResult{}, errors.New("Sandbox ProcessRunner 未初始化")
	}
	if ctx == nil {
		return ProcessResult{}, errors.New("Sandbox ProcessRunner Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return ProcessResult{}, err
	}
	if err := policy.Validate(); err != nil {
		return ProcessResult{}, err
	}
	if strings.TrimSpace(spec.Executable) == "" {
		return ProcessResult{}, errors.New("Sandbox Process Executable 不能为空")
	}
	if !filepath.IsAbs(spec.Executable) {
		return ProcessResult{}, errors.New("Sandbox Process Executable 必须是绝对路径")
	}

	dir := strings.TrimSpace(spec.Dir)
	if dir == "" {
		dir = policy.WorkspaceRoot
	}
	decision, err := policy.CheckPath(dir, OpList)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("Sandbox 工作目录被拒绝: %w", err)
	}
	resolvedDir := decision.CanonicalPath

	if err := validateProcessIsolationPolicy(policy); err != nil {
		return ProcessResult{}, err
	}

	// 不把宿主 TMPDIR 原样交给受限子进程。macOS 的 TMPDIR 通常位于
	// /var/folders/...，它并不天然属于 Agent Sandbox 的可写范围；Python、Node、Git
	// 等运行时在启动阶段又可能访问临时目录。为避免合法命令因为临时目录越界而卡住或失败，
	// 每次执行都在 Workspace 内创建一个私有临时目录，并在进程结束后清理。
	runtimeTemp, tempCleanup, err := prepareRuntimeTemp(policy.WorkspaceRoot)
	if err != nil {
		return ProcessResult{}, err
	}
	defer tempCleanup()

	executable, args, nativeCleanup, nativeUsed, err := prepareNativeCommand(policy, spec.Executable, spec.Args, resolvedDir)
	if err != nil {
		return ProcessResult{}, err
	}
	if nativeCleanup != nil {
		defer nativeCleanup()
	}

	cmd := exec.Command(executable, args...)
	cmd.Dir = resolvedDir
	// Env 必须始终显式赋非 nil slice，避免 exec.Cmd 默认继承 Humbert 全部环境变量。
	cmd.Env = withRuntimeTempEnvironment(spec.Env, runtimeTemp)
	cmd.Stdin = spec.Stdin
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr

	beforeCleanup, err := configurePlatformProcess(cmd, policy)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("配置平台 Process Sandbox 失败: %w", err)
	}
	if beforeCleanup != nil {
		defer beforeCleanup()
	}

	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		return ProcessResult{}, fmt.Errorf("启动 Sandbox 进程失败: %w", err)
	}

	killTree, afterCleanup, err := attachPlatformProcess(cmd, policy)
	if err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return ProcessResult{}, fmt.Errorf("绑定进程树 Sandbox 失败: %w", err)
	}
	if afterCleanup != nil {
		defer afterCleanup()
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	var waitErr error
	timedOut := false
	select {
	case waitErr = <-waitCh:
	case <-ctx.Done():
		timedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
		if killTree != nil {
			_ = killTree()
		} else {
			_ = cmd.Process.Kill()
		}
		select {
		case waitErr = <-waitCh:
		case <-time.After(r.manager.Config().CommandGracePeriod):
			_ = cmd.Process.Kill()
			waitErr = <-waitCh
		}
	}

	result := ProcessResult{
		ExitCode:          0,
		TimedOut:          timedOut,
		TerminationReason: "exited",
		Duration:          time.Since(startedAt),
		NativeUsed:        nativeUsed,
	}
	if timedOut {
		result.ExitCode = -1
		result.TerminationReason = "timeout"
		result.TerminationDetail = ctx.Err().Error()
		return result, nil
	}
	if waitErr == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		// Go 在 Unix 进程被 signal 终止时会返回 ExitCode=-1。之前上层把这种情况
		// 错误展示为普通 "exited"，导致模型把真正的 Sandbox/Runtime 崩溃原因猜成
		// shell_enabled 或白名单问题。这里保留 Wait 的真实错误文本用于诊断。
		if result.ExitCode == -1 {
			result.TerminationReason = "signaled"
			result.TerminationDetail = exitErr.Error()
		}
		return result, nil
	}
	return ProcessResult{}, fmt.Errorf("等待 Sandbox 进程失败: %w", waitErr)
}
