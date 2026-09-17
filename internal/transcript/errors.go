package transcript

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidIdentifier 表示 AgentID / SessionID 不能安全地作为 Humbert 内部
	// Transcript 路径的一部分。
	ErrInvalidIdentifier = errors.New("Transcript 标识无效")

	// ErrSessionExists 表示目标 Session Transcript 已存在。
	ErrSessionExists = errors.New("Session Transcript 已存在")

	// ErrSessionNotFound 表示目标 Session Transcript 不存在。
	ErrSessionNotFound = errors.New("Session Transcript 不存在")

	// ErrMessageCursorNotFound 表示分页游标不属于当前 Active Branch 的 Message 序列。
	ErrMessageCursorNotFound = errors.New("Transcript Message 分页游标不存在")

	// ErrCompactionStale 表示 Compaction 摘要生成期间 ActiveBranch 已经发生变化。
	// 调用方必须丢弃旧摘要并基于新的分支重新准备，不能把过期切点强行写入 JSONL。
	ErrCompactionStale = errors.New("Session Compaction 已过期")

	// ErrCorrupted 表示 Transcript 存在无法安全自动恢复的数据损坏。
	//
	// 当前只自动修复文件尾部因进程异常退出产生的残缺 JSON；中间损坏绝不会被静默
	// 删除或跳过。
	ErrCorrupted = errors.New("Session Transcript 已损坏")
)

// CorruptionError 描述 Transcript 中无法安全恢复的具体损坏位置。
//
// Reason 只描述结构问题，不包含原始 JSON。原始内容可能包含用户消息、Tool 参数或
// Tool Result，不允许进入普通日志。
type CorruptionError struct {
	Line int

	Offset int64

	Reason string
}

// Error 返回 Transcript 损坏的结构化描述。
func (e *CorruptionError) Error() string {
	if e == nil {
		return ErrCorrupted.Error()
	}

	return fmt.Sprintf(
		"%s: line=%d offset=%d reason=%s",
		ErrCorrupted.Error(),
		e.Line,
		e.Offset,
		e.Reason,
	)
}

// Unwrap 允许调用方使用 errors.Is(err, transcript.ErrCorrupted)。
func (e *CorruptionError) Unwrap() error {
	return ErrCorrupted
}
