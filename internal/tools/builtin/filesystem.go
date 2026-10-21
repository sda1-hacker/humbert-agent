package builtin

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/adk"
	filesystemmw "github.com/cloudwego/eino/adk/middlewares/filesystem"
	einotool "github.com/cloudwego/eino/components/tool"

	"github.com/sda1-hacker/humbert-agent/internal/fileops"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

// NewFilesystemFactories 保留能力选择和权限风险，六个文件工具共用一个 Backend 实现。
func NewFilesystemFactories(limits fileops.Limits, maxWritableBytes int64) ([]humberttools.Factory, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	if maxWritableBytes <= 0 {
		return nil, errors.New("文件写入上限必须大于 0")
	}
	var factories []humberttools.Factory
	for _, name := range []string{"list_files", "read_file", "write_file", "edit_file", "glob_files", "grep_files"} {
		factories = append(factories, &filesystemFactory{name: name, limits: limits, maxWritableBytes: maxWritableBytes})
	}
	return factories, nil
}

type filesystemFactory struct {
	name             string
	limits           fileops.Limits
	maxWritableBytes int64
}

func (f *filesystemFactory) Descriptor() humberttools.Descriptor {
	risk := humberttools.RiskRead
	if f.name == "write_file" || f.name == "edit_file" {
		risk = humberttools.RiskWrite
	}
	return humberttools.Descriptor{Name: f.name, Risk: risk}
}
func (f *filesystemFactory) Build(ctx context.Context, scope humberttools.Scope) (einotool.InvokableTool, error) {
	config := func(name string) *filesystemmw.ToolConfig {
		return &filesystemmw.ToolConfig{Name: name, Disable: name != f.name}
	}
	backend, err := fileops.NewBackend(scope.SandboxPolicy(), f.limits, f.maxWritableBytes)
	if err != nil {
		return nil, err
	}
	handler, err := filesystemmw.New(ctx, &filesystemmw.MiddlewareConfig{
		Backend:      backend,
		LsToolConfig: config("list_files"), ReadFileToolConfig: config("read_file"), WriteFileToolConfig: config("write_file"), EditFileToolConfig: config("edit_file"), GlobToolConfig: config("glob_files"), GrepToolConfig: config("grep_files"),
	})
	if err != nil {
		return nil, err
	}
	// 在解析快照时取得 Eino 实际工具定义，再统一交给 Registry 的权限包装。
	_, agentCtx, err := handler.BeforeAgent(ctx, &adk.ChatModelAgentContext{})
	if err != nil {
		return nil, err
	}
	if len(agentCtx.Tools) != 1 {
		return nil, errors.New("Eino 文件工具注册数量异常")
	}
	tool, ok := agentCtx.Tools[0].(einotool.InvokableTool)
	if !ok {
		return nil, errors.New("不支持的 Eino 文件工具类型")
	}
	return tool, nil
}
