package notifications

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sda1-hacker/humbert-agent/internal/eventbus"
)

const TopicEvent = "notifications.event"

type Level string

const (
	LevelInfo    Level = "info"
	LevelSuccess Level = "success"
	LevelWarning Level = "warning"
	LevelError   Level = "error"
)

// Notification 是主动助手向宿主桌面发送的统一通知协议。
// Core 不直接依赖某个操作系统通知 SDK；Desktop Adapter 可以把它投影成
// Wails 事件、系统通知或应用内 Toast。
type Notification struct {
	ID string `json:"id"`

	Level Level  `json:"level"`
	Title string `json:"title"`
	Body  string `json:"body"`

	AgentID   string `json:"agentID,omitempty"`
	SessionID string `json:"sessionID,omitempty"`
	TaskID    string `json:"taskID,omitempty"`
	RunID     string `json:"runID,omitempty"`

	CreatedAt string `json:"createdAt"`
}

// Provider 允许以后接入原生 macOS/Windows/Linux 通知而不改变主动助手领域逻辑。
// Provider 必须快速返回；需要阻塞 I/O 的实现应自行遵守 ctx。
type Provider interface {
	Send(ctx context.Context, notification Notification) error
}

type Service struct {
	mu        sync.RWMutex
	providers []Provider
	recent    []Notification
}

func New(providers ...Provider) *Service {
	result := &Service{}
	for _, provider := range providers {
		if provider != nil {
			result.providers = append(result.providers, provider)
		}
	}
	return result
}

func (s *Service) AddProvider(provider Provider) {
	if provider == nil {
		return
	}
	s.mu.Lock()
	s.providers = append(s.providers, provider)
	s.mu.Unlock()
}

func (s *Service) Send(ctx context.Context, value Notification) error {
	if ctx == nil {
		return errors.New("context.Context 不能为空")
	}
	value.Title = strings.TrimSpace(value.Title)
	value.Body = strings.TrimSpace(value.Body)
	if value.Title == "" {
		return errors.New("通知标题不能为空")
	}
	if value.ID == "" {
		value.ID = uuid.NewString()
	}
	if value.Level == "" {
		value.Level = LevelInfo
	}
	if value.CreatedAt == "" {
		value.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	s.mu.Lock()
	s.recent = append(s.recent, value)
	if len(s.recent) > 100 {
		s.recent = append([]Notification(nil), s.recent[len(s.recent)-100:]...)
	}
	providers := append([]Provider(nil), s.providers...)
	s.mu.Unlock()

	var errs []error
	for _, provider := range providers {
		if err := provider.Send(ctx, value); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Recent 返回当前进程最近的通知，用于 Desktop Adapter 启动较晚时补齐启动阶段
// 已经产生的提醒。通知 ID 由前端去重，不会因此重复展示实时事件。
func (s *Service) Recent(limit int) []Notification {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.recent) {
		limit = len(s.recent)
	}
	start := len(s.recent) - limit
	return append([]Notification(nil), s.recent[start:]...)
}

// EventProvider 把通知发布到 Humbert 进程内事件总线。Wails Service 负责把它
// 转成桌面事件；前端可以进一步选择系统 Notification API 或应用内 Toast。
type EventProvider struct {
	events *eventbus.Bus
}

func NewEventProvider(events *eventbus.Bus) *EventProvider {
	return &EventProvider{events: events}
}

func (p *EventProvider) Send(ctx context.Context, value Notification) error {
	if p == nil || p.events == nil {
		return errors.New("Notification EventProvider 未初始化")
	}
	return p.events.Publish(ctx, TopicEvent, value)
}
