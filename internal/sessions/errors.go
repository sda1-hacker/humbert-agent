package sessions

import "errors"

var (
	// ErrSessionNotFound 表示指定 Session 不存在。
	ErrSessionNotFound = errors.New("Session 不存在")

	// ErrInvalidMessageRole 表示调用了错误的强类型 Session 写入入口。
	ErrInvalidMessageRole = errors.New("非法 Message Role")
)
