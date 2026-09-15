package approval

import "errors"

var (
	// ErrInvalidDecision 表示前端提交了当前版本不支持的审批动作。
	ErrInvalidDecision = errors.New("Approval 决策不合法")

	// ErrNotFound 表示审批 ID 不存在或已经被 Runtime 清理。
	ErrNotFound = errors.New("Approval Request 不存在")

	// ErrNotPending 表示审批已经进入 resolving/resolved/cancelled/expired，不能再次处理。
	ErrNotPending = errors.New("Approval Request 已不是待处理状态")
)
