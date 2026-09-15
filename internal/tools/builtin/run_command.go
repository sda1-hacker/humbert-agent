package builtin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

const (
	runCommandToolName = "run_command"

	// 模型可选的 timeout_seconds 不能过短。原生 Sandbox 启动本身存在少量固定开销，
	// 1-2 秒的超时很容易把一次完全正常的短命令误判为失败。
	minimumCommandTimeout = 5 * time.Second

	runCommandToolDescription = `在当前 Agent Workspace 中执行一个经过白名单允许的本地程序。

该工具不会启动 Shell，也不会解析 "&&"、"|"、"$()" 等 Shell 语法。
请把程序名称放在 command，把每个参数分别放在 args 中。

例如：
- Go 测试：command="go", args=["test", "./..."]
- Python 脚本：command="python3", args=["scripts/check.py"]
- Node 脚本：command="node", args=["scripts/check.js"]

本地执行属于高风险能力，只有用户显式启用 security.shell_enabled，且 command
位于 shell_allowed_commands 白名单中时，该工具才会注册。

如果当前模型能够看到并调用 run_command，说明本地程序能力已经在本次 Runtime 中启用。
白名单不匹配会直接返回明确的 Tool Error；exit_code=-1 必须结合 termination_reason 判断：
- timed_out=true / termination_reason="timeout" 表示超时；
- termination_reason="signaled" 表示进程被操作系统信号异常终止。
不要把这两种情况解释成 shell_enabled 未开启或程序不在白名单。`
)

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
		return errors.New(
			"DefaultTimeout 必须大于 0",
		)
	}

	if l.MaxTimeout <= 0 ||
		l.DefaultTimeout > l.MaxTimeout {
		return errors.New(
			"MaxTimeout 无效",
		)
	}

	if l.MaxOutputBytes <= 0 {
		return errors.New(
			"MaxOutputBytes 必须大于 0",
		)
	}

	if l.MaxArgs <= 0 {
		return errors.New(
			"MaxArgs 必须大于 0",
		)
	}

	if l.MaxArgBytes <= 0 {
		return errors.New(
			"MaxArgBytes 必须大于 0",
		)
	}

	return nil
}

// RunCommandInput 是 run_command 的模型输入。
type RunCommandInput struct {
	// Command 只能是程序名称，不能是路径，也不能是一整段 Shell 命令。
	Command string `json:"command" jsonschema:"description=Allowed executable name only, for example go, git, python3 or node. Do not include a path or shell syntax."`

	// Args 会逐项作为 argv 传递，不经过 Shell 解析。
	Args []string `json:"args,omitempty" jsonschema:"description=Argument vector passed directly to the executable. Do not combine multiple arguments into a shell command string."`

	// WorkingDirectory 是 Workspace 内的相对目录，默认 "."。
	WorkingDirectory string `json:"working_directory,omitempty" jsonschema:"description=Relative working directory inside the current workspace. Defaults to workspace root."`

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
	workspaces *workspace.Manager

	runner *sandbox.Runner

	allowedCommands map[string]struct{}

	limits CommandLimits

	environment []string
}

// NewRunCommandFactory 创建 run_command Factory。
//
// environment 必须由 Application Config Boundary 生成，Builtin Tool 不会自行继承
// os.Environ，从而避免把 Token/API Key 等敏感环境变量自动泄露给 Agent 脚本。
func NewRunCommandFactory(
	workspaceManager *workspace.Manager,
	runner *sandbox.Runner,
	allowedCommands []string,
	limits CommandLimits,
	environment []string,
) (*RunCommandFactory, error) {
	if workspaceManager == nil {
		return nil, errors.New(
			"RunCommand WorkspaceManager 不能为空",
		)
	}

	if runner == nil {
		return nil, errors.New("RunCommand Sandbox Runner 不能为空")
	}

	if err := limits.Validate(); err != nil {
		return nil, fmt.Errorf(
			"RunCommand Limits 无效: %w",
			err,
		)
	}

	allowed :=
		make(
			map[string]struct{},
			len(allowedCommands),
		)

	for _, command := range allowedCommands {
		command =
			normalizeCommandName(
				command,
			)

		if err :=
			validateCommandName(
				command,
			); err != nil {
			return nil, fmt.Errorf(
				"RunCommand AllowedCommand 无效: %w",
				err,
			)
		}

		allowed[command] =
			struct{}{}
	}

	if len(allowed) == 0 {
		return nil, errors.New(
			"RunCommand AllowedCommands 不能为空",
		)
	}

	return &RunCommandFactory{
		workspaces: workspaceManager,

		runner: runner,

		allowedCommands: allowed,

		limits: limits,

		environment: cloneStringSlice(
			environment,
		),
	}, nil
}

