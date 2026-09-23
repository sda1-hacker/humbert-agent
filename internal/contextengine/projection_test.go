package contextengine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

func projectionUser(id, text string) transcript.Entry {
	return transcript.Entry{Type: transcript.EntryMessage, ID: id, Message: &transcript.AgentMessage{
		Role: transcript.RoleUser, Content: []transcript.ContentBlock{{Type: transcript.ContentText, Text: text}}, Timestamp: 1,
	}}
}

func projectionAssistant(id, text, thinking string, calls ...transcript.ContentBlock) transcript.Entry {
	content := make([]transcript.ContentBlock, 0, len(calls)+2)
	if thinking != "" {
		content = append(content, transcript.ContentBlock{Type: transcript.ContentThinking, Thinking: thinking})
	}
	if text != "" {
		content = append(content, transcript.ContentBlock{Type: transcript.ContentText, Text: text})
	}
	content = append(content, calls...)
	return transcript.Entry{Type: transcript.EntryMessage, ID: id, Message: &transcript.AgentMessage{
		Role: transcript.RoleAssistant, Content: content, Provider: "test", Model: "test", StopReason: transcript.StopReasonStop, Timestamp: 1,
	}}
}

func TestProjectionActiveBranchUsesLatestCheckpointAndRecentRawMessages(t *testing.T) {
	t.Parallel()

	branch := []transcript.Entry{
		projectionUser("u1", "old-user"),
		projectionAssistant("a1", "old-assistant", "old-thinking"),
		projectionUser("u2", "recent-user"),
		projectionAssistant("a2", "recent-assistant", "recent-thinking"),
		{Type: transcript.EntryCompaction, ID: "cmp1", Summary: "## Goal\nkeep working", FirstKeptEntryID: "u2"},
	}

	projection, err := projectActiveBranch(transcript.Document{ActiveBranch: branch}, ReasoningReplayAuto)
	if err != nil {
		t.Fatalf("projectActiveBranch() error = %v", err)
	}
	messages := projection.Messages
	if projection.LatestCompactionID != "cmp1" {
		t.Fatalf("compactionID = %q", projection.LatestCompactionID)
	}
	if len(messages) != 3 {
		t.Fatalf("messages len = %d, want 3", len(messages))
	}
	if messages[0].Role != schema.User || !strings.Contains(messages[0].Content, "keep working") {
		t.Fatalf("first message is not checkpoint: %#v", messages[0])
	}
	if strings.Contains(messages[0].Content, "old-user") || messages[1].Content != "recent-user" {
		t.Fatal("compressed raw history leaked into projection")
	}
	if messages[2].ReasoningContent != "recent-thinking" {
		t.Fatalf("reasoning was not preserved in auto mode: %q", messages[2].ReasoningContent)
	}
}

func TestProjectionCachedWindowMatchesFullBranch(t *testing.T) {
	t.Parallel()
	branch := []transcript.Entry{
		projectionUser("u1", "old"),
		projectionUser("u2", "kept"),
		{Type: transcript.EntryCompaction, ID: "cmp1", Summary: "first", FirstKeptEntryID: "u2"},
		projectionAssistant("a2", "answer", "thinking"),
		{Type: transcript.EntryCompaction, ID: "cmp2", Summary: "latest", FirstKeptEntryID: "a2"},
		projectionUser("u3", "current"),
	}
	full, err := projectActiveBranch(transcript.Document{ActiveBranch: branch}, ReasoningReplayAuto)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := projectActiveBranch(transcript.Document{
		ActiveBranch: branch,
		ContextWindow: transcript.ContextWindowIndex{
			Valid: true, LatestCompactionIndex: 4, FirstKeptIndex: 3, Generation: 2,
		},
	}, ReasoningReplayAuto)
	if err != nil {
		t.Fatal(err)
	}
	if cached.Window != full.Window || cached.LatestCompactionID != full.LatestCompactionID || len(cached.Messages) != len(full.Messages) {
		t.Fatalf("cached context differs from full scan: cached=%#v full=%#v", cached, full)
	}
	for index := range full.Messages {
		if cached.Messages[index].Role != full.Messages[index].Role || cached.Messages[index].Content != full.Messages[index].Content ||
			cached.Messages[index].ReasoningContent != full.Messages[index].ReasoningContent {
			t.Fatalf("message %d differs between cached index and full scan", index)
		}
	}
}

func TestProjectionActiveBranchCanOmitReasoning(t *testing.T) {
	t.Parallel()

	branch := []transcript.Entry{projectionAssistant("a1", "answer", "private-reasoning")}
	projection, err := projectActiveBranch(transcript.Document{ActiveBranch: branch}, ReasoningReplayOmit)
	if err != nil {
		t.Fatalf("projectActiveBranch() error = %v", err)
	}
	messages := projection.Messages
	if len(messages) != 1 || messages[0].ReasoningContent != "" {
		t.Fatalf("reasoning should be omitted: %#v", messages)
	}
	if messages[0].Content != "answer" {
		t.Fatalf("visible answer was changed: %q", messages[0].Content)
	}
}

