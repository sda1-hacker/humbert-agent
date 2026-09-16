package agents

import "errors"

var (
	// ErrNotFound 表示指定 Agent 不存在。
	ErrNotFound = errors.New("Agent 不存在")

	// ErrDeleting 表示 Agent 已进入可恢复删除流程，不再接受新的读取或写入。
	ErrDeleting = errors.New("Agent 正在删除")
)