// Descriptor 返回 Humbert Registry 描述。
func (f *RunCommandFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{
		Name: runCommandToolName,

		Risk: humberttools.RiskExec,
	}
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
		return nil, errors.New(
			"构建 run_command 失败: context.Context 不能为空",
		)
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf(
			"构建 run_command 被取消: %w",
			err,
		)
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
			return f.run(
				callCtx,
				scope,
				input,
			)
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
		return nil, errors.New(
			"run_command: context.Context 不能为空",
		)
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf(
			"run_command 被取消: %w",
			err,
		)
	}

	if input == nil {
		return nil, errors.New(
			"run_command 输入不能为空",
		)
	}

	command :=
		normalizeCommandName(
			input.Command,
		)

	if err :=
		validateCommandName(
			command,
		); err != nil {
		return nil, fmt.Errorf(
			"run_command command 无效: %w",
			err,
		)
	}

	if _, allowed :=
		f.allowedCommands[command]; !allowed {
		return nil, fmt.Errorf(
			"run_command 程序 %q 不在 security.shell_allowed_commands 白名单中",
			command,
		)
	}

	if err :=
		validateCommandArguments(
			input.Args,
			f.limits,
		); err != nil {
		return nil, fmt.Errorf(
			"run_command args 无效: %w",
			err,
		)
	}

	workingDirectory,
		displayDirectory,
		err :=
		resolveCommandWorkingDirectory(
			ctx,
			scope,
			input.WorkingDirectory,
		)

	if err != nil {
		return nil, fmt.Errorf(
			"run_command working_directory 无效: %w",
			err,
		)
	}

	executable, err :=
		exec.LookPath(
			command,
		)

	if err != nil {
		return nil, fmt.Errorf(
			"run_command 找不到程序 %q: %w",
			command,
			err,
		)
	}

	if !filepath.IsAbs(
		executable,
	) {
		absolute,
			absErr :=
			filepath.Abs(
				executable,
			)

		if absErr != nil {
			return nil, fmt.Errorf(
				"run_command 解析程序路径失败: %w",
				absErr,
			)
		}

		executable =
			absolute
	}

	resolvedExecutable, resolveErr := filepath.EvalSymlinks(executable)
	if resolveErr != nil {
		return nil, fmt.Errorf("run_command 解析程序真实路径失败: %w", resolveErr)
	}
	executable = resolvedExecutable

	timeout :=
		f.limits.
			DefaultTimeout

	if input.TimeoutSeconds > 0 {
		timeout = time.Duration(input.TimeoutSeconds) * time.Second
		if timeout < minimumCommandTimeout {
			timeout = minimumCommandTimeout
		}
	}

	if timeout > f.limits.MaxTimeout {
		timeout = f.limits.MaxTimeout
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	capture := newBoundedCommandOutput(f.limits.MaxOutputBytes)
	processResult, err := f.runner.Run(
		runCtx,
		scope.SandboxPolicy(),
		sandbox.ProcessSpec{
			Executable: executable,
			Args:       append([]string(nil), input.Args...),
			Dir:        workingDirectory,
			Env:        cloneStringSlice(f.environment),
			Stdout:     capture,
			Stderr:     capture,
		},
	)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("run_command 被取消: %w", ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("run_command Sandbox 执行失败: %w", err)
	}

	output,
		truncated :=
		capture.Result()

	terminationReason := processResult.TerminationReason
	if terminationReason == "" {
		terminationReason = "exited"
	}
	message := ""
	if processResult.TimedOut {
		message = fmt.Sprintf(
			"命令执行超过 %s 后被 Humbert 终止。exit_code=-1 在这里表示超时，不表示 Shell 未启用或程序不在白名单。",
			timeout,
		)
	} else if terminationReason == "signaled" {
		message = "本地进程被操作系统信号异常终止。"
		if detail := strings.TrimSpace(processResult.TerminationDetail); detail != "" {
			message += " 系统信息：" + detail
		}
		message += " 这通常表示本地运行时或原生 Sandbox 启动阶段失败；不要把它解释为 Shell 未启用或命令不在白名单。"
	}

	return &RunCommandOutput{
		Command:           command,
		Args:              append([]string(nil), input.Args...),
		WorkingDirectory:  displayDirectory,
		ExitCode:          processResult.ExitCode,
		TimedOut:          processResult.TimedOut,
		TerminationReason: terminationReason,
		Message:           message,
		Output:            output,
		OutputTruncated:   truncated,
		DurationMS:        processResult.Duration.Milliseconds(),
		NativeSandbox:     processResult.NativeUsed,
	}, nil
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
		make(
			[]string,
			len(values),
		)

	copy(
		cloned,
		values,
	)

	return cloned
}

