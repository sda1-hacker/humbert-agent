package builtin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

const (
	runCommandToolName = "run_command"

	// 模型可选的 timeout_seconds 不能过短。原生 Sandbox 启动本身存在少量固定开销，
	// 1-2 秒的超时很容易把一次完全正常的短命令误判为失败。
	minimumCommandTimeout = 5 * time.Second

	runCommandToolDescription = `在当前 Sandbox 授权范围内执行本地程序。command 可填程序名或路径，args 逐项填写；不会隐式启动 Shell。命令可修改获准的目录；删除、未知脚本等由审批策略判断。临时文件请写入 TMPDIR。网页操作和网页截图使用 browser。exit_code=-1 时结合 termination_reason 区分 timeout 与 signaled。`
)

// RunCommandInput 是 run_command 的模型输入。
type RunCommandInput struct {
	// Command 支持程序名或路径；参数单独传入，不解析 Shell 命令字符串。
	Command string `json:"command" jsonschema:"description=Executable name or path, for example go or /opt/homebrew/bin/go. No shell syntax."`

	// Args 会逐项作为 argv 传递，不经过 Shell 解析。
	Args []string `json:"args,omitempty" jsonschema:"description=Argument vector passed directly to the executable. Do not combine multiple arguments into a shell command string."`

	// WorkingDirectory 支持沙盒允许的绝对目录或工作区相对目录，默认 "."。
	WorkingDirectory string `json:"working_directory,omitempty" jsonschema:"description=Absolute or workspace-relative working directory allowed by the active sandbox. Defaults to workspace root."`

	// TimeoutSeconds 允许模型缩短或适度延长单次命令时间，但不能突破配置硬上限。
	TimeoutSeconds int `json:"timeout_seconds,omitempty" jsonschema:"description=Optional timeout in seconds. The configured maximum is always enforced."`
}

// RunCommandOutput 是本地程序执行结果。
//
// 非零 ExitCode 是正常的命令结果而不是 Tool Transport Error，因此仍返回结构化
// Output，让模型可以根据编译错误、测试失败等内容继续修复。只有无法启动程序、
// 权限/路径校验失败或父 Context 被取消时才返回 Go error。
type RunCommandOutput struct {
	Command string `json:"command"`

	Args []string `json:"args,omitempty"`

	WorkingDirectory string `json:"working_directory"`

	ExitCode int `json:"exit_code"`

	TimedOut bool `json:"timed_out"`

	// TerminationReason 明确告诉模型进程为什么结束，避免仅凭 exit_code=-1 猜测配置问题。
	TerminationReason string `json:"termination_reason"`

	Message string `json:"message,omitempty"`

	Output string `json:"output,omitempty"`

	OutputTruncated bool `json:"output_truncated"`

	DurationMS int64 `json:"duration_ms"`

	NativeSandbox bool `json:"native_sandbox"`
}

// RunCommandFactory 为每个 RuntimeSnapshot 创建绑定 Workspace 的 run_command。
type RunCommandFactory struct {
	runner *sandbox.Runner

	limits CommandLimits

	environment []string
}

// NewRunCommandFactory 创建 run_command Factory。
//
// environment 必须由 Application Config Boundary 生成，Builtin Tool 不会自行继承
// os.Environ，从而避免把 Token/API Key 等敏感环境变量自动泄露给 Agent 脚本。
func NewRunCommandFactory(
	runner *sandbox.Runner,
	limits CommandLimits,
	environment []string,
) (*RunCommandFactory, error) {

	if runner == nil {
		return nil, errors.New("RunCommand Sandbox Runner 不能为空")
	}

	if err := limits.Validate(); err != nil {
		return nil, fmt.Errorf("RunCommand Limits 无效: %w", err)
	}

	return &RunCommandFactory{runner: runner, limits: limits, environment: cloneStringSlice(environment)}, nil
}

// Descriptor 返回 Humbert Registry 描述。
func (f *RunCommandFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{Name: runCommandToolName, Risk: humberttools.RiskExec}
}

// Build 创建绑定当前 Runtime Workspace 的 Eino Tool。
func (f *RunCommandFactory) Build(
	ctx context.Context,
	scope humberttools.Scope,
) (
	einotool.InvokableTool,
	error,
) {
	if ctx == nil {
		return nil, errors.New("构建 run_command 失败: context.Context 不能为空")
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("构建 run_command 被取消: %w", err)
	}

	return utils.InferTool(
		runCommandToolName,
		runCommandToolDescription,
		func(
			callCtx context.Context,
			input *RunCommandInput,
		) (
			*RunCommandOutput,
			error,
		) {
			return f.run(callCtx, scope, input)
		},
	)
}

func (f *RunCommandFactory) run(
	ctx context.Context,
	scope humberttools.Scope,
	input *RunCommandInput,
) (
	*RunCommandOutput,
	error,
) {
	if ctx == nil {
		return nil, errors.New("run_command: context.Context 不能为空")
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("run_command 被取消: %w", err)
	}

	if input == nil {
		return nil, errors.New("run_command 输入不能为空")
	}

	command := normalizeCommandName(input.Command)

	if err := validateCommandName(command); err != nil {
		return nil, fmt.Errorf("run_command command 无效: %w", err)
	}

	if err := validateCommandArguments(input.Args, f.limits); err != nil {
		return nil, fmt.Errorf("run_command args 无效: %w", err)
	}

	workingDirectory, displayDirectory, err := resolveCommandWorkingDirectory(ctx, scope, input.WorkingDirectory)

	if err != nil {
		return nil, fmt.Errorf("run_command working_directory 无效: %w", err)
	}

	executable, err := sandbox.ResolveCommand(command, workingDirectory)
	if err != nil {
		return nil, fmt.Errorf("run_command: %w", err)
	}

	timeout := commandTimeout(f.limits, input.TimeoutSeconds)
	processResult, output, truncated, err := executeCaptured(ctx, f.runner, scope.SandboxPolicy(), sandbox.ProcessSpec{
		Executable: executable, Args: append([]string(nil), input.Args...), Dir: workingDirectory,
		Env: cloneStringSlice(f.environment),
	}, f.limits, input.TimeoutSeconds, runCommandToolName)
	if err != nil {
		return nil, err
	}
	terminationReason := processResult.TerminationReason
	message := ""
	if processResult.TimedOut {
		message = fmt.Sprintf("命令执行超过 %s 后被 Humbert 终止。exit_code=-1 在这里表示超时，不表示 Shell 未启用。", timeout)
	} else if terminationReason == "signaled" {
		message = "本地进程被操作系统信号异常终止。"
		if detail := strings.TrimSpace(processResult.TerminationDetail); detail != "" {
			message += " 系统信息：" + detail
		}
		message += " 这通常表示本地运行时或原生 Sandbox 启动阶段失败；不要把它解释为 Shell 未启用。"
	}

	result := commandResult(command, input.Args, displayDirectory, processResult, output, truncated, message)
	return &result, nil
}
