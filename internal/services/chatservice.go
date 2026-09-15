package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	"github.com/sda1-hacker/humbert-agent/internal/approval"
	"github.com/sda1-hacker/humbert-agent/internal/contextengine"
	agentruntime "github.com/sda1-hacker/humbert-agent/internal/runtime"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const RuntimeEventName = "humbert:runtime:event"

// init 注册 Runtime Event 类型。
//
// Wails binding generator 能够识别直接 RegisterEvent 调用，
// 并为事件数据生成对应类型信息。
func init() {
	application.RegisterEvent[agentruntime.Event](
		RuntimeEventName,
	)
}

// StartTurnRequest 是 Vue 发起聊天请求时的 DTO。
type StartTurnRequest struct {
	SessionID string `json:"sessionID"`

	Content            string              `json:"content"`
	Attachments        []AttachmentRequest `json:"attachments,omitempty"`
	RetryUserMessageID string              `json:"retryUserMessageID,omitempty"`
}

// AttachmentRequest 使用 Base64 穿过 Wails 边界；后端立即写入 Session sidecar。
type AttachmentRequest struct {
	Name       string `json:"name"`
	MIMEType   string `json:"mimeType"`
	Base64Data string `json:"base64Data"`
}

// ResolveApprovalRequest 是 Vue 对一次待审批 Tool 的明确决策。
//
// Decision 只允许 approval.Decision 定义的 deny/deny_agent/allow_once/allow_session/allow_agent；
// 原始 Tool Arguments 不会从前端回传，Resume 始终使用 Eino checkpoint 中保存的参数。
type ResolveApprovalRequest struct {
	ApprovalID string            `json:"approvalID"`
	Decision   approval.Decision `json:"decision"`
}

// ChatService 是 RuntimeService 的 Desktop Adapter。
//
// 调用方向：
//
//	Vue
//	  ↓
//	Wails Binding
//	  ↓
//	ChatService
//	  ↓
//	RuntimeService
//
// 流式输出方向：
//
//	RuntimeService
//	  ↓
//	Internal EventBus
//	  ↓
//	ChatService Event Bridge
//	  ↓
//	Wails Custom Event
//	  ↓
//	Vue Events.On()
type ChatService struct {
	core *coreapp.Application

	mu sync.Mutex

	unsubscribe func()
}

// NewChatService 创建 ChatService。
func NewChatService(
	core *coreapp.Application,
) *ChatService {
	return &ChatService{
		core: core,
	}
}

// ServiceName 返回 Wails Service 名称。
func (s *ChatService) ServiceName() string {
	return "ChatService"
}

// ServiceStartup 建立 Runtime EventBus → Wails Event Bridge。
func (s *ChatService) ServiceStartup(
	ctx context.Context,
	options application.ServiceOptions,
) error {
	unsubscribe, err :=
		s.core.Events().
			Subscribe(
				agentruntime.TopicEvent,
				func(
					ctx context.Context,
					payload any,
				) {
					event, ok := payload.(agentruntime.Event)
					if !ok {
						s.core.Logger().
							Warn(
								ctx,
								"收到非法 Runtime Event payload",
								"payload_type",
								fmt.Sprintf(
									"%T",
									payload,
								),
							)

						return
					}

					app :=
						application.Get()

					if app == nil {
						return
					}

					app.Event.Emit(
						RuntimeEventName,
						event,
					)
				},
			)

	if err != nil {
		return fmt.Errorf(
			"订阅 Runtime EventBus 失败: %w",
			err,
		)
	}

	s.mu.Lock()

	s.unsubscribe =
		unsubscribe

	s.mu.Unlock()

	s.core.Logger().Info(
		ctx,
		"ChatService Runtime Event Bridge 已启动",
	)

	return nil
}

// ServiceShutdown 清理 EventBus subscription。
func (s *ChatService) ServiceShutdown() error {
	s.mu.Lock()

	unsubscribe :=
		s.unsubscribe

	s.unsubscribe = nil

	s.mu.Unlock()

	if unsubscribe != nil {
		unsubscribe()
	}

	return nil
}

// StartTurn 发起一个新的异步 User Turn。
//
// 方法只等待 Runtime 完成 Snapshot/Message/Run 初始化，
// 不等待模型完整回复。
func (s *ChatService) StartTurn(
	request StartTurnRequest,
) (
	agentruntime.StartTurnResult,
	error,
) {
	ctx, cancel :=
		context.WithTimeout(
			context.Background(),
			s.contextOperationTimeout(),
		)
	defer cancel()

	result, err :=
		s.core.Runtime().
			StartTurn(
				ctx,
				agentruntime.StartTurnInput{
					SessionID:          request.SessionID,
					Input:              sessions.UserInput{Text: request.Content, Attachments: attachmentInputs(request.Attachments)},
					RetryUserMessageID: request.RetryUserMessageID,
				},
			)

	if err != nil {
		// Wails 的 error 返回会丢弃其他返回值；已持久化消息改用带错误状态的收据返回，
		// 前端仍显示失败，但保留消息 ID，重试时不再次追加相同输入。
		if result.UserMessageID != "" && result.StartError != "" {
			return result, nil
		}
		return agentruntime.StartTurnResult{},
			fmt.Errorf(
				"启动 Agent Turn 失败: %w",
				err,
			)
	}

	return result, nil
}

