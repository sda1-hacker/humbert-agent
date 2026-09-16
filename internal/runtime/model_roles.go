package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/models"
)

const (
	modelRoleChat    = "chat"
	modelRoleUtility = "utility"
	modelRoleMemory  = "memory"
	modelRoleVision  = "vision"
)

type turnInputRequirements struct {
	Vision bool
	Files  bool
}

type resolvedModelRoles struct {
	chat          models.RuntimeSnapshot
	utility       models.RuntimeSnapshot
	memory        models.RuntimeSnapshot
	visionModelID string
	vision        *models.RuntimeSnapshot

	active     models.RuntimeSnapshot
	activeRole string
}

// ModelCapabilityError 是 Runtime 在 Provider 调用前返回的稳定能力错误。
// UI 可以直接展示 Error()，同时 errors.Is(..., ErrModelCapabilityUnsupported) 仍然成立。
type ModelCapabilityError struct {
	ModelID      string
	DisplayName  string
	Role         string
	Capabilities []string
}

func (e *ModelCapabilityError) Error() string {
	name := strings.TrimSpace(e.DisplayName)
	if name == "" {
		name = e.ModelID
	}
	return fmt.Sprintf(
		"%v：模型「%s」(%s) 缺少能力 %s，请在模型设置中启用对应 Capability，或为 Agent 配置合适的模型角色",
		ErrModelCapabilityUnsupported,
		name,
		e.Role,
		strings.Join(e.Capabilities, "/"),
	)
}

func (e *ModelCapabilityError) Unwrap() error { return ErrModelCapabilityUnsupported }

func (r *Resolver) resolveModelRoles(
	ctx context.Context,
	agent agents.Agent,
	requirements turnInputRequirements,
) (resolvedModelRoles, error) {
	chatID := strings.TrimSpace(agent.ModelID)
	if chatID == "" {
		return resolvedModelRoles{}, ErrAgentModelMissing
	}

	chat, err := r.models.ResolveSnapshot(ctx, chatID)
	if err != nil {
		return resolvedModelRoles{}, fmt.Errorf("解析 Chat Model 失败: %w", err)
	}

	utility := chat
	if id := strings.TrimSpace(agent.ModelRoles.UtilityModelID); id != "" && id != chat.ModelConfigID {
		utility, err = r.models.ResolveSnapshot(ctx, id)
		if err != nil {
			return resolvedModelRoles{}, fmt.Errorf("解析 Utility Model 失败: %w", err)
		}
	}

	memoryModel := utility
	if id := strings.TrimSpace(agent.ModelRoles.MemoryModelID); id != "" && id != utility.ModelConfigID {
		if id == chat.ModelConfigID {
			memoryModel = chat
		} else {
			memoryModel, err = r.models.ResolveSnapshot(ctx, id)
			if err != nil {
				return resolvedModelRoles{}, fmt.Errorf("解析 Memory Model 失败: %w", err)
			}
		}
	}

	visionModelID := strings.TrimSpace(agent.ModelRoles.VisionModelID)
	var vision *models.RuntimeSnapshot

	active := chat
	activeRole := modelRoleChat
	if missing := missingInputCapabilities(chat.Capabilities, requirements); len(missing) > 0 {
		if visionModelID == "" {
			return resolvedModelRoles{}, capabilityError(chat, modelRoleChat, missing)
		}

		var snapshot models.RuntimeSnapshot
		switch visionModelID {
		case chat.ModelConfigID:
			snapshot = chat
		case utility.ModelConfigID:
			snapshot = utility
		case memoryModel.ModelConfigID:
			snapshot = memoryModel
		default:
			snapshot, err = r.models.ResolveSnapshot(ctx, visionModelID)
			if err != nil {
				return resolvedModelRoles{}, fmt.Errorf("解析 Vision Model 失败: %w", err)
			}
		}
		vision = &snapshot

		if visionMissing := missingInputCapabilities(snapshot.Capabilities, requirements); len(visionMissing) > 0 {
			return resolvedModelRoles{}, capabilityError(snapshot, modelRoleVision, visionMissing)
		}
		active = snapshot
		activeRole = modelRoleVision
	}

	return resolvedModelRoles{
		chat: chat, utility: utility, memory: memoryModel, visionModelID: visionModelID, vision: vision,
		active: active, activeRole: activeRole,
	}, nil
}

func (r *Resolver) currentTurnRequirements(ctx context.Context, sessionID string) (turnInputRequirements, error) {
	messages, err := r.sessions.Messages(ctx, sessionID, 1)
	if err != nil {
		return turnInputRequirements{}, fmt.Errorf("读取当前 User Input 失败: %w", err)
	}
	if len(messages) == 0 || messages[0].Message == nil || messages[0].Message.Role != schema.User {
		return turnInputRequirements{}, errors.New("当前 Turn 缺少已经持久化的 User Message")
	}
	return requirementsFromMessage(messages[0].Message), nil
}

func requirementsFromMessage(message *schema.Message) turnInputRequirements {
	var result turnInputRequirements
	if message == nil {
		return result
	}
	for _, part := range message.UserInputMultiContent {
		switch part.Type {
		case schema.ChatMessagePartTypeImageURL:
			result.Vision = true
		case schema.ChatMessagePartTypeFileURL:
			result.Files = true
		}
	}
	return result
}

// requirementsFromMessages 检查本次真正会发送给 Provider 的 Context。
// 历史多模态 User Message 仍在 Context 中时，后续纯文本 Turn 也必须继续使用
// 能处理这些内容的模型；只检查当前 User Message 会在第二轮重新切回 text-only 模型。
func requirementsFromMessages(messages []*schema.Message) turnInputRequirements {
	var result turnInputRequirements
	for _, message := range messages {
		current := requirementsFromMessage(message)
		result.Vision = result.Vision || current.Vision
		result.Files = result.Files || current.Files
	}
	return result
}

func missingInputCapabilities(capabilities models.Capabilities, requirements turnInputRequirements) []string {
	missing := make([]string, 0, 2)
	if requirements.Vision && !capabilities.Vision {
		missing = append(missing, "Vision")
	}
	if requirements.Files && !capabilities.Files {
		missing = append(missing, "Files")
	}
	return missing
}

func capabilityError(snapshot models.RuntimeSnapshot, role string, missing []string) error {
	return &ModelCapabilityError{
		ModelID: snapshot.ModelConfigID, DisplayName: snapshot.ModelDisplayName,
		Role: role, Capabilities: append([]string(nil), missing...),
	}
}

func validateToolCapability(snapshot models.RuntimeSnapshot, role string, exposedToolNames []string) error {
	if len(exposedToolNames) == 0 || snapshot.Capabilities.Tools {
		return nil
	}
	return capabilityError(snapshot, role, []string{"Tools"})
}

// compactionModel 优先使用 Utility Role；若它的上下文窗口小于当前执行模型，回退到
// active model，避免为节省成本而把一个本来可处理的长上下文送进更小窗口模型。
func compactionModel(roles resolvedModelRoles) models.RuntimeSnapshot {
	if roles.utility.ContextWindow >= roles.active.ContextWindow {
		return roles.utility
	}
	return roles.active
}
