package contextengine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

type summaryModel struct {
	calls int
	fail  bool
}

func (m *summaryModel) Generate(_ context.Context, _ []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	m.calls++
	if m.fail {
		return nil, errors.New("offline")
	}
	return schema.AssistantMessage("保留用户目标：完成当前任务。", nil), nil
}
func (m *summaryModel) Stream(context.Context, []*schema.Message, ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("unexpected stream")
}
func newSummaryHandler(t *testing.T, model *summaryModel) *MidRunCompactor {
	t.Helper()
	h, err := NewMidRunCompactor(ContextMiddlewareConfig{
		SessionID: "session", Model: model, Estimator: NewApproxEstimator(), Logger: logging.NewBootstrap(),
		CompactionContextWindow: 32768, CompactionMaxOutputTokens: 1024,
		Budget: Budget{ContextWindow: 32768, ThresholdTokens: 6000, SoftThresholdTokens: 1000, TargetRecentTokens: 1, CheckpointBudgetTokens: 512},
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func summaryMessages() []*schema.Message {
	return []*schema.Message{schema.UserMessage(strings.Repeat("历史需求", 1000)), schema.AssistantMessage(strings.Repeat("已完成工作", 1000), nil), schema.UserMessage("继续检查测试")}
}

func TestNativeSummarizationAndCommitUseOneModelCall(t *testing.T) {
	m := &summaryModel{}
	h := newSummaryHandler(t, m)
	messages := summaryMessages()
	messages[2].Extra = map[string]any{entryIDKey: "u2"}
	state := &adk.ChatModelAgentState{Messages: messages}
	_, after, err := h.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.calls != 1 || len(after.Messages) != 2 || after.Messages[1] != messages[2] {
		t.Fatalf("summary calls=%d messages=%d", m.calls, len(after.Messages))
	}
	committed := 0
	h.commit = func(_ context.Context, p *pendingSummary) error {
		committed++
		if p.FirstKept != messages[2] {
			t.Fatal("wrong source boundary")
		}
		return nil
	}
	if err := h.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.calls != 1 || committed != 1 {
		t.Fatalf("repeated generation/commit: calls=%d commits=%d", m.calls, committed)
	}
	if state.Messages[0].Content != messages[0].Content || len(state.Messages) != 3 {
		t.Fatal("raw state mutated")
	}
}
func TestSummaryFailureRetainsRawStateAndEnforcesHardLimit(t *testing.T) {
	m := &summaryModel{fail: true}
	h := newSummaryHandler(t, m)
	state := &adk.ChatModelAgentState{Messages: summaryMessages()}
	_, after, err := h.BeforeModelRewriteState(context.Background(), state, nil)
	if !errors.Is(err, ErrContextBudgetExceeded) || after != state || h.pending != nil {
		t.Fatalf("failed summary lost raw state: %v", err)
	}
	h.config.Budget.ThresholdTokens = 20000
	_, after, err = h.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil || after != state {
		t.Fatalf("soft failure should retain usable state: %v", err)
	}
}
func TestSummaryBoundaryKeepsToolTransaction(t *testing.T) {
	h := newSummaryHandler(t, &summaryModel{})
	msgs := summaryMessages()[:2]
	msgs = append(msgs, &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call", Function: schema.FunctionCall{Name: "read_file"}}}}, schema.ToolMessage("result", "call"))
	_, boundary, err := h.summaryBoundary(msgs)
	if err != nil || boundary != 2 {
		t.Fatalf("split tool transaction: %d %v", boundary, err)
	}
}
func TestSkillReferenceCannotReplaceMainDefinition(t *testing.T) {
	msgs := []*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "main", Function: schema.FunctionCall{Name: "skill", Arguments: `{"skill":"demo"}`}}}}, schema.ToolMessage("主定义", "main"),
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "ref", Function: schema.FunctionCall{Name: "skill", Arguments: `{"skill":"demo","file":"references/a.md"}`}}}}, schema.ToolMessage("参考文件", "ref"),
	}
	summary := preserveSkillDefinitions(msgs, "摘要", len(msgs))
	if !strings.Contains(summary, "主定义") || strings.Contains(summary, "参考文件") {
		t.Fatal(summary)
	}
	second := preserveSkillDefinitions([]*schema.Message{schema.UserMessage(compactionCheckpointPrefix + summary + "\nother")}, "第二次摘要", 1)
	if !strings.Contains(second, "主定义") {
		t.Fatal("skill definition lost after repeated compaction")
	}
}

