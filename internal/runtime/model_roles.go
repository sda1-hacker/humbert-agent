package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	"github.com/sda1-hacker/humbert-agent/internal/models"
	"github.com/sda1-hacker/humbert-agent/internal/multimodal"
)

const (
	modelRoleChat    = "chat"
	modelRoleUtility = "utility"
	modelRoleMemory  = "memory"
	modelRoleImage   = "image"
)

type turnInputRequirements struct {
	Vision bool
	Files  bool
}

type resolvedModelRoles struct {
	chat         models.RuntimeSnapshot
	utility      models.RuntimeSnapshot
	memory       models.RuntimeSnapshot
	imageModelID string
	image        *models.RuntimeSnapshot
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
	hint := "请在模型设置中启用对应 Capability，或在“设置 → 多媒体”中配置视觉辅助模型"
	for _, capability := range e.Capabilities {
		if capability == "Files" {
			hint = "当前消息要求 Provider 原生 Files 能力；Humbert 目前只正式支持图片和可提取的 UTF-8 文本附件"
			break
		}
	}
	return fmt.Sprintf(
		"%v：模型「%s」(%s) 缺少能力 %s，%s",
		ErrModelCapabilityUnsupported,
		name,
		e.Role,
		strings.Join(e.Capabilities, "/"),
		hint,
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

	// Files 是主聊天模型自身的输入能力。图片辅助模型只能补足 Vision，不能代替
	// Chat Model 接管原生文件输入，因此异常的原生 file_url 仍然 fail closed。
	if requirements.Files && !chat.Capabilities.Files {
		return resolvedModelRoles{}, capabilityError(chat, modelRoleChat, []string{"Files"})
	}

	multimedia, err := r.models.MultimediaConfig(ctx)
	if err != nil {
		return resolvedModelRoles{}, fmt.Errorf("读取多媒体模型配置失败: %w", err)
	}
	imageModelID := strings.TrimSpace(multimedia.ImageModelID)
	var image *models.RuntimeSnapshot

	// Vision 与 Turn 执行模型解耦：Chat Model 永远负责当前 Agent Turn。只有当真正
	// 会发送给 Provider 的 Context 含图片且 Chat Model 不支持视觉时，才解析全局
	// 视觉辅助模型只作为观察模型。它不需要 Tools Capability。
	if requirements.Vision && !chat.Capabilities.Vision {
		if imageModelID == "" {
			return resolvedModelRoles{}, capabilityError(chat, modelRoleChat, []string{"Vision"})
		}

		var snapshot models.RuntimeSnapshot
		switch imageModelID {
		case chat.ModelConfigID:
			snapshot = chat
		case utility.ModelConfigID:
			snapshot = utility
		case memoryModel.ModelConfigID:
			snapshot = memoryModel
		default:
			snapshot, err = r.models.ResolveSnapshot(ctx, imageModelID)
			if err != nil {
				return resolvedModelRoles{}, fmt.Errorf("解析视觉辅助模型失败: %w", err)
			}
		}
		if !snapshot.Capabilities.Vision {
			return resolvedModelRoles{}, capabilityError(snapshot, modelRoleImage, []string{"Vision"})
		}
		image = &snapshot
	}

	return resolvedModelRoles{
		chat: chat, utility: utility, memory: memoryModel, imageModelID: imageModelID, image: image,
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
	return requirementsFromMessageWithImages(message, true)
}

func requirementsFromMessageWithImages(message *schema.Message, includeImages bool) turnInputRequirements {
	var result turnInputRequirements
	if message == nil {
		return result
	}
	for _, part := range message.UserInputMultiContent {
		switch part.Type {
		case schema.ChatMessagePartTypeImageURL:
			result.Vision = result.Vision || includeImages
		case schema.ChatMessagePartTypeFileURL:
			// 当前文件附件在接收时已提取为受控 UTF-8 文本，Provider 请求阶段会转换成
			// text part，不依赖 Eino Adapter 尚未实现的原生 file_url。只有缺少提取结果
			// 的异常消息才需要原生 Files Capability，并由后续水合 fail closed。
			if stringMessagePartExtra(part.Extra, "extracted_text") == "" {
				result.Files = true
			}
		}
	}
	return result
}

func stringMessagePartExtra(extra map[string]any, key string) string {
	if extra == nil {
		return ""
	}
	value, _ := extra[key].(string)
	return strings.TrimSpace(value)
}

// requirementsFromMessages 检查本次真正会发送给 Provider 的 Context。它与
// sessions.HydrateMessages 使用同一个历史图片重放窗口：图片紧邻追问仍使用 Vision
// Model，更早图片已经变为文本占位，不应继续要求视觉辅助。
func requirementsFromMessages(messages []*schema.Message) turnInputRequirements {
	var result turnInputRequirements
	imageReplayMask := multimodal.ImageReplayMask(messages)
	for index, message := range messages {
		current := requirementsFromMessageWithImages(message, imageReplayMask[index])
		result.Vision = result.Vision || current.Vision
		result.Files = result.Files || current.Files
	}
	return result
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

// compactionModel 优先使用 Utility Role；若它的上下文窗口小于主聊天模型，回退到
// Chat Model，避免为节省成本而把一个本来可处理的长上下文送进更小窗口模型。
func compactionModel(roles resolvedModelRoles) models.RuntimeSnapshot {
	if roles.utility.ContextWindow >= roles.chat.ContextWindow {
		return roles.utility
	}
	return roles.chat
}
