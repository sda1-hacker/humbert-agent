package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/sda1-hacker/humbert-agent/internal/approval"
	"github.com/sda1-hacker/humbert-agent/internal/config"
	"github.com/sda1-hacker/humbert-agent/internal/contextengine"
	"github.com/sda1-hacker/humbert-agent/internal/eventbus"
	"github.com/sda1-hacker/humbert-agent/internal/logging"
	"github.com/sda1-hacker/humbert-agent/internal/sessions"
	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

func TestCloseCancelsAndWaitsForInitialization(t *testing.T) {
	s := NewService(nil, nil, nil, nil, nil, nil)
	ctx, finish, err := s.beginOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Close(context.Background()) }()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("close did not cancel initialization")
	}
	if _, _, err := s.beginOperation(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("accepted work after close: %v", err)
	}
	select {
	case err := <-done:
		t.Fatalf("close returned before initialization finished: %v", err)
	default:
	}
	// 第二次 Close 也必须等待，而不是因为 closed=true 就错误地返回成功。
	timeout, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := s.Close(timeout); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second close did not wait: %v", err)
	}
	finish()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not finish")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type blockedMaintenanceRepository struct{ entered chan struct{} }

func (r *blockedMaintenanceRepository) LoadTranscript(ctx context.Context, _ string) (transcript.Document, error) {
	close(r.entered)
	<-ctx.Done()
	return transcript.Document{}, ctx.Err()
}

func (*blockedMaintenanceRepository) AppendCompaction(context.Context, string, transcript.AppendCompactionInput) (transcript.Entry, error) {
	return transcript.Entry{}, errors.New("unexpected compaction write")
}

type unusedModel struct{}

func (unusedModel) Generate(context.Context, []*schema.Message, ...einomodel.Option) (*schema.Message, error) {
	return nil, errors.New("unexpected model call")
}
func (unusedModel) Stream(context.Context, []*schema.Message, ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("unexpected model stream")
}
func (m unusedModel) WithTools([]*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	return m, nil
}

func TestCancelStopsPostTurnMaintenanceAndPublishesCancelled(t *testing.T) {
	repo := &blockedMaintenanceRepository{entered: make(chan struct{})}
	logger := logging.NewBootstrap()
	engine, err := contextengine.NewEngine(config.ContextConfig{AutoCompaction: true, OperationTimeoutMS: 10000}, repo, contextengine.NewApproxEstimator(), logger, nil)
	if err != nil {
		t.Fatal(err)
	}
	events := eventbus.New()
	terminal := make(chan EventType, 2)
	unsubscribe, err := events.Subscribe(TopicEvent, func(_ context.Context, payload any) {
		event := payload.(Event)
		if event.Type == EventTurnCompleted || event.Type == EventTurnCancelled {
			terminal <- event.Type
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	s := NewService(&Resolver{contextEngine: engine}, nil, nil, events, logger, nil)
	defer s.rootCancel()
	ctx, cancel := context.WithCancel(s.rootCtx)
	defer cancel()
	active := &activeRun{
		RequestID: "request", RunID: "run", SessionID: "session", ctx: ctx, cancel: cancel,
		phase: RunPhaseRunning, startedAt: time.Now(), checkpointStore: approval.NewCheckpointStore(),
		snapshot: &Snapshot{RequestID: "request", RunID: "run", SessionID: "session", Model: unusedModel{}, ContextWindow: 8192, MaxOutputTokens: 1024},
	}
	s.activeByRequest["request"] = active
	s.activeBySession["session"] = "request"
	done := make(chan struct{})
	go func() { s.completeTurn(active, ExecutionResult{}); close(done) }()
	select {
	case <-repo.entered:
	case <-time.After(time.Second):
		t.Fatal("maintenance did not start")
	}
	if state := s.activeRunStatus("session"); state == nil || state.Phase != RunPhaseMaintaining {
		t.Fatalf("maintenance phase missing: %#v", state)
	}
	if err := s.CancelTurn("request"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maintenance ignored cancellation")
	}
	select {
	case got := <-terminal:
		if got != EventTurnCancelled {
			t.Fatalf("wrong terminal event: %s", got)
		}
	default:
		t.Fatal("missing cancellation event")
	}
	if s.activeRunStatus("session") != nil {
		t.Fatal("session reservation not released")
	}
}

func TestDeleteRejectsReservedSession(t *testing.T) {
	s := NewService(nil, nil, nil, nil, nil, nil)
	defer s.rootCancel()
	if err := s.reserveSession("session", "initializing"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession(context.Background(), "session"); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("delete did not respect initialization reservation: %v", err)
	}
	if got := s.activeBySession["session"]; got != "initializing" {
		t.Fatalf("reservation changed: %s", got)
	}
}

// cancellationWriter 模拟写入前检查 Context 的持久化边界，不执行任何真实工具。
type cancellationWriter struct{ recorded bool }

func (w *cancellationWriter) AppendAssistantMessage(context.Context, string, *schema.Message, sessions.AssistantPersistence) (sessions.Message, error) {
	return sessions.Message{}, errors.New("unexpected assistant write")
}

func (w *cancellationWriter) AppendToolResult(ctx context.Context, _ string, message *schema.Message, _ sessions.ToolResultPersistence) (sessions.Message, error) {
	if err := ctx.Err(); err != nil {
		return sessions.Message{}, err
	}
	if _, ok := ctx.Deadline(); !ok {
		return sessions.Message{}, errors.New("missing bounded persistence deadline")
	}
	w.recorded = true
	return sessions.Message{Message: message}, nil
}

func TestCompletedToolResultSurvivesConcurrentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	writer := &cancellationWriter{}
	message := schema.ToolMessage("written", "call", schema.WithToolName("write_file"))
	stored, err := persistCompletedTool(ctx, &Snapshot{SessionID: "session", SessionWriter: writer}, message)
	if err != nil || !writer.recorded || stored.Message != message {
		t.Fatalf("completed result lost: %v", err)
	}
}
