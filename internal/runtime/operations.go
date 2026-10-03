package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// 本文件管理 Session/Agent 的独占操作。所有 reservation 与删除状态仍由 Service.mu
// 保护，Turn、压缩及删除使用同一套门禁，避免拆分文件后出现第二份运行状态。
// DeleteSession 与 Turn/手动压缩共用占用锁，后端拒绝删除正在使用的会话。
// 前端的运行状态只用于交互提示，不能作为删除历史的并发保护。
func (s *Service) DeleteSession(ctx context.Context, sessionID string) error {
	ctx, finish, err := s.beginOperation(ctx)
	if err != nil {
		return err
	}
	defer finish()
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("Session ID 不能为空")
	}
	requestID := "delete:" + uuid.NewString()
	if err := s.ensureSessionUnreserved(sessionID); err != nil {
		return err
	}
	session, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	if err := s.reserveAgentSession(sessionID, session.AgentID, requestID); err != nil {
		return err
	}
	defer s.releaseReservation(sessionID, requestID)
	return s.sessions.Delete(ctx, sessionID)
}

// DeleteAgent 协调 Runtime 占用与 Agent 的可恢复删除状态机。
//
// 删除标记设置与 Session reservation 使用同一把锁：已有操作会阻止删除，删除中的 Agent
// 也不会接受新操作。真正的持久化删除由 deleteAgent 回调完成。
func (s *Service) DeleteAgent(ctx context.Context, agentID string, deleteAgent func(context.Context) error) ([]string, error) {
	ctx, finish, err := s.beginOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()

	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("Agent ID 不能为空")
	}
	if deleteAgent == nil {
		return nil, errors.New("Agent 删除回调不能为空")
	}

	requestID := "delete-agent:" + uuid.NewString()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	if existing, exists := s.deletingAgents[agentID]; exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: request_id=%s", ErrAgentDeleting, existing)
	}
	for sessionID, reservedAgentID := range s.reservationAgents {
		if reservedAgentID == agentID {
			existing := s.activeBySession[sessionID]
			s.mu.Unlock()
			return nil, fmt.Errorf("%w: session_id=%s request_id=%s", ErrSessionBusy, sessionID, existing)
		}
	}
	s.deletingAgents[agentID] = requestID
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if current := s.deletingAgents[agentID]; current == requestID {
			delete(s.deletingAgents, agentID)
		}
		s.mu.Unlock()
	}()

	ids, err := s.sessions.ListIDs(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if err := deleteAgent(ctx); err != nil {
		return ids, err
	}
	return ids, nil
}

// beginOperation 将初始化、同步 IO 和审批处理纳入与 Worker 相同的关闭边界。
// 必须在释放生命周期锁前计数；关闭会取消派生 Context，随后等待调用方执行 finish。
func (s *Service) beginOperation(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, errors.New("context.Context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, ErrClosed
	}
	s.wg.Add(1)
	operationCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.rootCtx, cancel)
	return operationCtx, func() {
		stop()
		cancel()
		s.wg.Done()
	}, nil
}

func (s *Service) reserveSession(sessionID string, requestID string) error {
	return s.reserveAgentSession(sessionID, "", requestID)
}

func (s *Service) ensureSessionUnreserved(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if existing, exists := s.activeBySession[sessionID]; exists {
		return fmt.Errorf("%w: request_id=%s", ErrSessionBusy, existing)
	}
	return nil
}

func (s *Service) reserveAgentSession(sessionID string, agentID string, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrClosed
	}
	if existing, exists := s.activeBySession[sessionID]; exists {
		return fmt.Errorf("%w: request_id=%s", ErrSessionBusy, existing)
	}
	if agentID != "" {
		if existing, exists := s.deletingAgents[agentID]; exists {
			return fmt.Errorf("%w: request_id=%s", ErrAgentDeleting, existing)
		}
	}
	s.activeBySession[sessionID] = requestID
	if agentID != "" {
		s.reservationAgents[sessionID] = agentID
	}
	return nil
}

func (s *Service) releaseReservation(sessionID string, requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, exists := s.activeBySession[sessionID]
	if !exists || current != requestID {
		return
	}
	delete(s.activeBySession, sessionID)
	delete(s.reservationAgents, sessionID)
}
