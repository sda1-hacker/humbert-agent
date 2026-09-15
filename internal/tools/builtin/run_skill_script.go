package builtin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	"github.com/sda1-hacker/humbert-agent/internal/skills"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
	"github.com/sda1-hacker/humbert-agent/internal/workspace"
)

const (
	runSkillScriptToolName        = "run_skill_script"
	runSkillScriptToolDescription = `执行当前 Agent 已启用 Skill 的 scripts/ 脚本。

安全边界：
- 只能选择当前 Turn 已启用、已冻结 Identity 的 Skill；不能执行任意本地路径。
- script 必须位于该 Skill 的 scripts/ 目录；可传 scripts/check-update.sh 或 check-update.sh，两者会规范化为同一脚本身份，并且 Humbert 必须能识别其解释器。
- Skill 会先复制到当前 Workspace 的临时 Stage；真实 ~/.humbert-agent/skills 安装目录不会直接交给脚本修改。
- 解释器仍必须位于 security.shell_allowed_commands 白名单中。
- 执行继续经过 Permission + Approval、Agent Sandbox、网络策略、超时和输出上限。
- 不经过 Shell，不支持 &&、|、$() 等 Shell 拼接语法。
- 如果 exit_code=-1，必须结合 termination_reason 判断 timeout 或 signaled；白名单或 Sandbox 配置错误会以明确 Tool Error 返回。`
)

// RunSkillScriptInput 是 run_skill_script 的模型输入。
type RunSkillScriptInput struct {
	Skill string `json:"skill" jsonschema:"description=Name of an enabled Skill in the current turn."`

	Script string `json:"script" jsonschema:"description=Script path relative to the Skill scripts directory. Both extract.py and scripts/extract.py are accepted."`

	Args []string `json:"args,omitempty" jsonschema:"description=Arguments passed to the Skill script after the script path."`

	WorkingDirectory string `json:"working_directory,omitempty" jsonschema:"description=Relative working directory inside the current Agent workspace. Defaults to workspace root."`

	TimeoutSeconds int `json:"timeout_seconds,omitempty" jsonschema:"description=Optional timeout in seconds. The configured command maximum is always enforced."`
}

// RunSkillScriptOutput 是受控 Skill 脚本执行结果。
type RunSkillScriptOutput struct {
	Skill             string   `json:"skill"`
	Script            string   `json:"script"`
	Runtime           string   `json:"runtime"`
	Command           string   `json:"command"`
	Args              []string `json:"args,omitempty"`
	WorkingDirectory  string   `json:"working_directory"`
	ExitCode          int      `json:"exit_code"`
	TimedOut          bool     `json:"timed_out"`
	TerminationReason string   `json:"termination_reason"`
	Message           string   `json:"message,omitempty"`
	Output            string   `json:"output,omitempty"`
	OutputTruncated   bool     `json:"output_truncated"`
	DurationMS        int64    `json:"duration_ms"`
	NativeSandbox     bool     `json:"native_sandbox"`
}

// RunSkillScriptFactory 让标准 Agent Skill 的 scripts/ 能在现有 Humbert 安全边界内执行。
type RunSkillScriptFactory struct {
	skills          *skills.Manager
	workspaces      *workspace.Manager
	runner          *sandbox.Runner
	allowedCommands map[string]struct{}
	limits          CommandLimits
	environment     []string
}

func NewRunSkillScriptFactory(
	skillManager *skills.Manager,
	workspaceManager *workspace.Manager,
	runner *sandbox.Runner,
	allowedCommands []string,
	limits CommandLimits,
	environment []string,
) (*RunSkillScriptFactory, error) {
	if skillManager == nil {
		return nil, errors.New("RunSkillScript SkillManager 不能为空")
	}
	if workspaceManager == nil {
		return nil, errors.New("RunSkillScript WorkspaceManager 不能为空")
	}
	if runner == nil {
		return nil, errors.New("RunSkillScript Sandbox Runner 不能为空")
	}
	if err := limits.Validate(); err != nil {
		return nil, fmt.Errorf("RunSkillScript Limits 无效: %w", err)
	}
	allowed := make(map[string]struct{}, len(allowedCommands))
	for _, command := range allowedCommands {
		command = normalizeCommandName(command)
		if err := validateCommandName(command); err != nil {
			return nil, fmt.Errorf("RunSkillScript AllowedCommand 无效: %w", err)
		}
		allowed[command] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, errors.New("RunSkillScript AllowedCommands 不能为空")
	}
	return &RunSkillScriptFactory{
		skills:          skillManager,
		workspaces:      workspaceManager,
		runner:          runner,
		allowedCommands: allowed,
		limits:          limits,
		environment:     cloneStringSlice(environment),
	}, nil
}

func (f *RunSkillScriptFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{Name: runSkillScriptToolName, Risk: humberttools.RiskExec}
}