type summaryRepository struct {
	doc    transcript.Document
	writes int
	stale  bool
}

func (r *summaryRepository) LoadContextTranscript(context.Context, string) (transcript.Document, error) {
	return r.doc, nil
}
func (r *summaryRepository) LoadTranscript(context.Context, string) (transcript.Document, error) {
	return r.doc, nil
}
func (r *summaryRepository) AppendCompaction(_ context.Context, _ string, input transcript.AppendCompactionInput) (transcript.Entry, error) {
	if r.stale || input.ExpectedLeafID != r.doc.LeafID {
		return transcript.Entry{}, transcript.ErrCompactionStale
	}
	r.writes++
	return transcript.Entry{ID: "cp"}, nil
}
func TestCommitWaitsForRawBoundaryAndRejectsChangedBranch(t *testing.T) {
	repo := &summaryRepository{doc: transcript.Document{LeafID: "a1", ActiveBranch: []transcript.Entry{projectionUser("u1", "old"), projectionAssistant("a1", "done", "")}}}
	engine, err := NewEngine(testContextConfig(), repo, NewApproxEstimator(), logging.NewBootstrap())
	if err != nil {
		t.Fatal(err)
	}
	kept := schema.UserMessage("new")
	kept.Extra = map[string]any{entryIDKey: "u2"}
	pending := &pendingSummary{Summary: "summary", FirstKept: kept}
	if err := engine.commitSummary(context.Background(), "s", pending); !errors.Is(err, transcript.ErrCompactionStale) || repo.writes != 0 {
		t.Fatalf("committed ahead of transcript: %v", err)
	}
	repo.doc.ActiveBranch = append(repo.doc.ActiveBranch, projectionUser("u2", "new"))
	repo.doc.LeafID = "u2"
	repo.stale = true
	if err := engine.commitSummary(context.Background(), "s", pending); !errors.Is(err, transcript.ErrCompactionStale) {
		t.Fatalf("stale commit accepted: %v", err)
	}
	repo.stale = false
	if err := engine.commitSummary(context.Background(), "s", pending); err != nil {
		t.Fatal(err)
	}
	if repo.writes != 1 {
		t.Fatal(repo.writes)
	}
}

func TestRepeatedSummariesKeepCurrentRequestVerbatim(t *testing.T) {
	h := newSummaryHandler(t, &summaryModel{})
	request := "检查报表；只能修改 src/a.go，金额为 123.45 元。"
	messages := summaryMessages()[:2]
	messages = append(messages, schema.UserMessage(request), schema.AssistantMessage(strings.Repeat("工具进度", 500), nil), &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "last", Function: schema.FunctionCall{Name: "read_file"}}}}, schema.ToolMessage("result", "last"))
	after, err := h.Summarize(context.Background(), &adk.ChatModelAgentState{Messages: messages})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after[0].Content, request) {
		t.Fatal("current request lost")
	}
	after = append(after, schema.AssistantMessage(strings.Repeat("后续进度", 500), nil), &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "next", Function: schema.FunctionCall{Name: "read_file"}}}}, schema.ToolMessage("result", "next"))
	again, err := h.Summarize(context.Background(), &adk.ChatModelAgentState{Messages: after})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again[0].Content, request) {
		t.Fatal("current request lost after second summary")
	}
}
