package projects

import "errors"

var (
	ErrNotFound      = errors.New("Project 不存在")
	ErrAlreadyExists = errors.New("Project 已存在")
)
