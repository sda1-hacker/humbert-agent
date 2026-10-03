// Package component 定义模块唯一的接入契约：构造、Eino 工具贡献与资源所有权。
package component

import (
	"context"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/sda1-hacker/humbert-agent/internal/credential"
	"github.com/sda1-hacker/humbert-agent/internal/eventbus"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/sandbox"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

// Host 只提供公共平台资源。业务依赖由组件自己的构造函数显式接收，不能反查 Application。
// 模块在 DataDir 下拥有自己的目录；凭证复用公共存储，秘密不进入业务配置。
type Host struct {
	DataDir     string
	Logger      *logging.Logger
	Credentials *credential.Store
	Events      *eventbus.Bus
	Sandbox     *sandbox.Manager
}

// Module 是构造完成后交给宿主的静态装配结果。工具直接使用 Eino 契约，UI 服务另行装配。
// 生命周期顺序：Stop 停止生产新工作，宿主等待已有 Turn 后再 Close 释放连接。
// Start 创建部分资源后即使失败也会执行 Stop；没有后台工作时可省略 Start/Stop。
type Module struct {
	ID           string
	Capabilities []Provider
	Start        func(context.Context) error
	Stop         func(context.Context) error
	Close        func(context.Context) error
}

// Installer 在构造期间注入依赖。返回错误时清理尚未转交宿主的资源，成功后由宿主管理。
type Installer func(context.Context, Host) (Module, error)

// Provider 是功能模块向 Runtime 贡献工具的接入面。
// Selection 必须读取模块自己保存的显式 Agent 绑定；空选择表示不开放任何工具。
// Describe 只提供本地 Schema，不连接服务或调用模型；Resolve 返回本轮冻结的 Eino 工具。
// 模块继续拥有业务、配置和连接，宿主统一处理选择校验、权限、预算和结果归档。
type Provider interface {
	ID() string
	Selection(ctx context.Context, agentID string) ([]string, error)
	Describe(ctx context.Context, request Request) (Contribution, error)
	Resolve(ctx context.Context, request Request) (Contribution, error)
}

// Request 区分能力所属 Agent 与授权 Scope：子 Agent 读取自己的能力绑定，
// 但 Scope.AgentID/Workspace/Sandbox 仍继承父运行，模块不能扩大授权范围。
// ToolNames 已由宿主冻结，模块应返回这些名称的工具，不得改写传入 Scope。
type Request struct {
	AgentID   string
	Scope     humberttools.Scope
	ToolNames []string
}

// Tool 保留 Eino 原生工具契约。Describe 可返回仅实现 Info 的 Schema 投影；
// Resolve 必须返回 InvokableTool，宿主在执行前统一套上已有 Permission Guard。
type Tool struct {
	Descriptor humberttools.Descriptor
	Tool       einotool.BaseTool
}

// Contribution.Revision 应是实现、配置及 Schema 的内容版本/摘要。
// 影响工具行为的内容变化必须更改它；不得写入 Secret 正文。Resolve 的对象必须捕获
// 本轮配置，而不是在 InvokableRun 时再读取会改变行为的全局设置。
type Contribution struct {
	Revision string
	Tools    []Tool
}
