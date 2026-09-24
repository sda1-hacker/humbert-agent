package memory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

type memoryTestDirectoryResolver struct {
	root string
}

func (r memoryTestDirectoryResolver) SessionDirectory(_ context.Context, sessionID string) (string, error) {
	path := filepath.Join(r.root, sessionID)
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", err
	}
	return path, nil
}

type memoryTestTranscriptRepository struct{}

func (memoryTestTranscriptRepository) LoadTranscript(context.Context, string) (transcript.Document, error) {
	return transcript.Document{}, nil
}

type memoryTestEstimator struct{}

func (memoryTestEstimator) EstimateText(value string) int { return len(value) }

type recordingMemoryModel struct {
	prompts []string
	failAt  int
}

func (m *recordingMemoryModel) Generate(_ context.Context, messages []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	m.prompts = append(m.prompts, messages[len(messages)-1].Content)
	if m.failAt == len(m.prompts) {
		return nil, errors.New("model unavailable")
	}
	return schema.AssistantMessage("### 重要事实\n- 保留目标\n\n### 事情经过\n- 已处理片段", nil), nil
}

func (*recordingMemoryModel) Stream(context.Context, []*schema.Message, ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func TestMemoryProcessesEveryLongHistoryChunkBeforeAdvancingCursor(t *testing.T) {
	store, err := NewStore(memoryTestDirectoryResolver{root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(config.ContextConfig{SerializerMaxChars: 256, OperationTimeoutMS: 1000}, store, memoryTestTranscriptRepository{}, memoryTestEstimator{}, logging.NewBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	branch := make([]transcript.Entry, 0, 4)
	for _, marker := range []string{"FIRST", "MIDDLE", "LAST"} {
		branch = append(branch, transcript.Entry{Type: transcript.EntryMessage, ID: marker, Message: &transcript.AgentMessage{
			Role: transcript.RoleUser, Content: []transcript.ContentBlock{{Type: transcript.ContentText, Text: strings.Repeat("长", 900) + marker}},
		}})
	}
	document := transcript.Document{Header: transcript.SessionHeader{ID: "session-1"}, ActiveBranch: branch, LeafID: "LAST"}
	serialized, _ := serializeSegment(branch, 256)
	model := &recordingMemoryModel{}
	result, next, save, err := manager.prepareAndGenerate(context.Background(), document, Document{}, false, model, true)
	if err != nil || !save || !result.Updated || next.Cursor.CoveredLeafID != "LAST" || len(model.prompts) < 2 {
		t.Fatalf("result=%#v save=%v calls=%d err=%v", result, save, len(model.prompts), err)
	}
	var recovered strings.Builder
	for _, prompt := range model.prompts {
		index := strings.Index(prompt, "会话片段")
		if index < 0 {
			t.Fatalf("missing chunk heading: %q", prompt)
		}
		index = strings.Index(prompt[index:], "\n") + index + 1
		recovered.WriteString(prompt[index:])
	}
	if recovered.String() != serialized {
		t.Fatal("memory refresh omitted or reordered history")
	}

	failing := &recordingMemoryModel{failAt: 2}
	_, _, save, err = manager.prepareAndGenerate(context.Background(), document, Document{}, false, failing, true)
	if err == nil || save {
		t.Fatalf("failed second chunk must not advance cursor: save=%v err=%v", save, err)
	}
}

func TestLegacyMemoryRemainsReadableAndRebuildsOnRefresh(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(memoryTestDirectoryResolver{root: root})
	if err != nil {
		t.Fatal(err)
	}
	branch := []transcript.Entry{{Type: transcript.EntryMessage, ID: "user", Message: &transcript.AgentMessage{
		Role: transcript.RoleUser, Content: []transcript.ContentBlock{{Type: transcript.ContentText, Text: "新的真实目标"}},
	}}}
	legacy := Document{Version: 2, SessionID: "session-1", Cursor: Cursor{CoveredLeafID: "user", LineageHash: lineageHash(branch)},
		Summary: "### 重要事实\n- 旧事实\n\n### 事情经过\n- 旧事件", Artifacts: Artifacts{ModifiedFiles: []string{"denied.txt"}}}
	path := filepath.Join(root, "session-1", memoryFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, exists, err := store.Load(context.Background(), "session-1")
	if err != nil || !exists || loaded.Version != 2 {
		t.Fatalf("legacy memory unreadable: exists=%v document=%#v err=%v", exists, loaded, err)
	}
	manager, err := NewManager(config.ContextConfig{SerializerMaxChars: 256, OperationTimeoutMS: 1000}, store, memoryTestTranscriptRepository{}, memoryTestEstimator{}, logging.NewBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	model := &recordingMemoryModel{}
	result, next, save, err := manager.prepareAndGenerate(context.Background(), transcript.Document{Header: transcript.SessionHeader{ID: "session-1"}, ActiveBranch: branch, LeafID: "user"}, loaded, true, model, true)
	if err != nil || !save || !result.Rebuilt || next.Version != CurrentVersion || len(next.Artifacts.ModifiedFiles) != 0 {
		t.Fatalf("legacy memory was not rebuilt: result=%#v next=%#v err=%v", result, next, err)
	}
	if strings.Contains(model.prompts[0], "旧事实") {
		t.Fatal("legacy summary leaked into rebuilt memory")
	}
}

func TestContextFactsRejectsMemoryFromAbandonedBranch(t *testing.T) {
	t.Parallel()

	store, err := NewStore(memoryTestDirectoryResolver{root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	manager, err := NewManager(
		config.ContextConfig{},
		store,
		memoryTestTranscriptRepository{},
		memoryTestEstimator{},
		logging.NewBootstrap(),
	)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	originalBranch := []transcript.Entry{{ID: "a"}, {ID: "b"}}
	document := Document{
		Version:   CurrentVersion,
		SessionID: "session-1",
		Cursor: Cursor{
			CoveredLeafID: "b",
			LineageHash:   lineageHash(originalBranch),
		},
		Summary:   "### 重要事实\n- 只属于旧分支\n\n### 事情经过\n- 旧事件",
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.Save(context.Background(), "session-1", document); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	fork := []transcript.Entry{{ID: "a"}, {ID: "x"}}
	facts, err := manager.ContextFacts(context.Background(), "session-1", fork)
	if err != nil {
		t.Fatalf("ContextFacts() error = %v", err)
	}
	if facts != "" {
		t.Fatalf("stale branch facts leaked into context: %q", facts)
	}

	facts, err = manager.ContextFacts(context.Background(), "session-1", originalBranch)
	if err != nil {
		t.Fatalf("ContextFacts(valid) error = %v", err)
	}
	if facts != "- 只属于旧分支" {
		t.Fatalf("ContextFacts(valid) = %q", facts)
	}
}