func (f *RunSkillScriptFactory) Build(
	ctx context.Context,
	scope humberttools.Scope,
) (tool.InvokableTool, error) {
	if ctx == nil {
		return nil, errors.New("构建 run_skill_script 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("构建 run_skill_script 被取消: %w", err)
	}

	identities, revision, err := f.skills.RuntimeIdentities(ctx, scope.EnabledSkills)
	if err != nil {
		return nil, fmt.Errorf("构建 run_skill_script Skill Snapshot 失败: %w", err)
	}
	if scope.SkillRevision != "" && revision != scope.SkillRevision {
		return nil, fmt.Errorf("构建 run_skill_script 失败: %w", skills.ErrSkillSnapshotStale)
	}

	return utils.InferTool(
		runSkillScriptToolName,
		runSkillScriptToolDescription,
		func(callCtx context.Context, input *RunSkillScriptInput) (*RunSkillScriptOutput, error) {
			return f.run(callCtx, scope, identities, input)
		},
	)
}

func (f *RunSkillScriptFactory) run(
	ctx context.Context,
	scope humberttools.Scope,
	identities map[string]string,
	input *RunSkillScriptInput,
) (*RunSkillScriptOutput, error) {
	if ctx == nil {
		return nil, errors.New("run_skill_script: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("run_skill_script 被取消: %w", err)
	}
	if input == nil {
		return nil, errors.New("run_skill_script 输入不能为空")
	}

	skillName := strings.TrimSpace(input.Skill)
	expectedIdentity, enabled := identities[skillName]
	if !enabled || expectedIdentity == "" {
		return nil, fmt.Errorf("Skill %q 未在当前 Turn 启用", skillName)
	}

	pkg, err := f.skills.Get(ctx, skillName)
	if err != nil {
		return nil, fmt.Errorf("读取 Skill %q 失败: %w", skillName, err)
	}
	if pkg.Info.Identity != expectedIdentity {
		return nil, fmt.Errorf("%w: Skill %q 已在当前 Turn 期间发生变化", skills.ErrSkillSnapshotStale, skillName)
	}

	scriptPath, err := skills.NormalizeScriptPath(input.Script)
	if err != nil {
		return nil, fmt.Errorf("run_skill_script script 无效: %w", err)
	}
	var scriptRuntime *skills.ScriptRuntime
	for index := range pkg.Info.ScriptRuntimes {
		if pkg.Info.ScriptRuntimes[index].Path == scriptPath {
			value := pkg.Info.ScriptRuntimes[index]
			scriptRuntime = &value
			break
		}
	}
	if scriptRuntime == nil {
		return nil, fmt.Errorf("Skill %q 中不存在可识别脚本 %q", skillName, scriptPath)
	}
	if !scriptRuntime.Supported || strings.TrimSpace(scriptRuntime.Command) == "" {
		return nil, fmt.Errorf("脚本 %q 的运行时当前不受支持", scriptPath)
	}
	command := normalizeCommandName(scriptRuntime.Command)
	if _, allowed := f.allowedCommands[command]; !allowed {
		return nil, fmt.Errorf("Skill 脚本解释器 %q 不在 security.shell_allowed_commands 白名单中", command)
	}
	if err := validateCommandArguments(input.Args, f.limits); err != nil {
		return nil, fmt.Errorf("run_skill_script args 无效: %w", err)
	}
	executable, err := exec.LookPath(command)
	if err != nil {
		return nil, fmt.Errorf("找不到 Skill 脚本解释器 %q: %w", command, err)
	}
	if !filepath.IsAbs(executable) {
		executable, err = filepath.Abs(executable)
		if err != nil {
			return nil, fmt.Errorf("解析 Skill 脚本解释器路径失败: %w", err)
		}
	}
	resolvedExecutable, resolveErr := filepath.EvalSymlinks(executable)
	if resolveErr != nil {
		return nil, fmt.Errorf("解析 Skill 脚本解释器真实路径失败: %w", resolveErr)
	}
	executable = resolvedExecutable

	workingDirectory, displayDirectory, err := resolveCommandWorkingDirectory(ctx, scope, input.WorkingDirectory)
	if err != nil {
		return nil, fmt.Errorf("run_skill_script working_directory 无效: %w", err)
	}

	stageDecision, err := scope.SandboxPolicy().CheckPath(filepath.Join(".humbert", "skill-runs"), sandbox.OpCreate)
	if err != nil {
		return nil, fmt.Errorf("准备 Skill Stage 目录失败: %w", err)
	}
	stageParent := stageDecision.CanonicalPath
	if err := os.MkdirAll(stageParent, 0o700); err != nil {
		return nil, fmt.Errorf("创建 Skill Stage 父目录失败: %w", err)
	}
	stageRoot := filepath.Join(stageParent, uuid.NewString())
	if _, err := f.skills.StagePackage(ctx, skillName, expectedIdentity, stageRoot); err != nil {
		return nil, err
	}
	defer os.RemoveAll(stageRoot)

	stagedScript := filepath.Join(stageRoot, filepath.FromSlash(scriptPath))
	if _, err := os.Stat(stagedScript); err != nil {
		return nil, fmt.Errorf("Stage 后找不到脚本 %q: %w", scriptPath, err)
	}

	timeout := f.limits.DefaultTimeout
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
	argv := make([]string, 0, len(input.Args)+1)
	argv = append(argv, stagedScript)
	argv = append(argv, input.Args...)
	processResult, err := f.runner.Run(
		runCtx,
		scope.SandboxPolicy(),
		sandbox.ProcessSpec{
			Executable: executable,
			Args:       argv,
			Dir:        workingDirectory,
			Env:        cloneStringSlice(f.environment),
			Stdout:     capture,
			Stderr:     capture,
		},
	)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("run_skill_script 被取消: %w", ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("run_skill_script Sandbox 执行失败: %w", err)
	}
	output, truncated := capture.Result()
	terminationReason := processResult.TerminationReason
	if terminationReason == "" {
		terminationReason = "exited"
	}
	message := ""
	if processResult.TimedOut {
		message = fmt.Sprintf(
			"Skill 脚本执行超过 %s 后被 Humbert 终止。exit_code=-1 在这里表示超时。",
			timeout,
		)
	} else if terminationReason == "signaled" {
		message = "Skill 脚本进程被操作系统信号异常终止。"
		if detail := strings.TrimSpace(processResult.TerminationDetail); detail != "" {
			message += " 系统信息：" + detail
		}
		message += " 这通常表示脚本运行时或原生 Sandbox 启动阶段失败。"
	}
	return &RunSkillScriptOutput{
		Skill:             skillName,
		Script:            scriptPath,
		Runtime:           scriptRuntime.Language,
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
