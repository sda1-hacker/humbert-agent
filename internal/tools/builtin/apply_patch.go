package builtin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

const applyPatchToolName = "apply_patch"

type PatchChange struct {
	Path    string `json:"path" jsonschema:"description=File to edit. Relative paths are resolved from the workspace."`
	OldText string `json:"old_text" jsonschema:"description=Exact existing text to replace. It must occur exactly once. Use empty old_text only with create=true."`
	NewText string `json:"new_text" jsonschema:"description=Replacement text or full content for create=true."`
	Create  bool   `json:"create,omitempty" jsonschema:"description=Create a new file. old_text must be empty and the target must not exist."`
}
type ApplyPatchInput struct {
	Changes []PatchChange `json:"changes" jsonschema:"description=One or more exact text replacements. All changes are validated before writes begin."`
}
type ApplyPatchOutput struct {
	Files   []string `json:"files"`
	Changes int      `json:"changes"`
}

type ApplyPatchFactory struct{ maxBytes int64 }

func NewApplyPatchFactory(maxBytes int64) (*ApplyPatchFactory, error) {
	if maxBytes <= 0 {
		return nil, errors.New("apply_patch maxBytes 必须大于 0")
	}
	return &ApplyPatchFactory{maxBytes: maxBytes}, nil
}
func (f *ApplyPatchFactory) Descriptor() humberttools.Descriptor {
	return humberttools.Descriptor{Name: applyPatchToolName, Risk: humberttools.RiskWrite}
}
func (f *ApplyPatchFactory) Build(ctx context.Context, scope humberttools.Scope) (einotool.InvokableTool, error) {
	return utils.InferTool(applyPatchToolName, "Apply several exact text edits to UTF-8 files allowed by the Agent Sandbox. Every change is validated before any write; old_text must match exactly once.", func(callCtx context.Context, input *ApplyPatchInput) (*ApplyPatchOutput, error) {
		return f.run(callCtx, scope, input)
	})
}

type preparedPatch struct {
	target  *sandboxTarget
	data    []byte
	mode    fs.FileMode
	existed bool
	display string
}

func (f *ApplyPatchFactory) run(ctx context.Context, scope humberttools.Scope, input *ApplyPatchInput) (*ApplyPatchOutput, error) {
	if input == nil || len(input.Changes) == 0 {
		return nil, errors.New("apply_patch 至少需要一个 change")
	}
	if len(input.Changes) > 32 {
		return nil, errors.New("apply_patch 单次最多 32 个 change")
	}
	prepared := make([]preparedPatch, 0, len(input.Changes))
	closeAll := func() {
		for i := range prepared {
			prepared[i].target.Close()
		}
	}
	committed := false
	defer func() {
		if !committed {
			closeAll()
		}
	}()
	seen := map[string]struct{}{}
	for i, ch := range input.Changes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if strings.TrimSpace(ch.Path) == "" {
			return nil, fmt.Errorf("change %d path 不能为空", i+1)
		}
		target, err := openSandboxTarget(ctx, scope, ch.Path, sandbox.OpModify)
		if err != nil {
			return nil, fmt.Errorf("change %d 路径被 Sandbox 拒绝: %w", i+1, err)
		}
		if target.relative == "." {
			target.Close()
			return nil, fmt.Errorf("change %d 必须指定文件", i+1)
		}
		if _, ok := seen[target.absolute]; ok {
			target.Close()
			return nil, fmt.Errorf("同一个文件 %q 在一次 apply_patch 中只能出现一次", target.display)
		}
		seen[target.absolute] = struct{}{}
		var data []byte
		mode := fs.FileMode(0o600)
		existed := false
		info, statErr := target.root.Lstat(target.relative)
		if ch.Create {
			if ch.OldText != "" {
				target.Close()
				return nil, fmt.Errorf("create change %d 的 old_text 必须为空", i+1)
			}
			if statErr == nil {
				target.Close()
				return nil, fmt.Errorf("create 目标 %q 已存在", target.display)
			}
			if !errors.Is(statErr, fs.ErrNotExist) {
				target.Close()
				return nil, statErr
			}
			data = []byte(ch.NewText)
		} else {
			if statErr != nil {
				target.Close()
				return nil, fmt.Errorf("读取 %q 失败: %w", target.display, statErr)
			}
			if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
				target.Close()
				return nil, fmt.Errorf("%q 不是普通文件", target.display)
			}
			if info.Size() > f.maxBytes {
				target.Close()
				return nil, fmt.Errorf("%q 超过大小限制", target.display)
			}
			file, err := target.root.Open(target.relative)
			if err != nil {
				target.Close()
				return nil, err
			}
			original, err := io.ReadAll(io.LimitReader(file, f.maxBytes+1))
			file.Close()
			if err != nil {
				target.Close()
				return nil, err
			}
			if int64(len(original)) > f.maxBytes {
				target.Close()
				return nil, fmt.Errorf("%q 超过大小限制", target.display)
			}
			count := strings.Count(string(original), ch.OldText)
			if ch.OldText == "" || count != 1 {
				target.Close()
				return nil, fmt.Errorf("%q old_text 必须非空且恰好匹配一次，实际 %d 次", target.display, count)
			}
			data = []byte(strings.Replace(string(original), ch.OldText, ch.NewText, 1))
			existed = true
			if info.Mode().Perm() != 0 {
				mode = info.Mode().Perm()
			}
		}
		if int64(len(data)) > f.maxBytes {
			target.Close()
			return nil, fmt.Errorf("修改后 %q 超过大小限制", target.display)
		}
		prepared = append(prepared, preparedPatch{target: target, data: data, mode: mode, existed: existed, display: target.display})
	}
	files := make([]string, 0, len(prepared))
	for _, p := range prepared {
		if err := ensureRootParentDirectory(p.target.root, p.target.relative); err != nil {
			return nil, err
		}
		if err := atomicWriteWorkspaceFile(ctx, p.target.root, p.target.relative, p.data, p.mode, p.existed); err != nil {
			return nil, fmt.Errorf("写入 %q 失败: %w", p.display, err)
		}
		files = append(files, p.display)
	}
	closeAll()
	committed = true
	return &ApplyPatchOutput{Files: files, Changes: len(prepared)}, nil
}
