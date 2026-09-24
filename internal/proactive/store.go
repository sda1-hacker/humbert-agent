package proactive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
)

const (
	storeSchemaVersion = 1
	maxStoredRecords   = 500
	maxPendingEvents   = 1000
)

type storeDocument struct {
	SchemaVersion int `json:"schema_version"`

	Settings      Settings `json:"settings"`
	Records       []Record `json:"records"`
	PendingEvents []Event  `json:"pending_events,omitempty"`

	Workspaces map[string]WorkspaceSnapshot `json:"workspaces"`

	LastHeartbeatAt *time.Time `json:"last_heartbeat_at,omitempty"`
}

type Store struct {
	path string

	mu  sync.RWMutex
	doc storeDocument
}

func NewStore(ctx context.Context, path string) (*Store, error) {
	if ctx == nil {
		return nil, errors.New("context.Context 不能为空")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("主动助手状态文件路径不能为空")
	}
	store := &Store{path: path}
	var doc storeDocument
	if err := atomicfile.ReadJSON(ctx, path, &doc); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("读取主动助手状态失败: %w", err)
		}
		doc = storeDocument{
			SchemaVersion: storeSchemaVersion,
			Settings:      DefaultSettings(),
			Records:       []Record{},
			Workspaces:    map[string]WorkspaceSnapshot{},
		}
		if err := normalizeDocument(&doc); err != nil {
			return nil, err
		}
		if err := store.persistLocked(ctx, doc); err != nil {
			return nil, err
		}
		return store, nil
	}
	if doc.SchemaVersion != storeSchemaVersion {
		return nil, fmt.Errorf("不支持的主动助手状态 schema_version: %d", doc.SchemaVersion)
	}
	if err := normalizeDocument(&doc); err != nil {
		return nil, err
	}
	store.doc = doc
	return store, nil
}

func normalizeDocument(doc *storeDocument) error {
	if doc == nil {
		return errors.New("主动助手状态为空")
	}
	settings, err := NormalizeSettings(doc.Settings)
	if err != nil {
		return err
	}
	doc.Settings = settings
	if doc.Records == nil {
		doc.Records = []Record{}
	}
	if len(doc.Records) > maxStoredRecords {
		doc.Records = append([]Record(nil), doc.Records[len(doc.Records)-maxStoredRecords:]...)
	}
	if doc.Workspaces == nil {
		doc.Workspaces = map[string]WorkspaceSnapshot{}
	}
	if doc.PendingEvents == nil {
		doc.PendingEvents = []Event{}
	}
	if len(doc.PendingEvents) > maxPendingEvents {
		doc.PendingEvents = append([]Event(nil), doc.PendingEvents[len(doc.PendingEvents)-maxPendingEvents:]...)
	}
	return nil
}

// EnqueueEvent 在事件进入内存唤醒队列之前先持久化。EventKey 同时对已处理记录和待处理
// Inbox 去重，因此应用崩溃、重复事件订阅或多次工作区扫描都不会重复执行同一动作。
func (s *Store) EnqueueEvent(ctx context.Context, value Event) (bool, error) {
	value.Key = strings.TrimSpace(value.Key)
	if value.Key == "" {
		return false, errors.New("主动事件 key 不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.doc.Records {
		if record.Event.Key == value.Key {
			return false, nil
		}
	}
	for _, event := range s.doc.PendingEvents {
		if event.Key == value.Key {
			return false, nil
		}
	}
	if len(s.doc.PendingEvents) >= maxPendingEvents {
		return false, errors.New("主动事件 Inbox 已满")
	}
	next := s.doc
	next.PendingEvents = append(append([]Event(nil), s.doc.PendingEvents...), value)
	if err := s.persistLocked(ctx, next); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) PendingEvents() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Event(nil), s.doc.PendingEvents...)
}

func (s *Store) PendingEventCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.doc.PendingEvents)
}

func (s *Store) RemovePendingEvent(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key = strings.TrimSpace(key)
	for index, event := range s.doc.PendingEvents {
		if event.Key != key {
			continue
		}
		next := s.doc
		next.PendingEvents = append(append([]Event(nil), s.doc.PendingEvents[:index]...), s.doc.PendingEvents[index+1:]...)
		return s.persistLocked(ctx, next)
	}
	return nil
}

func NormalizeSettings(value Settings) (Settings, error) {
	defaults := DefaultSettings()
	if value.HeartbeatIntervalMinutes == 0 {
		value.HeartbeatIntervalMinutes = defaults.HeartbeatIntervalMinutes
	}
	if value.HeartbeatIntervalMinutes < 1 || value.HeartbeatIntervalMinutes > 24*60 {
		return Settings{}, errors.New("心跳间隔必须位于 1-1440 分钟")
	}
	if value.QuietHours.Start == "" {
		value.QuietHours.Start = defaults.QuietHours.Start
	}
	if value.QuietHours.End == "" {
		value.QuietHours.End = defaults.QuietHours.End
	}
	if _, _, err := parseClock(value.QuietHours.Start); err != nil {
		return Settings{}, fmt.Errorf("免打扰开始时间无效: %w", err)
	}
	if _, _, err := parseClock(value.QuietHours.End); err != nil {
		return Settings{}, fmt.Errorf("免打扰结束时间无效: %w", err)
	}
	if strings.TrimSpace(value.QuietHours.TimeZone) != "" {
		if _, err := time.LoadLocation(strings.TrimSpace(value.QuietHours.TimeZone)); err != nil {
			return Settings{}, fmt.Errorf("免打扰时区无效: %w", err)
		}
	}
	if value.Rules == nil {
		value.Rules = map[EventKind]EventRule{}
	}
	for kind, defaultRule := range defaults.Rules {
		rule, exists := value.Rules[kind]
		if !exists {
			value.Rules[kind] = defaultRule
			continue
		}
		if rule.Action == "" {
			rule.Action = defaultRule.Action
		}
		if rule.Action != ActionIgnore && rule.Action != ActionNotify && rule.Action != ActionRunAgent {
			return Settings{}, fmt.Errorf("事件 %s 的动作无效: %s", kind, rule.Action)
		}
		if rule.CooldownSeconds < 0 || rule.CooldownSeconds > 24*60*60 {
			return Settings{}, fmt.Errorf("事件 %s 的冷却时间无效", kind)
		}
		rule.AgentID = strings.TrimSpace(rule.AgentID)
		rule.AgentPrompt = strings.TrimSpace(rule.AgentPrompt)
		value.Rules[kind] = rule
	}
	return value, nil
}