func normalizeCommandName(
	command string,
) string {
	command =
		strings.TrimSpace(
			command,
		)

	if runtime.GOOS ==
		"windows" {
		command =
			strings.ToLower(
				command,
			)
	}

	return command
}

func validateCommandName(
	command string,
) error {
	if command == "" {
		return errors.New(
			"程序名称不能为空",
		)
	}

	if len(command) > 128 {
		return errors.New(
			"程序名称过长",
		)
	}

	if strings.ContainsRune(
		command,
		'\x00',
	) {
		return errors.New(
			"程序名称不能包含 NUL",
		)
	}

	if filepath.Base(command) !=
		command ||
		strings.ContainsAny(
			command,
			`/\`,
		) {
		return errors.New(
			"只能提供程序名称，不能提供路径",
		)
	}

	return nil
}

func validateCommandArguments(
	args []string,
	limits CommandLimits,
) error {
	if len(args) >
		limits.MaxArgs {
		return fmt.Errorf(
			"参数数量 %d 超过最大值 %d",
			len(args),
			limits.MaxArgs,
		)
	}

	for index, arg := range args {
		if strings.ContainsRune(
			arg,
			'\x00',
		) {
			return fmt.Errorf(
				"第 %d 个参数包含 NUL",
				index+1,
			)
		}

		if len(arg) >
			limits.MaxArgBytes {
			return fmt.Errorf(
				"第 %d 个参数超过 %d bytes",
				index+1,
				limits.MaxArgBytes,
			)
		}

		if !utf8.ValidString(arg) {
			return fmt.Errorf(
				"第 %d 个参数不是有效 UTF-8",
				index+1,
			)
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

// boundedCommandOutput 对 stdout/stderr 做固定内存预算。
//
// 为了同时保留命令开头上下文和结尾错误信息，超过预算以后保留 head + tail，
// 中间被丢弃。Write 永远返回 len(p)，因此不会因为 UI 输出达到上限而向子进程
// 制造 Broken Pipe。
type boundedCommandOutput struct {
	mu sync.Mutex

	limit int

	headLimit int

	tailLimit int

	head bytes.Buffer

	tail []byte

	total int64
}

func newBoundedCommandOutput(
	limit int,
) *boundedCommandOutput {
	headLimit :=
		limit / 2

	if headLimit == 0 {
		headLimit =
			limit
	}

	return &boundedCommandOutput{
		limit: limit,

		headLimit: headLimit,

		tailLimit: limit - headLimit,
	}
}

func (b *boundedCommandOutput) Write(
	p []byte,
) (
	int,
	error,
) {
	originalLength :=
		len(p)

	b.mu.Lock()
	defer b.mu.Unlock()

	b.total +=
		int64(
			originalLength,
		)

	remainingHead :=
		b.headLimit -
			b.head.Len()

	if remainingHead > 0 {
		take :=
			remainingHead

		if len(p) < take {
			take =
				len(p)
		}

		_, _ =
			b.head.Write(
				p[:take],
			)

		p =
			p[take:]
	}

	if len(p) > 0 &&
		b.tailLimit > 0 {
		b.tail =
			append(
				b.tail,
				p...,
			)

		if len(b.tail) >
			b.tailLimit {
			overflow :=
				len(b.tail) -
					b.tailLimit

			copy(
				b.tail,
				b.tail[overflow:],
			)

			b.tail =
				b.tail[:b.tailLimit]
		}
	}

	return originalLength,
		nil
}

func (b *boundedCommandOutput) Result() (
	string,
	bool,
) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.total <=
		int64(
			b.limit,
		) {
		combined :=
			append(
				[]byte(nil),
				b.head.Bytes()...,
			)

		combined =
			append(
				combined,
				b.tail...,
			)

		return strings.ToValidUTF8(
				string(combined),
				"�",
			),
			false
	}

	dropped :=
		b.total -
			int64(
				b.head.Len(),
			) -
			int64(
				len(b.tail),
			)

	var builder strings.Builder

	builder.Write(
		b.head.Bytes(),
	)

	fmt.Fprintf(
		&builder,
		"\n... [truncated %d bytes] ...\n",
		dropped,
	)

	builder.Write(
		b.tail,
	)

	return strings.ToValidUTF8(
			builder.String(),
			"�",
		),
		true
}
