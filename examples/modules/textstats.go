// Package modules 演示一个独立能力模块的最小接入方式，不默认加入桌面应用。
package modules

import (
	"context"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/sda1-hacker/humbert-agent/internal/component"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

const textStatsTool = "textstats_count"

// InstallTextStats 的调用方式为 app.Bootstrap(ctx, app.WithModules(InstallTextStats(agentID)))。
// Agent 绑定来自模块自己的配置；实际模块可以在自己的 Store 中保存，而不修改 Agent Profile。
// 此示例只做纯文本统计，因此没有外部 SDK 连接、后台 Worker 或 Close 回调。
func InstallTextStats(boundAgentID string) component.Installer {
	return func(context.Context, component.Host) (component.Module, error) {
		provider := &textStatsProvider{boundAgentID: boundAgentID}
		return component.Module{ID: "textstats", Capabilities: []component.Provider{provider}}, nil
	}
}

type textStatsProvider struct{ boundAgentID string }
type textStatsInput struct {
	Text string `json:"text" jsonschema:"description=Text to count Unicode characters in"`
}
type textStatsOutput struct {
	Characters int `json:"characters"`
}

func (*textStatsProvider) ID() string { return "textstats" }
func (p *textStatsProvider) Selection(_ context.Context, agentID string) ([]string, error) {
	if p.boundAgentID == "" || agentID != p.boundAgentID {
		return nil, nil
	}
	return []string{textStatsTool}, nil
}

// Describe 只构造本地 Schema。InferTool 自动从 Go 类型生成 Schema 和 JSON 编解码，
// 无需另外实现 Eino 工具包装；构造不会执行统计函数，也不会连接外部服务。
func (p *textStatsProvider) Describe(_ context.Context, _ component.Request) (component.Contribution, error) {
	return p.contribution()
}

// Resolve 每轮返回新的工具对象；统一权限 Guard、Token 预算和执行流程由宿主处理。
func (p *textStatsProvider) Resolve(_ context.Context, _ component.Request) (component.Contribution, error) {
	return p.contribution()
}
func (*textStatsProvider) contribution() (component.Contribution, error) {
	tool, err := utils.InferTool(textStatsTool, "Count Unicode characters in the supplied text.", func(_ context.Context, input *textStatsInput) (*textStatsOutput, error) {
		return &textStatsOutput{Characters: utf8.RuneCountInString(input.Text)}, nil
	})
	if err != nil {
		return component.Contribution{}, err
	}
	// 无动态配置时使用固定实现版本；修改实现或 Schema 时一并更新该版本。
	return component.Contribution{Revision: "textstats-v1", Tools: []component.Tool{{
		Descriptor: humberttools.Descriptor{Name: textStatsTool, Risk: humberttools.RiskRead}, Tool: tool,
	}}}, nil
}