func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSettings(s.doc.Settings)
}

func (s *Store) UpdateSettings(ctx context.Context, value Settings) (Settings, error) {
	normalized, err := NormalizeSettings(cloneSettings(value))
	if err != nil {
		return Settings{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.doc
	next.Settings = cloneSettings(normalized)
	if err := s.persistLocked(ctx, next); err != nil {
		return Settings{}, err
	}
	return cloneSettings(normalized), nil
}

func (s *Store) Records(limit int) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.doc.Records) {
		limit = len(s.doc.Records)
	}
	start := len(s.doc.Records) - limit
	result := append([]Record(nil), s.doc.Records[start:]...)
	sort.SliceStable(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result
}

func (s *Store) FindByEventKey(key string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for index := len(s.doc.Records) - 1; index >= 0; index-- {
		if s.doc.Records[index].Event.Key == key {
			return s.doc.Records[index], true
		}
	}
	return Record{}, false
}

func (s *Store) FindByAutomationRunID(runID string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for index := len(s.doc.Records) - 1; index >= 0; index-- {
		if s.doc.Records[index].AutomationRunID == runID {
			return s.doc.Records[index], true
		}
	}
	return Record{}, false
}

func (s *Store) LastHandledKind(kind EventKind) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for index := len(s.doc.Records) - 1; index >= 0; index-- {
		value := s.doc.Records[index]
		if value.Event.Kind == kind && value.Status != RecordIgnored {
			return value, true
		}
	}
	return Record{}, false
}

func (s *Store) PutRecord(ctx context.Context, value Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.doc
	next.Records = append([]Record(nil), s.doc.Records...)
	updated := false
	for index := range next.Records {
		if next.Records[index].ID == value.ID {
			next.Records[index] = value
			updated = true
			break
		}
	}
	if !updated {
		next.Records = append(next.Records, value)
	}
	if len(next.Records) > maxStoredRecords {
		next.Records = append([]Record(nil), next.Records[len(next.Records)-maxStoredRecords:]...)
	}
	return s.persistLocked(ctx, next)
}

func (s *Store) DeferredRecords() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Record, 0)
	for _, value := range s.doc.Records {
		if value.Status == RecordDeferred {
			result = append(result, value)
		}
	}
	return result
}

func (s *Store) WorkspaceSnapshot(agentID string) (WorkspaceSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, exists := s.doc.Workspaces[agentID]
	if !exists {
		return WorkspaceSnapshot{}, false
	}
	value.Files = cloneFileMap(value.Files)
	return value, true
}

func (s *Store) PutWorkspaceSnapshot(ctx context.Context, value WorkspaceSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.doc
	next.Workspaces = make(map[string]WorkspaceSnapshot, len(s.doc.Workspaces)+1)
	for key, snapshot := range s.doc.Workspaces {
		next.Workspaces[key] = snapshot
	}
	value.Files = cloneFileMap(value.Files)
	next.Workspaces[value.AgentID] = value
	return s.persistLocked(ctx, next)
}

func (s *Store) SetHeartbeat(ctx context.Context, value time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value = value.UTC()
	next := s.doc
	next.LastHeartbeatAt = &value
	return s.persistLocked(ctx, next)
}

func (s *Store) LastHeartbeat() *time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.doc.LastHeartbeatAt == nil {
		return nil
	}
	value := *s.doc.LastHeartbeatAt
	return &value
}

// persistLocked 在磁盘写入成功后才提交内存状态，调用方须持有写锁。
func (s *Store) persistLocked(ctx context.Context, next storeDocument) error {
	next.SchemaVersion = storeSchemaVersion
	if err := atomicfile.WriteJSON(ctx, s.path, 0o600, next); err != nil {
		return err
	}
	s.doc = next
	return nil
}

func cloneSettings(value Settings) Settings {
	value.Rules = cloneRules(value.Rules)
	return value
}

func cloneRules(source map[EventKind]EventRule) map[EventKind]EventRule {
	result := make(map[EventKind]EventRule, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneFileMap(source map[string]WorkspaceFileStamp) map[string]WorkspaceFileStamp {
	result := make(map[string]WorkspaceFileStamp, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func parseClock(value string) (int, int, error) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, 0, errors.New("必须使用 HH:MM 格式")
	}
	return parsed.Hour(), parsed.Minute(), nil
}
