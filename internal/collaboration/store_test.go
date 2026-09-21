package collaboration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/sda1-hacker/humbert-agent/internal/agents"
	humberttools "github.com/sda1-hacker/humbert-agent/internal/tools"
)

type testSessionDirectories struct{ root string }

func (r testSessionDirectories) SessionDirectory(_ context.Context, sessionID string) (string, error) {
	directory := filepath.Join(r.root, sessionID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	return directory, nil
}

func TestStoreRoundTripKeepsSubrunInsideParentSession(t *testing.T) {
	store, err := NewStore(testSessionDirectories{root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	value := Run{
		ID: "subrun_1234", ParentSessionID: "session-1", ParentAgentID: "parent",
		ParentRequestID: "request", ParentRunID: "run", ToolCallID: "call",
		ChildAgentID: "child", ChildAgentName: "研究员", Task: "核对事实",
		Status: StatusSucceeded, Result: "完成", StartedAt: now, UpdatedAt: now, FinishedAt: &now,
	}
	if err := store.Save(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := store.Load(context.Background(), "session-1", value.ID)
	if err != nil || !found {
		t.Fatalf("Load()=(%+v,%v,%v)", loaded, found, err)
	}
	if loaded.Result != value.Result || loaded.SchemaVersion != runSchemaVersion {
		t.Fatalf("loaded=%+v", loaded)
	}
	if _, err := os.Stat(filepath.Join(store.sessions.(testSessionDirectories).root, "session-1", "subagents", value.ID+".json")); err != nil {
		t.Fatalf("subrun sidecar missing: %v", err)
	}
}

func TestCorruptSubrunDoesNotBlockHealthySubrun(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(testSessionDirectories{root: root})
	if err != nil {
		t.Fatal(err)
	}
	healthy := Run{ID: "subrun_healthy", ParentSessionID: "session-1", ParentAgentID: "parent", ChildAgentID: "child", Task: "task", Status: StatusRunning, StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := store.Save(context.Background(), healthy); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(root, "session-1", "subagents", "subrun_bad.json")
	if err := os.WriteFile(badPath, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(context.Background(), "session-1", "subrun_bad"); err == nil {
		t.Fatal("corrupt subrun should return an isolated read error")
	}
	if _, found, err := store.Load(context.Background(), "session-1", healthy.ID); err != nil || !found {
		t.Fatalf("healthy subrun unavailable after corrupt neighbor: found=%v err=%v", found, err)
	}
}

func TestStableRunIDUsesRequestAndToolCall(t *testing.T) {
	first := stableRunID("request-1", "call-1")
	if first != stableRunID("request-1", "call-1") {
		t.Fatal("same ToolCall must keep the same subrun id across approval resume")
	}
	if first == stableRunID("request-1", "call-2") || first == stableRunID("request-2", "call-1") {
		t.Fatal("different parent identity must not reuse a subrun id")
	}
}

type testAgentLister struct{ values []agents.AgentInfo }

func (l testAgentLister) List(context.Context) ([]agents.AgentInfo, error) { return l.values, nil }
func (l testAgentLister) Get(_ context.Context, id string) (agents.AgentInfo, error) {
	for _, value := range l.values {
		if value.Agent.ID == id {
			return value, nil
		}
	}
	// Most execution tests only care about the opt-in gate.
	return agents.AgentInfo{Agent: agents.Agent{ID: id, SubagentEnabled: true}}, nil
}

type testAgentBuilder struct{ agent adk.Agent }

func (b testAgentBuilder) BuildChildAgent(context.Context, BuildAgentInput) (BuiltAgent, error) {
	return BuiltAgent{Agent: b.agent, AgentID: "child", AgentName: "研究员"}, nil
}

type testChildAgent struct {
	received string
	calls    int
}

func (a *testChildAgent) Name(context.Context) string        { return "child_agent" }
func (a *testChildAgent) Description(context.Context) string { return "test child" }
func (a *testChildAgent) Run(_ context.Context, input *adk.AgentInput, _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	a.calls++
	if input != nil && len(input.Messages) == 1 {
		a.received = input.Messages[0].Content
	}
	iterator, generator := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	go func() {
		defer generator.Close()
		generator.Send(&adk.AgentEvent{AgentName: "child_agent", Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
			Message: schema.AssistantMessage("已核对：事实成立", nil), Role: schema.Assistant,
		}}})
	}()
	return iterator
}

func TestRunAgentReturnsChildResultAndPersistsAudit(t *testing.T) {
	directories := testSessionDirectories{root: t.TempDir()}
	store, err := NewStore(directories)
	if err != nil {
		t.Fatal(err)
	}
	child := &testChildAgent{}
	manager, err := NewManager(testAgentLister{}, store, testAgentBuilder{agent: child})
	if err != nil {
		t.Fatal(err)
	}
	scope := humberttools.Scope{RequestID: "request", RunID: "run", SessionID: "session", AgentID: "parent"}
	ctx := humberttools.WithCallContext(context.Background(), "call-1", RunAgentToolName)
	output, err := manager.RunAgent(ctx, RunInput{ChildAgentID: "child", Task: "只核对这一条事实", ParentScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if child.received != "只核对这一条事实" {
		t.Fatalf("child received %q", child.received)
	}
	if output.Status != StatusSucceeded || output.Result != "已核对：事实成立" {
		t.Fatalf("output=%+v", output)
	}
	stored, found, err := store.Load(context.Background(), "session", output.RunID)
	if err != nil || !found || stored.Status != StatusSucceeded || stored.Result != output.Result {
		t.Fatalf("stored=(%+v,%v,%v)", stored, found, err)
	}
	replayed, err := manager.RunAgent(ctx, RunInput{ChildAgentID: "child", Task: "只核对这一条事实", ParentScope: scope})
	if err != nil || replayed.Result != output.Result || child.calls != 1 {
		t.Fatalf("idempotent replay=(%+v,%v), child calls=%d", replayed, err, child.calls)
	}
}

func TestListAgentsExcludesCurrentAgent(t *testing.T) {
	store, err := NewStore(testSessionDirectories{root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(testAgentLister{values: []agents.AgentInfo{
		{Agent: agents.Agent{ID: "parent", Name: "主 Agent"}},
		{Agent: agents.Agent{ID: "child", Name: "研究员", Instruction: "负责资料核验", SubagentEnabled: true}},
		{Agent: agents.Agent{ID: "private", Name: "私人 Agent", SubagentEnabled: false}},
	}}, store, testAgentBuilder{agent: &testChildAgent{}})
	if err != nil {
		t.Fatal(err)
	}
	values, err := manager.ListAgents(context.Background(), "parent")
	if err != nil || len(values) != 1 || values[0].ID != "child" {
		t.Fatalf("ListAgents()=(%+v,%v)", values, err)
	}
}

func TestRunAgentRejectsAgentWithoutOptIn(t *testing.T) {
	store, err := NewStore(testSessionDirectories{root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(testAgentLister{values: []agents.AgentInfo{{
		Agent: agents.Agent{ID: "private", Name: "私人 Agent", SubagentEnabled: false},
	}}}, store, testAgentBuilder{agent: &testChildAgent{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := humberttools.WithCallContext(context.Background(), "call", RunAgentToolName)
	_, err = manager.RunAgent(ctx, RunInput{
		ChildAgentID: "private", Task: "task",
		ParentScope: humberttools.Scope{RequestID: "request", RunID: "run", SessionID: "session", AgentID: "parent"},
	})
	if err == nil {
		t.Fatal("disabled Agent must not be callable as a subagent")
	}
}

func TestParentRunFinishedMarksPendingAuditInterrupted(t *testing.T) {
	store, err := NewStore(testSessionDirectories{root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(testAgentLister{}, store, testAgentBuilder{agent: &testChildAgent{}})
	if err != nil {
		t.Fatal(err)
	}
	record := Run{
		ID: "subrun_pending", ParentSessionID: "session", ParentRequestID: "request",
		ParentAgentID: "parent", ChildAgentID: "child", Task: "task",
		Status: StatusWaitingApproval, StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := store.Save(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	manager.track(record)
	manager.ParentRunFinished(context.Background(), "request")
	loaded, found, err := store.Load(context.Background(), "session", record.ID)
	if err != nil || !found || loaded.Status != StatusInterrupted || loaded.FinishedAt == nil {
		t.Fatalf("loaded=(%+v,%v,%v)", loaded, found, err)
	}
}
