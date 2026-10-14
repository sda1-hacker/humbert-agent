package builtin

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

// gitReadFactory 将三种固定查询收敛为一个入口，仍强制使用原来的只读进程策略。
type gitReadFactory struct {
	runner         *sandbox.Runner
	environment    []string
	maxOutputBytes int
}

func NewGitFactory(runner *sandbox.Runner, environment []string, maxOutputBytes int) (humberttools.Factory, error) {
	if runner == nil || maxOutputBytes <= 0 {
		return nil, errors.New("Git 工具需要 Sandbox Runner 和有效输出上限")
	}
	return &gitReadFactory{runner: runner, environment: gitReadEnvironment(environment), maxOutputBytes: maxOutputBytes}, nil
}
func (f *gitReadFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{Name: "git", Risk: humberttools.RiskRead}
}

type GitInput struct {
	Action string `json:"action" jsonschema:"enum=status,enum=diff,enum=log,description=Read-only Git operation: status, diff, or log."`
	Path   string `json:"path,omitempty" jsonschema:"description=Repository directory, relative to the workspace or an allowed absolute path."`
	Staged bool   `json:"staged,omitempty" jsonschema:"description=For diff: show staged changes."`
	File   string `json:"file,omitempty" jsonschema:"description=For diff: optional repository-relative file path."`
	Limit  int    `json:"limit,omitempty" jsonschema:"description=For log: commit count, default 20, maximum 100."`
}
type GitReadOutput struct {
	Output        string `json:"output"`
	ExitCode      int    `json:"exit_code"`
	Truncated     bool   `json:"truncated"`
	NativeSandbox bool   `json:"native_sandbox"`
}

func (f *gitReadFactory) Build(ctx context.Context, scope humberttools.Scope) (einotool.InvokableTool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	actions := make([]string, 0, 3)
	allActions := scope.EnabledBuiltinTools == nil || slices.Contains(scope.EnabledBuiltinTools, "git")
	for _, action := range []string{"status", "diff", "log"} {
		if allActions || slices.Contains(scope.EnabledBuiltinTools, "git_"+action) {
			actions = append(actions, action)
		}
	}
	if len(actions) == 0 {
		return nil, errors.New("当前 Agent 未启用 Git 查询")
	}
	tool, err := utils.InferTool("git", "Read Git status, diff, or log without modifying the repository. External diff/textconv and fsmonitor are disabled; execution requires a read-only native sandbox.", func(callCtx context.Context, input *GitInput) (*GitReadOutput, error) {
		if input == nil {
			return nil, errors.New("git 输入不能为空")
		}
		action := strings.ToLower(strings.TrimSpace(input.Action))
		if !slices.Contains(actions, action) {
			return nil, errors.New("Git 动作无效或未被当前 Agent 启用")
		}
		args := []string{"status", "--short", "--branch"}
		switch action {
		case "diff":
			args = []string{"diff", "--no-ext-diff", "--no-textconv"}
			if input.Staged {
				args = append(args, "--cached")
			}
			args = append(args, "--")
			if input.File != "" {
				if strings.Contains(input.File, "..") || filepath.IsAbs(input.File) {
					return nil, errors.New("Git diff file 必须是仓库内相对路径")
				}
				args = append(args, input.File)
			}
		case "log":
			limit := input.Limit
			if limit <= 0 {
				limit = 20
			}
			args = []string{"log", fmt.Sprintf("-%d", min(limit, 100)), "--date=iso-strict", "--pretty=format:%h%x09%ad%x09%an%x09%s"}
		}
		path := strings.TrimSpace(input.Path)
		if path == "" {
			path = "."
		}
		return f.run(callCtx, scope, path, args)
	})
	if err != nil {
		return nil, err
	}
	// 旧 Profile 只选择 git_diff 时，既不向模型声明其他动作，也不允许执行其他动作。
	info, err := tool.Info(ctx)
	if err != nil {
		return nil, err
	}
	params, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		return nil, err
	}
	actionParam, ok := params.Properties.Get("action")
	if !ok {
		return nil, errors.New("Git action Schema 缺失")
	}
	actionParam.Enum = make([]any, len(actions))
	for i, action := range actions {
		actionParam.Enum[i] = action
	}
	return tool, nil
}

func (f *gitReadFactory) run(ctx context.Context, scope humberttools.Scope, path string, args []string) (*GitReadOutput, error) {
	policy := scope.SandboxPolicy()
	decision, err := policy.CheckPath(path, sandbox.OpList)
	if err != nil {
		return nil, fmt.Errorf("Git 工作目录被 Sandbox 拒绝: %w", err)
	}
	dir := decision.CanonicalPath
	gitPath, err := sandbox.ResolveCommand("git", scope.Workspace.RootDir)
	if err != nil {
		return nil, errors.New("系统未安装 git 或 git 不在 PATH")
	}
	gitPath, err = filepath.Abs(gitPath)
	if err != nil {
		return nil, err
	}
	out := newBoundedCommandOutput(f.maxOutputBytes)
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Git 的仓库配置可以启动外部程序。禁用已知入口，同时由操作系统兜底保证只读；
	// 即使用户关闭原生沙箱，也不能把标为 RiskRead 的工具静默降级成可写进程。
	args = append([]string{"--no-pager", "-c", "core.fsmonitor=false", "-c", "status.submoduleSummary=false"}, args...)
	result, err := f.runner.Run(cmdCtx, policy, sandbox.ProcessSpec{Executable: gitPath, Args: args, Dir: dir, Env: f.environment, Stdout: out, Stderr: out, ReadOnlyWorkspace: true})
	if err != nil {
		return nil, err
	}
	output, truncated := out.Result()
	return &GitReadOutput{Output: output, ExitCode: result.ExitCode, Truncated: truncated, NativeSandbox: result.NativeUsed}, nil
}

func gitReadEnvironment(values []string) []string {
	result := make([]string, 0, len(values)+1)
	found := false
	for _, value := range values {
		if strings.HasPrefix(strings.ToUpper(value), "GIT_OPTIONAL_LOCKS=") {
			result = append(result, "GIT_OPTIONAL_LOCKS=0")
			found = true
			continue
		}
		result = append(result, value)
	}
	if !found {
		result = append(result, "GIT_OPTIONAL_LOCKS=0")
	}
	return result
}
