package builtin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

func commandTimeout(limits CommandLimits, seconds int) time.Duration {
	timeout := limits.DefaultTimeout
	if seconds > 0 {
		timeout = max(time.Duration(seconds)*time.Second, minimumCommandTimeout)
	}
	return min(timeout, limits.MaxTimeout)
}

// executeCaptured 不改变调用方提供的目录、环境和只读策略；统一超时、捕获和取消语义。
func executeCaptured(ctx context.Context, runner *sandbox.Runner, policy sandbox.EffectivePolicy, spec sandbox.ProcessSpec, limits CommandLimits, seconds int, toolName string) (sandbox.ProcessResult, string, bool, error) {
	runCtx, cancel := context.WithTimeout(ctx, commandTimeout(limits, seconds))
	defer cancel()
	capture := newBoundedCommandOutput(limits.MaxOutputBytes)
	spec.Stdout, spec.Stderr = capture, capture
	result, err := runner.Run(runCtx, policy, spec)
	if ctx.Err() != nil {
		return result, "", false, fmt.Errorf("%s 被取消: %w", toolName, ctx.Err())
	}
	if err != nil {
		return result, "", false, fmt.Errorf("%s Sandbox 执行失败: %w", toolName, err)
	}
	if result.TerminationReason == "" {
		result.TerminationReason = "exited"
	}
	output, truncated := capture.Result()
	return result, output, truncated, nil
}

// CommandLimits 描述 run_command 的资源边界。
type CommandLimits struct {
	DefaultTimeout time.Duration

	MaxTimeout time.Duration

	MaxOutputBytes int

	MaxArgs int

	MaxArgBytes int
}

// Validate 校验 CommandLimits。
func (l CommandLimits) Validate() error {
	if l.DefaultTimeout <= 0 {
		return errors.New("DefaultTimeout 必须大于 0")
	}

	if l.MaxTimeout <= 0 || l.DefaultTimeout > l.MaxTimeout {
		return errors.New("MaxTimeout 无效")
	}

	if l.MaxOutputBytes <= 0 {
		return errors.New("MaxOutputBytes 必须大于 0")
	}

	if l.MaxArgs <= 0 {
		return errors.New("MaxArgs 必须大于 0")
	}

	if l.MaxArgBytes <= 0 {
		return errors.New("MaxArgBytes 必须大于 0")
	}

	return nil
}

// cloneStringSlice 返回独立的字符串切片副本。
//
// 即使输入为空，也返回一个非 nil 的空切片。exec.Cmd 的 Env==nil 会继承当前进程
// 的全部环境变量，这会让 API Key、Token 等敏感信息意外暴露给 Agent 启动的子进程；
// 因此 run_command 必须显式使用非 nil 环境。
func cloneStringSlice(
	values []string,
) []string {
	cloned :=
		make([]string, len(values))

	copy(cloned, values)

	return cloned
}

func normalizeCommandName(command string) string { return strings.TrimSpace(command) }
func validateCommandName(command string) error   { return sandbox.ValidateCommand(command) }

func validateCommandArguments(
	args []string,
	limits CommandLimits,
) error {
	if len(args) > limits.MaxArgs {
		return fmt.Errorf("参数数量 %d 超过最大值 %d", len(args), limits.MaxArgs)
	}

	for index, arg := range args {
		if strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("第 %d 个参数包含 NUL", index+1)
		}

		if len(arg) > limits.MaxArgBytes {
			return fmt.Errorf("第 %d 个参数超过 %d bytes", index+1, limits.MaxArgBytes)
		}

		if !utf8.ValidString(arg) {
			return fmt.Errorf("第 %d 个参数不是有效 UTF-8", index+1)
		}
	}

	return nil
}

func resolveCommandWorkingDirectory(
	ctx context.Context,
	scope humberttools.Scope,
	input string,
) (
	absolute string,
	display string,
	err error,
) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	input = strings.TrimSpace(input)
	if input == "" {
		input = "."
	}
	decision, err := scope.SandboxPolicy().CheckPath(input, sandbox.OpList)
	if err != nil {
		return "", "", err
	}
	resolved := decision.CanonicalPath
	info, err := os.Stat(resolved)
	if err != nil {
		return "", "", fmt.Errorf("检查工作目录失败: %w", err)
	}
	if !info.IsDir() {
		return "", "", errors.New("working_directory 不是目录")
	}
	if rel, relErr := filepath.Rel(scope.Workspace.RootDir, resolved); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		display = filepath.ToSlash(rel)
		if display == "" {
			display = "."
		}
	} else {
		display = resolved
	}
	return resolved, display, nil
}

// commandResult 统一执行结果字段，脚本通过匿名嵌入保持原有扁平 JSON 协议。
func commandResult(command string, args []string, directory string, result sandbox.ProcessResult, output string, truncated bool, message string) RunCommandOutput {
	return RunCommandOutput{
		Command: command, Args: append([]string(nil), args...), WorkingDirectory: directory,
		ExitCode: result.ExitCode, TimedOut: result.TimedOut, TerminationReason: result.TerminationReason,
		Message: message, Output: output, OutputTruncated: truncated,
		DurationMS: result.Duration.Milliseconds(), NativeSandbox: result.NativeUsed,
	}
}
