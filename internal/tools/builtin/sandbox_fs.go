package builtin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

type sandboxTarget struct {
	root     *os.Root
	rootPath string
	relative string
	absolute string
	display  string
	decision sandbox.PathDecision
	policy   sandbox.EffectivePolicy
}

// openSandboxTarget 先通过 PathGuard 对具体业务动作做裁决，再使用命中的 PathRule Root
// 创建 os.Root。Tool 不再自行猜测“读 root / 写 root”，PathDecision 是唯一依据。
func openSandboxTarget(ctx context.Context, scope humberttools.Scope, input string, operation sandbox.PathOperation) (*sandboxTarget, error) {
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	policy := scope.SandboxPolicy()
	decision, err := policy.CheckPath(input, operation)
	if err != nil {
		return nil, err
	}
	if !decision.Allowed || strings.TrimSpace(decision.MatchedRoot) == "" {
		return nil, fmt.Errorf("Sandbox PathGuard 返回了无效裁决: %+v", decision)
	}

	rootPath := filepath.Clean(decision.MatchedRoot)
	absolute := filepath.Clean(decision.CanonicalPath)
	relative, err := filepath.Rel(rootPath, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("Sandbox 路径不属于裁决根目录: path=%s root=%s", absolute, rootPath)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("打开 Sandbox Root 失败: %w", err)
	}

	display := absolute
	if workspaceRel, relErr := filepath.Rel(policy.WorkspaceRoot, absolute); relErr == nil && workspaceRel != ".." && !strings.HasPrefix(workspaceRel, ".."+string(filepath.Separator)) {
		display = filepath.ToSlash(workspaceRel)
		if display == "" {
			display = "."
		}
	}
	return &sandboxTarget{
		root:     root,
		rootPath: rootPath,
		relative: relative,
		absolute: absolute,
		display:  display,
		decision: decision,
		policy:   policy,
	}, nil
}

// canReadChild 对每个后代重新裁决：父目录可读不代表其中的受保护目录也可读。
// 遍历期间被删除的文件直接跳过，其余策略或文件系统错误不能伪装成空结果。
func (t *sandboxTarget) canReadChild(relative string) (bool, error) {
	_, err := t.policy.CheckPath(filepath.Join(t.rootPath, relative), sandbox.OpRead)
	if errors.Is(err, sandbox.ErrPathDenied) || errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (t *sandboxTarget) Close() error {
	if t == nil || t.root == nil {
		return nil
	}
	return t.root.Close()
}
