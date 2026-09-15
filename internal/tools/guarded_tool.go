package tools

import (
	"context"
	"errors"
	"fmt"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/approval"
	"github.com/sda1-hacker/humbert-agent/internal/permission"
)

// GuardInvokableTool 给任意 Eino InvokableTool 套上 Humbert 统一 Permission Boundary。
//
// Builtin Registry 与 MCP Adapter 必须调用同一个入口，避免不同 Tool Source 形成两套授权语义。
// 该函数同时验证 Descriptor / Scope / Eino ToolInfo Name 一致性，防止 Permission 判断的名称
// 与模型真正调用的名称不同。
func GuardInvokableTool(
	ctx context.Context,
	authorizer Authorizer,
	descriptor Descriptor,
	scope Scope,
	instance einotool.InvokableTool,
) (einotool.BaseTool, error) {
	if ctx == nil {
		return nil, errors.New("保护 Tool 失败: context.Context 不能为空")
	}
	if authorizer == nil {
		return nil, errors.New("保护 Tool 失败: Authorizer 不能为空")
	}
	if instance == nil {
		return nil, fmt.Errorf("%w: Tool Instance 不能为空", ErrInvalidTool)
	}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	if err := scope.Validate(); err != nil {
		return nil, fmt.Errorf("Tool Scope 无效: %w", err)
	}

	info, err := instance.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取 Tool %q Info 失败: %w", descriptor.Name, err)
	}
	if info == nil {
		return nil, fmt.Errorf("%w: Tool %q Info 不能为空", ErrInvalidTool, descriptor.Name)
	}
	if info.Name != descriptor.Name {
		return nil, fmt.Errorf(
			"%w: Descriptor Name=%q 与 Eino Tool Name=%q 不一致",
			ErrInvalidTool,
			descriptor.Name,
			info.Name,
		)
	}

	return &guardedInvokableTool{
		tool:       instance,
		descriptor: descriptor,
		scope:      scope,
		authorizer: authorizer,
	}, nil
}

// guardedInvokableTool 是所有 Capability 的统一 Permission Boundary。
//
// 调用链固定为：
//
//	Eino
//	  ↓
//	guardedInvokableTool
//	  ↓
//	permission.Engine
//	  ├─ Allow → real Tool
//	  ├─ Deny  → 正常 ToolResult 告知模型“策略拒绝”，不执行副作用
//	  └─ Ask   → Eino StatefulInterrupt → Runtime Approval → Resume
//
// Ask 使用 StatefulInterrupt 保存第一次调用的原始 Arguments。用户批准后执行 checkpoint 中
// 的参数，而不是任何恢复阶段重新传入的值，从而保证“批准的内容”和“真正执行的内容”一致。
type guardedInvokableTool struct {
	tool einotool.InvokableTool

	descriptor Descriptor
	scope      Scope
	authorizer Authorizer
}

// Info 将底层 Tool Metadata 原样提供给 Eino ChatModel。
func (t *guardedInvokableTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.tool.Info(ctx)
}

