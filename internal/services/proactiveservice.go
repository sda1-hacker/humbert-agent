package services

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	"github.com/sda1-hacker/humbert-agent/internal/notifications"
	"github.com/sda1-hacker/humbert-agent/internal/proactive"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	ProactiveEventName    = "humbert:proactive:event"
	NotificationEventName = "humbert:notification"
)

func init() {
	application.RegisterEvent[proactive.PublicEvent](ProactiveEventName)
	application.RegisterEvent[notifications.Notification](NotificationEventName)
}

type ProactiveQuietHoursDTO struct {
	Enabled  bool   `json:"enabled"`
	Start    string `json:"start"`
	End      string `json:"end"`
	TimeZone string `json:"timeZone"`
}

type ProactiveRuleDTO struct {
	Enabled         bool   `json:"enabled"`
	Action          string `json:"action"`
	AgentID         string `json:"agentID,omitempty"`
	AgentPrompt     string `json:"agentPrompt,omitempty"`
	CooldownSeconds int    `json:"cooldownSeconds,omitempty"`
}

type ProactiveSettingsDTO struct {
	Enabled                  bool                        `json:"enabled"`
	HeartbeatIntervalMinutes int                         `json:"heartbeatIntervalMinutes"`
	QuietHours               ProactiveQuietHoursDTO      `json:"quietHours"`
	Rules                    map[string]ProactiveRuleDTO `json:"rules"`
}

type ProactiveStatusDTO struct {
	Running         bool   `json:"running"`
	LastHeartbeatAt string `json:"lastHeartbeatAt,omitempty"`
	NextHeartbeatAt string `json:"nextHeartbeatAt,omitempty"`
	QueuedEvents    int    `json:"queuedEvents"`
}

type ProactiveService struct {
	core *coreapp.Application

	mu            sync.Mutex
	unsubscribers []func()
}

func NewProactiveService(core *coreapp.Application) *ProactiveService {
	return &ProactiveService{core: core}
}

func (s *ProactiveService) ServiceName() string { return "ProactiveService" }

func (s *ProactiveService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	unsubProactive, err := s.core.Events().Subscribe(proactive.TopicEvent, func(_ context.Context, payload any) {
		value, ok := payload.(proactive.PublicEvent)
		if !ok {
			return
		}
		if app := application.Get(); app != nil {
			app.Event.Emit(ProactiveEventName, value)
		}
	})
	if err != nil {
		return err
	}
	unsubNotifications, err := s.core.Events().Subscribe(notifications.TopicEvent, func(_ context.Context, payload any) {
		value, ok := payload.(notifications.Notification)
		if !ok {
			return
		}
		if app := application.Get(); app != nil {
			app.Event.Emit(NotificationEventName, value)
		}
	})
	if err != nil {
		unsubProactive()
		return err
	}
	s.mu.Lock()
	s.unsubscribers = []func(){unsubProactive, unsubNotifications}
	s.mu.Unlock()
	_ = ctx
	_ = options
	return nil
}

func (s *ProactiveService) ServiceShutdown() error {
	s.mu.Lock()
	values := append([]func(){}, s.unsubscribers...)
	s.unsubscribers = nil
	s.mu.Unlock()
	for _, unsubscribe := range values {
		if unsubscribe != nil {
			unsubscribe()
		}
	}
	return nil
}

func (s *ProactiveService) GetSettings() (ProactiveSettingsDTO, error) {
	if s.core.Proactive() == nil {
		return ProactiveSettingsDTO{}, fmt.Errorf("主动助手未初始化")
	}
	return proactiveSettingsDTO(s.core.Proactive().Settings()), nil
}

func (s *ProactiveService) UpdateSettings(value ProactiveSettingsDTO) (ProactiveSettingsDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	settings := proactiveSettingsFromDTO(value)
	for kind, rule := range settings.Rules {
		if rule.Action != proactive.ActionRunAgent || strings.TrimSpace(rule.AgentID) == "" {
			continue
		}
		if _, err := s.core.Agents().Get(ctx, rule.AgentID); err != nil {
			return ProactiveSettingsDTO{}, fmt.Errorf("事件 %s 配置的处理 Agent 不存在或不可用: %w", kind, err)
		}
	}
	updated, err := s.core.Proactive().UpdateSettings(ctx, settings)
	if err != nil {
		return ProactiveSettingsDTO{}, fmt.Errorf("保存主动助手设置失败: %w", err)
	}
	return proactiveSettingsDTO(updated), nil
}

func (s *ProactiveService) Status() (ProactiveStatusDTO, error) {
	if s.core.Proactive() == nil {
		return ProactiveStatusDTO{}, fmt.Errorf("主动助手未初始化")
	}
	return proactiveStatusDTO(s.core.Proactive().Status()), nil
}

func (s *ProactiveService) Notifications(limit int) ([]notifications.Notification, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	return s.core.Notifications().Recent(limit), nil
}

func (s *ProactiveService) Records(limit int) ([]proactive.Record, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	return s.core.Proactive().Records(limit), nil
}

func (s *ProactiveService) RunHeartbeat() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.core.Proactive().RunHeartbeat(ctx); err != nil {
		return fmt.Errorf("立即巡检失败: %w", err)
	}
	return nil
}

func proactiveSettingsDTO(value proactive.Settings) ProactiveSettingsDTO {
	rules := make(map[string]ProactiveRuleDTO, len(value.Rules))
	for kind, rule := range value.Rules {
		rules[string(kind)] = ProactiveRuleDTO{
			Enabled: rule.Enabled, Action: string(rule.Action), AgentID: rule.AgentID,
			AgentPrompt: rule.AgentPrompt, CooldownSeconds: rule.CooldownSeconds,
		}
	}
	return ProactiveSettingsDTO{
		Enabled:                  value.Enabled,
		HeartbeatIntervalMinutes: value.HeartbeatIntervalMinutes,
		QuietHours: ProactiveQuietHoursDTO{
			Enabled: value.QuietHours.Enabled, Start: value.QuietHours.Start,
			End: value.QuietHours.End, TimeZone: value.QuietHours.TimeZone,
		},
		Rules: rules,
	}
}

func proactiveSettingsFromDTO(value ProactiveSettingsDTO) proactive.Settings {
	rules := make(map[proactive.EventKind]proactive.EventRule, len(value.Rules))
	for kind, rule := range value.Rules {
		rules[proactive.EventKind(kind)] = proactive.EventRule{
			Enabled: rule.Enabled, Action: proactive.Action(rule.Action), AgentID: rule.AgentID,
			AgentPrompt: rule.AgentPrompt, CooldownSeconds: rule.CooldownSeconds,
		}
	}
	return proactive.Settings{
		Enabled:                  value.Enabled,
		HeartbeatIntervalMinutes: value.HeartbeatIntervalMinutes,
		QuietHours: proactive.QuietHours{
			Enabled: value.QuietHours.Enabled, Start: value.QuietHours.Start,
			End: value.QuietHours.End, TimeZone: value.QuietHours.TimeZone,
		},
		Rules: rules,
	}
}

func proactiveStatusDTO(value proactive.Status) ProactiveStatusDTO {
	result := ProactiveStatusDTO{Running: value.Running, QueuedEvents: value.QueuedEvents}
	if value.LastHeartbeatAt != nil {
		result.LastHeartbeatAt = value.LastHeartbeatAt.UTC().Format(time.RFC3339Nano)
	}
	if value.NextHeartbeatAt != nil {
		result.NextHeartbeatAt = value.NextHeartbeatAt.UTC().Format(time.RFC3339Nano)
	}
	return result
}