func TestValidateProjectedToolTransactionsRejectsDanglingResult(t *testing.T) {
	t.Parallel()

	tool := schema.ToolMessage("result", "call-1", schema.WithToolName("read_file"))
	if err := validateProjectedToolTransactions([]*schema.Message{tool}); err == nil {
		t.Fatal("expected dangling ToolResult to be rejected")
	}

	assistant := &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
		ID: "call-1", Type: "function", Function: schema.FunctionCall{Name: "read_file", Arguments: string(json.RawMessage(`{"path":"a"}`))},
	}}}
	if err := validateProjectedToolTransactions([]*schema.Message{assistant, tool}); err != nil {
		t.Fatalf("valid tool transaction rejected: %v", err)
	}
}

func TestProjectionAutoDropsCompletedHistoricalReasoning(t *testing.T) {
	t.Parallel()

	branch := []transcript.Entry{
		projectionUser("u1", "first"),
		projectionAssistant("a1", "first-answer", "old-thinking"),
		projectionUser("u2", "second"),
		projectionAssistant("a2", "second-answer", "current-thinking"),
	}
	projection, err := projectActiveBranch(transcript.Document{ActiveBranch: branch}, ReasoningReplayAuto)
	if err != nil {
		t.Fatal(err)
	}
	if projection.RecentMessages[1].ReasoningContent != "" {
		t.Fatalf("completed historical reasoning should be omitted: %q", projection.RecentMessages[1].ReasoningContent)
	}
	if projection.RecentMessages[3].ReasoningContent != "current-thinking" {
		t.Fatalf("current turn reasoning should remain available: %q", projection.RecentMessages[3].ReasoningContent)
	}
}

func TestRetainedCompactionEstimateUsesSameReasoningProjection(t *testing.T) {
	t.Parallel()
	entries := []transcript.Entry{
		projectionUser("u1", "first"),
		projectionAssistant("a1", "answer", "old-thinking"),
		projectionUser("u2", "second"),
		projectionAssistant("a2", "answer", "current-thinking"),
	}
	projected, err := projectActiveBranch(transcript.Document{ActiveBranch: entries}, ReasoningReplayAuto)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := decodeEntries(entries, ReasoningReplayAuto)
	if err != nil {
		t.Fatal(err)
	}
	for index := range retained {
		if retained[index].ReasoningContent != projected.RecentMessages[index].ReasoningContent {
			t.Fatalf("retained estimate differs from provider context at %d", index)
		}
	}
}

func TestProjectionCarriesWindowGenerationAndSourceRange(t *testing.T) {
	t.Parallel()

	branch := []transcript.Entry{
		projectionUser("u1", "old"),
		projectionAssistant("a1", "old-answer", ""),
		projectionUser("u2", "recent"),
		{Type: transcript.EntryCompaction, ID: "cmp1", Summary: "## Goal\nG\n## Constraints & Preferences\nC\n## Progress\n### Done\nD\n### In Progress\nI\n### Blocked\nB\n## Key Decisions\nK\n## Next Steps\nN\n## Critical Context\nX", FirstKeptEntryID: "u2", Details: &transcript.CompactionDetails{
			WindowGeneration: 1, SourceFirstEntryID: "u1", SourceLastEntryID: "a1", SourceEntryCount: 2,
		}},
	}
	projection, err := projectActiveBranch(transcript.Document{ActiveBranch: branch}, ReasoningReplayAuto)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Window.Generation != 1 || projection.Window.StartEntryID != "u2" || projection.Window.CheckpointID != "cmp1" {
		t.Fatalf("unexpected window state: %#v", projection.Window)
	}
	if !projection.Retained.Available || projection.Retained.SourceEntryCount != 2 {
		t.Fatalf("unexpected retained state: %#v", projection.Retained)
	}
	if !strings.Contains(projection.Checkpoint.Content, "session_history") {
		t.Fatalf("checkpoint did not expose history recovery guidance: %q", projection.Checkpoint.Content)
	}
}

func TestProjectionAddsCompatibilityGuidanceForLegacyHistoryTools(t *testing.T) {
	t.Parallel()

	branch := []transcript.Entry{
		projectionUser("u1", "old"),
		projectionUser("u2", "recent"),
		{Type: transcript.EntryCompaction, ID: "cmp1", Summary: "## Goal\n继续任务\n## Critical Context\n旧版本要求使用 history_search/history_read。", FirstKeptEntryID: "u2", Details: &transcript.CompactionDetails{
			WindowGeneration: 1, SourceFirstEntryID: "u1", SourceLastEntryID: "u1", SourceEntryCount: 1,
		}},
	}
	projection, err := projectActiveBranch(transcript.Document{ActiveBranch: branch}, ReasoningReplayAuto)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Checkpoint == nil || !strings.Contains(projection.Checkpoint.Content, "已合并为 session_history") {
		t.Fatalf("legacy history guidance was not migrated: %#v", projection.Checkpoint)
	}
}