// ContextStatus 返回 Composer 展示所需的 Context Usage。
//
// 该方法不触发模型调用或压缩，只读取当前 Session、Model、Tool Schema 与派生 Memory。
// 返回值中的 UsedTokens 是本地保守估算，ContextWindow 来自用户维护的 Model 配置。
func (s *ChatService) ContextStatus(sessionID string) (contextengine.Usage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	usage, err := s.core.Runtime().ContextStatus(ctx, sessionID)
	if err != nil {
		return contextengine.Usage{}, fmt.Errorf("读取 Context 使用情况失败: %w", err)
	}
	return usage, nil
}

// ContextOverview 返回 Context Usage 与统一 Runtime Manifest。
//
// 前端使用该接口展示“下一 Turn 实际会加载什么”，避免分别读取 Agent、Skill、MCP、Sandbox
// 后自行拼接状态。该调用不会发起 Provider 请求，也不会连接 MCP Server。
func (s *ChatService) ContextOverview(sessionID string) (agentruntime.ContextOverview, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	overview, err := s.core.Runtime().ContextOverview(ctx, sessionID)
	if err != nil {
		return agentruntime.ContextOverview{}, fmt.Errorf("读取 Runtime Context Overview 失败: %w", err)
	}
	return overview, nil
}

// CompactContext 执行用户主动压缩。
//
// updateMemory=false 对应“压缩”，只提交 CompactionEntry；true 对应“压缩并更新”，在
// Compaction 后额外强制刷新当前 Session memory.json。运行中的 Session 会返回
// runtime.ErrSessionBusy，避免对活动 ReAct loop 的历史进行并发改写。
func (s *ChatService) CompactContext(
	sessionID string,
	updateMemory bool,
) (agentruntime.ManualCompactionResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.contextOperationTimeout())
	defer cancel()

	result, err := s.core.Runtime().ManualCompact(ctx, sessionID, updateMemory)
	if err != nil {
		return agentruntime.ManualCompactionResult{}, fmt.Errorf("手动压缩 Context 失败: %w", err)
	}
	return result, nil
}

// ResolveApproval 处理当前等待中的 Human Approval，并异步恢复原 User Turn。
//
// 该 Wails 调用只等待 Permission Rule（如果选择了 Session/Agent 授权）提交完成，不等待
// Tool 或后续模型回复。真实执行结果继续通过 humbert:runtime:event 返回，因此前端按钮不会
// 被长时间模型调用阻塞。
func (s *ChatService) ResolveApproval(
	request ResolveApprovalRequest,
) (agentruntime.ResolveApprovalResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := s.core.Runtime().ResolveApproval(ctx, agentruntime.ResolveApprovalInput{
		ApprovalID: request.ApprovalID,
		Decision:   request.Decision,
	})
	if err != nil {
		return agentruntime.ResolveApprovalResult{}, fmt.Errorf("处理 Tool Approval 失败: %w", err)
	}
	return result, nil
}

func attachmentInputs(values []AttachmentRequest) []sessions.AttachmentInput {
	result := make([]sessions.AttachmentInput, 0, len(values))
	for _, value := range values {
		result = append(result, sessions.AttachmentInput{Name: value.Name, MIMEType: value.MIMEType, Base64Data: value.Base64Data})
	}
	return result
}

func (s *ChatService) contextOperationTimeout() time.Duration {
	configured := time.Duration(s.core.Config().Runtime.Context.OperationTimeoutMS) * time.Millisecond
	if configured <= 0 {
		configured = 2 * time.Minute
	}
	// StartTurn/ManualCompact 在模型维护调用之外还有 Session/Tool Snapshot IO，增加 10 秒
	// adapter 余量，避免内部操作刚结束就被 Wails 边界提前取消。
	return configured + 10*time.Second
}

// CancelTurn 请求取消指定 Turn。
func (s *ChatService) CancelTurn(
	requestID string,
) error {
	if err :=
		s.core.Runtime().
			CancelTurn(
				requestID,
			); err != nil {

		return fmt.Errorf(
			"取消 Agent Turn 失败: %w",
			err,
		)
	}

	return nil
}
