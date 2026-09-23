package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	coreapp "github.com/sda1-hacker/humbert-agent/internal/app"
	"github.com/sda1-hacker/humbert-agent/internal/tasks"
)

// DeskNote is a small user-facing projection of the existing durable TaskRun.
type DeskNote struct {
	TaskID    string `json:"taskID"`
	AgentID   string `json:"agentID"`
	Text      string `json:"text"`
	Status    string `json:"status"`
	RunID     string `json:"runID,omitempty"`
	SessionID string `json:"sessionID,omitempty"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"createdAt"`
}

type DeskService struct{ core *coreapp.Application }

func NewDeskService(core *coreapp.Application) *DeskService { return &DeskService{core: core} }

func (s *DeskService) List(agentID string) ([]DeskNote, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	all, err := s.core.Tasks().List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]DeskNote, 0)
	for _, task := range all {
		if task.AgentID != agentID || task.Origin != "desk_note" {
			continue
		}
		runs, err := s.core.Tasks().Runs(ctx, task.ID)
		if err != nil {
			return nil, err
		}
		note := DeskNote{TaskID: task.ID, AgentID: task.AgentID, Text: task.Prompt, Status: "pending", CreatedAt: task.CreatedAt.Format(time.RFC3339)}
		if len(runs) > 0 {
			run := runs[0]
			for _, candidate := range runs[1:] {
				if candidate.CreatedAt.After(run.CreatedAt) {
					run = candidate
				}
			}
			note.Status = string(run.Status)
			note.RunID, note.SessionID = run.ID, run.SessionID
			note.Result, note.Error = run.ResultPreview, run.Error
		}
		result = append(result, note)
	}
	return result, nil
}

func (s *DeskService) Add(agentID, text string) (DeskNote, error) {
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > 4000 {
		return DeskNote{}, errors.New("便签内容需要 1 到 4000 字")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	name := []rune(text)
	if len(name) > 36 {
		name = name[:36]
	}
	task, err := s.core.Tasks().Create(ctx, tasks.CreateInput{
		AgentID: agentID, Origin: "desk_note", Name: "便签：" + string(name), Prompt: text,
		Execution: tasks.ExecutionAgent, ConversationMode: tasks.ConversationIsolated,
		Status: tasks.TaskStatusActive, Schedule: tasks.Schedule{Type: tasks.ScheduleManual},
		Limits: tasks.Limits{MaxDurationSeconds: 600, MaxModelCalls: 30, MaxToolCalls: 50, MaxTotalTokens: 50000, MaxAttempts: 1},
	})
	if err != nil {
		return DeskNote{}, fmt.Errorf("保存便签失败: %w", err)
	}
	run, err := s.core.Tasks().RunNow(ctx, task.ID)
	if err != nil {
		return DeskNote{}, fmt.Errorf("便签已保存但启动失败: %w", err)
	}
	return DeskNote{TaskID: task.ID, AgentID: agentID, Text: text, Status: string(run.Status), RunID: run.ID, SessionID: run.SessionID, CreatedAt: task.CreatedAt.Format(time.RFC3339)}, nil
}

func (s *DeskService) Retry(taskID string) (DeskNote, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	all, err := s.core.Tasks().List(ctx)
	if err != nil {
		return DeskNote{}, err
	}
	for _, task := range all {
		if task.ID != taskID || task.Origin != "desk_note" {
			continue
		}
		runs, err := s.core.Tasks().Runs(ctx, taskID)
		if err != nil {
			return DeskNote{}, err
		}
		for _, run := range runs {
			switch run.Status {
			case tasks.RunQueued, tasks.RunStarting, tasks.RunRunning, tasks.RunWaitingApproval:
				return DeskNote{}, errors.New("便签仍在处理中")
			}
		}
		run, err := s.core.Tasks().RunNow(ctx, taskID)
		if err != nil {
			return DeskNote{}, err
		}
		return DeskNote{TaskID: task.ID, AgentID: task.AgentID, Text: task.Prompt, Status: string(run.Status), RunID: run.ID, SessionID: run.SessionID, CreatedAt: task.CreatedAt.Format(time.RFC3339)}, nil
	}
	return DeskNote{}, errors.New("便签不存在")
}