func TestProjectionMigratesLegacyContextArtifactGuidance(t *testing.T) {
	t.Parallel()

	legacy := schema.ToolMessage(`{"humbert_context_result_truncated":true,"artifact_id":"artifact-1","instruction":"完整结果已保存在会话上下文产物中；需要中间内容时使用 context_artifact_read 按区间读取。"}`, "call-1", schema.WithToolName("run_command"))
	migrated := normalizeLegacyContextToolGuidance(legacy)
	if migrated == legacy {
		t.Fatal("expected a cloned migrated message")
	}
	if !strings.Contains(migrated.Content, "context_resource") || strings.Contains(migrated.Content, "context_artifact_read") {
		t.Fatalf("legacy context artifact guidance was not migrated: %q", migrated.Content)
	}
}

func TestLongDialogueProjectionKeepsTransactionsAndRecoverableResults(t *testing.T) {
	toolResult := func(id, callID, text string) transcript.Entry {
		return transcript.Entry{Type: transcript.EntryMessage, ID: id, Message: &transcript.AgentMessage{Role: transcript.RoleToolResult, ToolCallID: callID, ToolName: "read_file", Timestamp: 1, Content: []transcript.ContentBlock{{Type: transcript.ContentText, Text: text}}}}
	}
	call := func(id, callID, thinking string) transcript.Entry {
		return projectionAssistant(id, "", thinking, transcript.ContentBlock{Type: transcript.ContentToolCall, ID: callID, Name: "read_file", Arguments: []byte(`{}`)})
	}
	branch := []transcript.Entry{
		projectionUser("u1", "old user"), projectionAssistant("a1", "old answer", "old thinking"),
		projectionUser("u2", "first kept"), {Type: transcript.EntryCompaction, ID: "cp1", Summary: "first checkpoint", FirstKeptEntryID: "u2", Details: &transcript.CompactionDetails{WindowGeneration: 1}},
		projectionUser("u3", "second kept"), projectionAssistant("a3", "previous answer", "previous thinking"),
		{Type: transcript.EntryCompaction, ID: "cp2", Summary: "latest checkpoint", FirstKeptEntryID: "u3", Details: &transcript.CompactionDetails{WindowGeneration: 2, SourceFirstEntryID: "u1", SourceLastEntryID: "a3", SourceEntryCount: 5}},
		{Type: transcript.EntryMessage, ID: "u4", Message: &transcript.AgentMessage{Role: transcript.RoleUser, Timestamp: 1, Content: []transcript.ContentBlock{{Type: transcript.ContentFile, AttachmentID: "attachment-1", Name: "notes.txt", MIMEType: "text/plain", SizeBytes: 12, ExtractedText: "latest evidence"}}}},
		call("a4", "call-big", "current thinking"), toolResult("t-big", "call-big", strings.Repeat("B", 300)),
		call("a5", "call-small", ""), toolResult("t-small", "call-small", "recent result"),
		call("a6", "call-interrupted", ""),
	}
	projected, err := projectActiveBranch(transcript.Document{ActiveBranch: branch}, ReasoningReplayAuto, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateProjectedToolTransactions(projected.Messages); err != nil {
		t.Fatal(err)
	}
	if projected.Window.Generation != 2 || projected.Window.CheckpointID != "cp2" || projected.Window.StartEntryID != "u3" {
		t.Fatalf("wrong latest window: %#v", projected.Window)
	}
	byCall := make(map[string]*schema.Message)
	for _, message := range projected.Messages {
		if message.Role == schema.Tool {
			byCall[message.ToolCallID] = message
		}
	}
	if len(byCall) != 3 || !strings.Contains(byCall["call-big"].Content, "entry_id=t-big") || byCall["call-small"].Content != "recent result" || !strings.Contains(byCall["call-interrupted"].Content, `"status":"unknown"`) {
		t.Fatalf("tool transactions lost: %#v", byCall)
	}
	if !strings.Contains(projected.Messages[0].Content, "latest checkpoint") || strings.Contains(projected.Messages[0].Content, "first checkpoint") {
		t.Fatal("wrong checkpoint")
	}
	if projected.Messages[2].ReasoningContent != "" || projected.Messages[4].ReasoningContent != "current thinking" {
		t.Fatal("thinking replay policy changed across turns")
	}
	if branch[9].Message.Content[0].Text != strings.Repeat("B", 300) {
		t.Fatal("projection mutated archived source")
	}
	if !strings.Contains(projected.Messages[3].Content, "latest evidence") && len(projected.Messages[3].UserInputMultiContent) == 0 {
		t.Fatal("attachment disappeared")
	}
}
