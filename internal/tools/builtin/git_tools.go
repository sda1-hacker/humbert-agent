package builtin

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

const (
	gitStatusToolName = "git_status"
	gitDiffToolName   = "git_diff"
	gitLogToolName    = "git_log"
)

type gitReadFactory struct {
	name           string
	description    string
	runner         *sandbox.Runner
	environment    []string
	maxOutputBytes int
}

func NewGitStatusFactory(runner *sandbox.Runner, environment []string, maxOutputBytes int) (humberttools.Factory, error) {
	return newGitReadFactory(gitStatusToolName, "Show concise Git working-tree and branch status without modifying the repository.", runner, environment, maxOutputBytes)
}
func NewGitDiffFactory(runner *sandbox.Runner, environment []string, maxOutputBytes int) (humberttools.Factory, error) {
	return newGitReadFactory(gitDiffToolName, "Show Git diff for the current repository without modifying it.", runner, environment, maxOutputBytes)
}
func NewGitLogFactory(runner *sandbox.Runner, environment []string, maxOutputBytes int) (humberttools.Factory, error) {
	return newGitReadFactory(gitLogToolName, "Show recent Git commit history without modifying the repository.", runner, environment, maxOutputBytes)
}
func newGitReadFactory(name, description string, runner *sandbox.Runner, env []string, max int) (*gitReadFactory, error) {
	if runner == nil {
		return nil, errors.New("Git Tool Sandbox Runner 不能为空")
	}
	if max <= 0 {
		return nil, errors.New("Git Tool maxOutputBytes 必须大于 0")
	}
	return &gitReadFactory{name: name, description: description, runner: runner, environment: gitReadEnvironment(env), maxOutputBytes: max}, nil
}
func (f *gitReadFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{Name: f.name, Risk: humberttools.RiskRead}
}

type GitStatusInput struct {
	Path string `json:"path,omitempty" jsonschema:"description=Repository directory. Relative paths resolve from the workspace."`
}
type GitDiffInput struct {
	Path   string `json:"path,omitempty" jsonschema:"description=Repository directory. Relative paths resolve from the workspace."`
	Staged bool   `json:"staged,omitempty" jsonschema:"description=Show staged changes instead of unstaged changes."`
	File   string `json:"file,omitempty" jsonschema:"description=Optional repository-relative file path to limit the diff."`
}
type GitLogInput struct {
	Path  string `json:"path,omitempty" jsonschema:"description=Repository directory. Relative paths resolve from the workspace."`
	Limit int    `json:"limit,omitempty" jsonschema:"description=Number of commits. Defaults to 20 and is capped at 100."`
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
	switch f.name {
	case gitStatusToolName:
		return utils.InferTool(f.name, f.description, func(callCtx context.Context, input *GitStatusInput) (*GitReadOutput, error) {
			path := "."
			if input != nil && strings.TrimSpace(input.Path) != "" {
				path = input.Path
			}
			return f.run(callCtx, scope, path, []string{"status", "--short", "--branch"})
		})
	case gitDiffToolName:
		return utils.InferTool(f.name, f.description, func(callCtx context.Context, input *GitDiffInput) (*GitReadOutput, error) {
			path := "."
			if input != nil && strings.TrimSpace(input.Path) != "" {
				path = input.Path
			}
			args := []string{"diff", "--no-ext-diff", "--"}
			if input != nil && input.Staged {
				args = []string{"diff", "--cached", "--no-ext-diff", "--"}
			}
			if input != nil && strings.TrimSpace(input.File) != "" {
				if strings.Contains(input.File, "..") || strings.HasPrefix(input.File, "/") {
					return nil, errors.New("git_diff file 必须是仓库内相对路径")
				}
				args = append(args, input.File)
			}
			return f.run(callCtx, scope, path, args)
		})
	case gitLogToolName:
		return utils.InferTool(f.name, f.description, func(callCtx context.Context, input *GitLogInput) (*GitReadOutput, error) {
			path := "."
			limit := 20
			if input != nil {
				if strings.TrimSpace(input.Path) != "" {
					path = input.Path
				}
				if input.Limit > 0 {
					limit = input.Limit
				}
			}
			if limit > 100 {
				limit = 100
			}
			return f.run(callCtx, scope, path, []string{"log", fmt.Sprintf("-%d", limit), "--date=iso-strict", "--pretty=format:%h%x09%ad%x09%an%x09%s"})
		})
	default:
		return nil, fmt.Errorf("未知 Git Tool %q", f.name)
	}
}

func (f *gitReadFactory) run(ctx context.Context, scope humberttools.Scope, path string, args []string) (*GitReadOutput, error) {
	policy := scope.SandboxPolicy()
	decision, err := policy.CheckPath(path, sandbox.OpList)
	if err != nil {
		return nil, fmt.Errorf("Git 工作目录被 Sandbox 拒绝: %w", err)
	}
	dir := decision.CanonicalPath
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, errors.New("系统未安装 git 或 git 不在 PATH")
	}
	gitPath, err = filepathAbs(gitPath)
	if err != nil {
		return nil, err
	}
	out := newBoundedCommandOutput(f.maxOutputBytes)
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := f.runner.Run(cmdCtx, policy, sandbox.ProcessSpec{Executable: gitPath, Args: args, Dir: dir, Env: f.environment, Stdout: out, Stderr: out})
	if err != nil {
		return nil, err
	}
	output, truncated := out.Result()
	return &GitReadOutput{Output: output, ExitCode: result.ExitCode, Truncated: truncated, NativeSandbox: result.NativeUsed}, nil
}

func filepathAbs(path string) (string, error) { return filepath.Abs(path) }
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
