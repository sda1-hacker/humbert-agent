package preferences

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sda1-hacker/humbert-agent/internal/atomicfile"
)

const personalMemorySchemaVersion = 1
const maxPersonalMemories = 20
const maxPersonalMemoryRunes = 300

// PersonalMemory 是由用户明确保存并可随时编辑/删除的跨会话事实。
type PersonalMemory struct {
	ID              string    `json:"id"`
	Text            string    `json:"text"`
	Source          string    `json:"source"`
	SourceSessionID string    `json:"source_session_id,omitempty"`
	SourceEntryID   string    `json:"source_entry_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type memoryDocument struct {
	SchemaVersion int              `json:"schema_version"`
	Items         []PersonalMemory `json:"items"`
}

func (s *Store) memoryPath() string {
	return filepath.Join(filepath.Dir(s.path), "personal-memory.json")
}

// ListMemories 返回用户维护的跨会话事实，供界面和 Runtime 同一次 Turn 快照读取。
func (s *Store) ListMemories(ctx context.Context) ([]PersonalMemory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, err := s.readMemories(ctx)
	if err != nil {
		return nil, err
	}
	return append([]PersonalMemory(nil), doc.Items...), nil
}

func (s *Store) AddMemory(ctx context.Context, text string) (PersonalMemory, error) {
	return s.AddMemoryWithSource(ctx, text, "", "")
}

// AddMemoryWithSource records the user's confirmed source without importing
// the surrounding conversation into persistent model context.
func (s *Store) AddMemoryWithSource(ctx context.Context, text, sessionID, entryID string) (PersonalMemory, error) {
	text, err := normalizeMemoryText(text)
	if err != nil {
		return PersonalMemory{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readMemories(ctx)
	if err != nil {
		return PersonalMemory{}, err
	}
	if len(doc.Items) >= maxPersonalMemories {
		return PersonalMemory{}, fmt.Errorf("个人记忆最多 %d 条", maxPersonalMemories)
	}
	now := time.Now().UTC()
	source := "manual"
	if sessionID != "" && entryID != "" {
		source = "conversation"
	}
	item := PersonalMemory{ID: uuid.NewString(), Text: text, Source: source, SourceSessionID: sessionID, SourceEntryID: entryID, CreatedAt: now, UpdatedAt: now}
	doc.Items = append(doc.Items, item)
	if err := atomicfile.WriteJSON(ctx, s.memoryPath(), 0o600, doc); err != nil {
		return PersonalMemory{}, err
	}
	return item, nil
}

func (s *Store) UpdateMemory(ctx context.Context, id, text string) (PersonalMemory, error) {
	text, err := normalizeMemoryText(text)
	if err != nil {
		return PersonalMemory{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readMemories(ctx)
	if err != nil {
		return PersonalMemory{}, err
	}
	for index := range doc.Items {
		if doc.Items[index].ID != id {
			continue
		}
		doc.Items[index].Text = text
		doc.Items[index].UpdatedAt = time.Now().UTC()
		if err := atomicfile.WriteJSON(ctx, s.memoryPath(), 0o600, doc); err != nil {
			return PersonalMemory{}, err
		}
		return doc.Items[index], nil
	}
	return PersonalMemory{}, errors.New("个人记忆不存在")
}

func (s *Store) DeleteMemory(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readMemories(ctx)
	if err != nil {
		return err
	}
	for index, item := range doc.Items {
		if item.ID != id {
			continue
		}
		doc.Items = append(doc.Items[:index], doc.Items[index+1:]...)
		return atomicfile.WriteJSON(ctx, s.memoryPath(), 0o600, doc)
	}
	return errors.New("个人记忆不存在")
}

func (s *Store) readMemories(ctx context.Context) (memoryDocument, error) {
	var doc memoryDocument
	if err := atomicfile.ReadJSON(ctx, s.memoryPath(), &doc); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return memoryDocument{SchemaVersion: personalMemorySchemaVersion, Items: []PersonalMemory{}}, nil
		}
		return memoryDocument{}, err
	}
	if doc.SchemaVersion != personalMemorySchemaVersion || len(doc.Items) > maxPersonalMemories {
		return memoryDocument{}, errors.New("个人记忆文件版本或数量无效")
	}
	seen := make(map[string]bool, len(doc.Items))
	for _, item := range doc.Items {
		if item.ID == "" || seen[item.ID] || (item.Source != "manual" && item.Source != "conversation") || item.CreatedAt.IsZero() || item.UpdatedAt.IsZero() {
			return memoryDocument{}, errors.New("个人记忆文件记录无效")
		}
		if item.Source == "conversation" && (item.SourceSessionID == "" || item.SourceEntryID == "") {
			return memoryDocument{}, errors.New("个人记忆来源无效")
		}
		seen[item.ID] = true
		if _, err := normalizeMemoryText(item.Text); err != nil {
			return memoryDocument{}, err
		}
	}
	return doc, nil
}

func normalizeMemoryText(value string) (string, error) {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" || len([]rune(value)) > maxPersonalMemoryRunes {
		return "", fmt.Errorf("个人记忆需要 1 到 %d 个字符", maxPersonalMemoryRunes)
	}
	return value, nil
}