// InvokableRun 执行 Permission 判断，并处理 Eino Interrupt/Resume 生命周期。
func (t *guardedInvokableTool) InvokableRun(
	ctx context.Context,
	argumentsInJSON string,
	options ...einotool.Option,
) (string, error) {
	if ctx == nil {
		return "", errors.New("执行 Tool 失败: context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("执行 Tool %q 被取消: %w", t.descriptor.Name, err)
	}

	wasInterrupted, hasState, rawState := einotool.GetInterruptState[string](ctx)
	if wasInterrupted {
		if !hasState {
			return "", fmt.Errorf("恢复 Tool %q Approval 失败: checkpoint 缺少内部状态", t.descriptor.Name)
		}
		return t.resumeInvocation(ctx, rawState, options...)
	}

	identity, err := buildCapabilityIdentity(t.descriptor, t.scope, argumentsInJSON)
	if err != nil {
		return "", fmt.Errorf("Tool %q Capability Identity 构建失败: %w", t.descriptor.Name, err)
	}
	request := permission.Request{
		RequestID: t.scope.RequestID,
		RunID:     t.scope.RunID,
		SessionID: t.scope.SessionID,
		AgentID:   t.scope.AgentID,
		ToolName:  t.descriptor.Name,
		Risk:      t.descriptor.Risk,
		Arguments: argumentsInJSON,
		Identity:  identity,
	}
	if origin := t.descriptor.MCPOrigin; origin != nil {
		request.MCPServerName = origin.ServerName
		request.MCPRawToolName = origin.RawToolName
	}
	decision, err := t.authorizer.Evaluate(ctx, request)
	if err != nil {
		return "", fmt.Errorf("Tool %q Permission 校验失败: %w", t.descriptor.Name, err)
	}

	switch decision.Action {
	case permission.ActionAllow:
		return t.invokeRealTool(ctx, argumentsInJSON, options...)

	case permission.ActionDeny:
		// Policy Deny 是预期的 Capability 结果而不是 Runtime 故障。返回普通 ToolResult 可让
		// Agent 理解该能力不可用并选择替代方案，同时保证真实 Tool 从未执行。
		return permissionDeniedResult(t.descriptor.Name), nil

	case permission.ActionAsk:
		info := approval.InterruptInfo{
			ApprovalID:   decision.ApprovalID,
			RequestID:    request.RequestID,
			RunID:        request.RunID,
			SessionID:    request.SessionID,
			AgentID:      request.AgentID,
			ToolName:     request.ToolName,
			Risk:         request.Risk,
			Identity:     decision.Identity,
			Presentation: decision.Presentation,
		}
		infoJSON, stateJSON, err := approval.EncodeInterrupt(info, argumentsInJSON)
		if err != nil {
			return "", fmt.Errorf("准备 Tool %q Approval Interrupt 失败: %w", t.descriptor.Name, err)
		}

		// StatefulInterrupt 返回的 error 必须原样向 Eino 传播。不要包装成普通 Tool 错误，
		// 否则编排层可能无法识别 InterruptSignal 并保存 checkpoint。
		return "", einotool.StatefulInterrupt(ctx, infoJSON, stateJSON)

	default:
		return "", fmt.Errorf("Tool %q Permission 返回未知 Action %q", t.descriptor.Name, decision.Action)
	}
}

func (t *guardedInvokableTool) resumeInvocation(
	ctx context.Context,
	rawState string,
	options ...einotool.Option,
) (string, error) {
	state, err := approval.DecodeInterruptState(rawState)
	if err != nil {
		return "", fmt.Errorf("恢复 Tool %q Approval 状态失败: %w", t.descriptor.Name, err)
	}

	isTarget, hasData, rawResume := einotool.GetResumeContext[string](ctx)
	if !isTarget {
		// 多 Tool/未来 Parallel 场景中，恢复另一个 root-cause 时当前 Tool 必须重新中断，
		// 否则会丢失自己的等待状态。
		return "", einotool.StatefulInterrupt(ctx, state.InfoJSON, rawState)
	}
	if !hasData {
		return "", fmt.Errorf("恢复 Tool %q Approval 失败: ResumeData 为空", t.descriptor.Name)
	}

	resume, err := approval.DecodeResumeData(rawResume)
	if err != nil {
		return "", fmt.Errorf("恢复 Tool %q Approval 结果失败: %w", t.descriptor.Name, err)
	}
	if !resume.Approved {
		return approvalRejectedResult(t.descriptor.Name), nil
	}

	info, err := approval.DecodeInterruptInfo(state.InfoJSON)
	if err != nil {
		return "", fmt.Errorf("恢复 Tool %q Approval 身份失败: %w", t.descriptor.Name, err)
	}
	currentIdentity, err := buildCapabilityIdentity(t.descriptor, t.scope, state.Arguments)
	if err != nil {
		return "", fmt.Errorf("恢复 Tool %q Capability Identity 失败: %w", t.descriptor.Name, err)
	}
	if !info.Identity.EqualExact(currentIdentity) {
		return "", fmt.Errorf(
			"Tool %q 安全身份在审批后发生变化，已拒绝执行；请重新发起调用并再次确认",
			t.descriptor.Name,
		)
	}

	return t.invokeRealTool(ctx, state.Arguments, options...)
}

func (t *guardedInvokableTool) invokeRealTool(
	ctx context.Context,
	argumentsInJSON string,
	options ...einotool.Option,
) (string, error) {
	result, err := t.tool.InvokableRun(ctx, argumentsInJSON, options...)
	if err != nil {
		return "", fmt.Errorf("Tool %q 执行失败: %w", t.descriptor.Name, err)
	}
	return result, nil
}

func permissionDeniedResult(toolName string) string {
	return fmt.Sprintf("工具 %q 已被 Humbert Permission Policy 拒绝，未执行任何操作。请不要假设该操作已经完成。", toolName)
}

func approvalRejectedResult(toolName string) string {
	return fmt.Sprintf("用户拒绝了工具 %q 的本次调用，未执行任何操作。请尊重该决定，并在可能时提供不需要该权限的替代方案。", toolName)
}
