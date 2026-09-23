package contextengine

import (
	"context"
	"strings"
	"testing"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

const testCheckpoint = "## Goal\nG\n## Constraints & Preferences\nC\n## Progress\n### Done\nD\n### In Progress\nI\n### Blocked\nB\n## Key Decisions\nK\n## Next Steps\nN\n## Critical Context\nX"

type recordingCheckpointModel struct {
	prompts []string
	summary string
}

func (m *recordingCheckpointModel) Generate(_ context.Context, messages []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	m.prompts = append(m.prompts, messages[len(messages)-1].Content)
	return schema.AssistantMessage(m.summary, nil), nil
}

func (*recordingCheckpointModel) Stream(context.Context, []*schema.Message, ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func TestLocalFallbackKeepsLatestDecision(t *testing.T) {
	t.Parallel()
	messages := []*schema.Message{schema.UserMessage("INITIAL_GOAL")}
	for i := 0; i < 20; i++ {
		messages = append(messages, schema.AssistantMessage(strings.Repeat("middle-detail-", 35), nil))
	}
	messages = append(messages, schema.UserMessage("LATEST_DECISION: use the new design"))
	checkpoint := localFallbackCheckpoint("", messages, 2048)
	if !strings.Contains(checkpoint, "INITIAL_GOAL") || !strings.Contains(checkpoint, "LATEST_DECISION") {
		t.Fatalf("emergency checkpoint omitted the beginning or latest decision: %s", checkpoint)
	}
	if !strings.Contains(checkpoint, "中间历史省略") {
		t.Fatal("emergency checkpoint did not disclose omitted middle history")
	}
}

func TestCheckpointSerializerKeepsImageIdentityWithoutGuessingContent(t *testing.T) {
	t.Parallel()
	imageURL := "humbert-attachment://image-42"
	message := &schema.Message{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{{
		Type:  schema.ChatMessagePartTypeImageURL,
		Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{URL: &imageURL, MIMEType: "image/png"}},
		Extra: map[string]any{"attachment_id": "image-42", "name": "diagram.png"},
	}}}
	serialized := serializeMessagesForCheckpoint([]*schema.Message{message}, 4000)
	for _, expected := range []string{"image-42", "diagram.png", "Visual details are unavailable"} {
		if !strings.Contains(serialized, expected) {
			t.Fatalf("missing image recovery marker %q: %s", expected, serialized)
		}
	}
	fallback := localFallbackCheckpoint("", []*schema.Message{message}, 2048)
	if !strings.Contains(fallback, "image-42") {
		t.Fatalf("emergency checkpoint lost image identity: %s", fallback)
	}
}

func TestCheckpointGeneratorFeedsEveryLongHistorySegment(t *testing.T) {
	t.Parallel()
	model := &recordingCheckpointModel{summary: testCheckpoint}
	input := make([]*schema.Message, 0, 40)
	for i := 0; i < 40; i++ {
		input = append(input, schema.UserMessage(strings.Repeat("long history line ", 40)+" marker="+string(rune('A'+i))))
	}
	generator := CheckpointGenerator{Model: model, Estimator: NewApproxEstimator(), ContextWindow: 4096, MaxOutputTokens: 512, OperationTimeout: time.Second}
	got, err := generator.Generate(context.Background(), CheckpointInput{Messages: input})
	if err != nil {
		t.Fatal(err)
	}
	if got != testCheckpoint || len(model.prompts) < 2 {
		t.Fatalf("expected a rolling multi-chunk checkpoint, calls=%d result=%q", len(model.prompts), got)
	}
	var allNewHistory strings.Builder
	for _, prompt := range model.prompts {
		position := strings.Index(prompt, "[New history to merge]\n")
		if position < 0 {
			t.Fatalf("missing history segment: %s", prompt)
		}
		allNewHistory.WriteString(prompt[position+len("[New history to merge]\n"):])
	}
	for i := 0; i < 40; i++ {
		marker := "marker=" + string(rune('A'+i))
		if !strings.Contains(allNewHistory.String(), marker) {
			t.Fatalf("history segment %q was omitted", marker)
		}
	}
}

type repairSessionRepository struct {
	branch    []transcript.Entry
	fullReads int
	committed transcript.Entry
}

type windowSessionRepository struct{ repairSessionRepository }

func (r *windowSessionRepository) LoadContextTranscript(context.Context, string) (transcript.Document, error) {
	branch := r.branch[2:]
	return transcript.Document{ActiveBranch: append([]transcript.Entry(nil), branch...), LeafID: r.branch[len(r.branch)-1].ID}, nil
}

func (r *repairSessionRepository) snapshot() transcript.Document {
	return transcript.Document{ActiveBranch: append([]transcript.Entry(nil), r.branch...), LeafID: r.branch[len(r.branch)-1].ID}
}
func (r *repairSessionRepository) LoadContextTranscript(context.Context, string) (transcript.Document, error) {
	return r.snapshot(), nil
}
func (r *repairSessionRepository) LoadTranscript(context.Context, string) (transcript.Document, error) {
	r.fullReads++
	return r.snapshot(), nil
}
func (r *repairSessionRepository) AppendCompaction(_ context.Context, _ string, input transcript.AppendCompactionInput) (transcript.Entry, error) {
	entry := transcript.Entry{Type: transcript.EntryCompaction, ID: "repaired", Summary: input.Summary, FirstKeptEntryID: input.FirstKeptEntryID, Details: &input.Details}
	r.branch = append(r.branch, entry)
	r.committed = entry
	return entry, nil
}

func TestCompactRepairsDegradedCheckpointFromOriginalSource(t *testing.T) {
	t.Parallel()
	repo := &repairSessionRepository{branch: []transcript.Entry{
		plannerEntry("u1", transcript.RoleUser, "old"),
		plannerEntry("a1", transcript.RoleAssistant, "old answer"),
		plannerEntry("u2", transcript.RoleUser, "original decision"),
		plannerEntry("a2", transcript.RoleAssistant, "decision result"),
		{Type: transcript.EntryCompaction, ID: "healthy", Summary: testCheckpoint, FirstKeptEntryID: "u2", Details: &transcript.CompactionDetails{ReadFiles: []string{"old.txt"}}},
		plannerEntry("u3", transcript.RoleUser, "keep recent"),
		plannerEntry("a3", transcript.RoleAssistant, "recent answer"),
		{Type: transcript.EntryCompaction, ID: "degraded", Summary: testCheckpoint + strings.Repeat(" incomplete", 300), FirstKeptEntryID: "u3", Details: &transcript.CompactionDetails{Degraded: true, SourceFirstEntryID: "u2", ReadFiles: []string{"old.txt"}}},
		plannerEntry("u4", transcript.RoleUser, "new turn"),
		plannerEntry("a4", transcript.RoleAssistant, "new answer"),
	}}
	engine, err := NewEngine(testContextConfig(), repo, NewApproxEstimator(), logging.NewBootstrap(), nil)
	if err != nil {
		t.Fatal(err)
	}
	model := &recordingCheckpointModel{summary: testCheckpoint}
	result, err := engine.Compact(context.Background(), CompactRequest{
		SessionID: "session", Model: model, ContextWindow: 16 * 1024, MaxOutputTokens: 2048,
		CompactionContextWindow: 16 * 1024, CompactionMaxOutputTokens: 2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted || repo.fullReads != 1 || repo.committed.Details == nil || repo.committed.Details.Degraded {
		t.Fatalf("degraded checkpoint was not repaired: result=%#v reads=%d committed=%#v", result, repo.fullReads, repo.committed)
	}
	if repo.committed.Details.SourceFirstEntryID != "u2" || len(repo.committed.Details.ReadFiles) != 1 || repo.committed.Details.ReadFiles[0] != "old.txt" {
		t.Fatalf("repair lost original source or file metadata: %#v", repo.committed.Details)
	}
}

func TestCompactHealthyWindowDoesNotReloadFullTranscript(t *testing.T) {
	t.Parallel()
	chunk := strings.Repeat("中", 200)
	repo := &windowSessionRepository{repairSessionRepository{branch: []transcript.Entry{
		plannerEntry("u1", transcript.RoleUser, "old"), plannerEntry("a1", transcript.RoleAssistant, "old"),
		plannerEntry("u2", transcript.RoleUser, chunk), plannerEntry("a2", transcript.RoleAssistant, chunk),
		{Type: transcript.EntryCompaction, ID: "healthy", Summary: testCheckpoint, FirstKeptEntryID: "u2", Details: &transcript.CompactionDetails{WindowGeneration: 1}},
		plannerEntry("u3", transcript.RoleUser, chunk), plannerEntry("a3", transcript.RoleAssistant, chunk),
		plannerEntry("u4", transcript.RoleUser, chunk), plannerEntry("a4", transcript.RoleAssistant, chunk),
	}}}
	engine, err := NewEngine(testContextConfig(), repo, NewApproxEstimator(), logging.NewBootstrap(), nil)
	if err != nil {
		t.Fatal(err)
	}
	model := &recordingCheckpointModel{summary: testCheckpoint}
	result, err := engine.Compact(context.Background(), CompactRequest{
		SessionID: "session", Model: model, ContextWindow: 4096, MaxOutputTokens: 1024,
		CompactionContextWindow: 16 * 1024, CompactionMaxOutputTokens: 2048,
		Instruction: strings.Repeat("系", 1700), Force: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted || repo.fullReads != 0 {
		t.Fatalf("healthy compaction reloaded full history: compacted=%t full_reads=%d", result.Compacted, repo.fullReads)
	}
}
