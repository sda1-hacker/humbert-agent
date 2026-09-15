package agents

import "errors"

var (
	// ErrNotFound 表示指定 Agent 不存在。
	ErrNotFound = errors.New("Agent 不存在 ")

	// ErrInUse 表示 Agent 下仍然存在 Session。
	//
	// 为避免误删除完整会话历史，拥有 Session 的 Agent
	// 不允许直接物理删除。
	ErrInUse = errors.New("Agent 仍然存在会话 ")
)
