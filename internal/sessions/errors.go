package sessions

import "errors"

var (
	// ErrSessionNotFound 表示指定 Session 不存在。
	ErrSessionNotFound = errors.New("Session 不存在")

	// ErrSessionUnavailable 表示 Session 目录存在，但其当前数据损坏或不一致。
	// Store 会隔离该 Session，使其它会话和应用启动不受影响。
	ErrSessionUnavailable = errors.New("Session 数据不可用")

	// ErrInvalidMessageRole 表示调用了错误的强类型 Session 写入入口。
	ErrInvalidMessageRole = errors.New("非法 Message Role")
)
