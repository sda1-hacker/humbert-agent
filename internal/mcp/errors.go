package mcp

import "errors"

var (
	ErrInvalidServer       = errors.New("MCP Server 配置不合法")
	ErrServerExists        = errors.New("MCP Server 已存在")
	ErrServerNotFound      = errors.New("MCP Server 不存在")
	ErrServerInUse         = errors.New("MCP Server 仍被 Agent 使用")
	ErrServerDisabled      = errors.New("MCP Server 已停用")
	ErrRuntimeUnavailable  = errors.New("MCP Runtime Backend 尚未就绪")
	ErrRuntimeRetryBackoff = errors.New("MCP Runtime 连接处于重试冷却期")
)
