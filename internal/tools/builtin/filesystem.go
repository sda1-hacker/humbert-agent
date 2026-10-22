package builtin

import (
	"context"
	"errors"
	"fmt"

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
		cfg := &filesystemmw.ToolConfig{Name: name, Disable: name != f.name}
		if name == "grep_files" {
			description := fmt.Sprintf("搜索授权目录中的文件内容。pattern 使用 Go regexp（RE2）语法，不支持反向引用或 lookaround；支持大小写和多行选项、glob 与文件类型过滤、前后文。文本按 UTF-8 搜索；受支持文档先提取文本，因此行号对应提取后的文本。跳过符号链接、二进制、不可读、超大或无法解析的文件，结果不代表所有文件均被搜索。最多扫描 %d 个目录项、返回 %d 行，每行最多 %d 字符；超限请缩小 path 或 pattern。", f.limits.MaxSearchEntries, f.limits.MaxSearchResults, f.limits.MaxSearchLineChars)
			cfg.Desc = &description
		}
		if name == "glob_files" {
			description := fmt.Sprintf("在授权范围内按 glob 模式查找路径。支持 ** 递归匹配；跳过符号链接和 .git。最多扫描 %d 个目录项、返回 %d 项；超限请缩小 path 或 pattern。", f.limits.MaxSearchEntries, f.limits.MaxSearchResults)
			cfg.Desc = &description
		}
		return cfg
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
