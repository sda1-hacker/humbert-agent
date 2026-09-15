package models

import "errors"

var (
	// ErrProviderNotFound 表示 Provider ID 不存在。
	ErrProviderNotFound = errors.New("模型 Provider 不存在 ")

	// ErrModelNotFound 表示 Model ID 不存在。
	ErrModelNotFound = errors.New("模型不存在 ")

	// ErrModelDisabled 表示模型已被用户禁用。
	ErrModelDisabled = errors.New("模型已被禁用 ")

	// ErrProviderInUse 表示 Provider 下仍然存在 Model，
	// 为避免意外级联删除，必须先删除 Model。
	ErrProviderInUse = errors.New("Provider 仍被模型使用 ")

	// ErrModelInUse 表示该 Model 已被 Run Audit 引用。
	//
	// 为保证历史运行记录完整性，已经参与真实 Agent Run 的模型
	// 不允许物理删除，只能禁用。
	ErrModelInUse = errors.New("模型已经存在运行记录，不能删除 ")

	// ErrModelConflict 表示同一 Provider 已经存在相同 ModelName。
	ErrModelConflict = errors.New("Provider 中已经存在相同模型 ")
)
