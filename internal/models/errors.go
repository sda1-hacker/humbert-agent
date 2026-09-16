package models

import (
	"errors"
	"fmt"
)

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

	// ErrModelInUse 表示该 Model 仍被当前 Agent Profile 的某个模型角色引用。
	//
	// 删除模型配置只需要保护当前配置引用，历史 Session 已经持久化了实际
	// Provider/Model 元数据，不依赖 models.json 中的配置继续存在。
	// 保留该错误名以兼容既有调用方。
	ErrModelInUse = errors.New("模型仍被 Agent 模型角色使用，不能删除")

	// ErrModelConflict 表示同一 Provider 已经存在相同 ModelName。
	ErrModelConflict = errors.New("Provider 中已经存在相同模型 ")
)

// InvalidCapabilityModeError 让上层可以稳定识别错误，同时保留具体非法值。
type InvalidCapabilityModeError struct {
	Value string
}

func (e *InvalidCapabilityModeError) Error() string {
	return fmt.Sprintf("模型 Capability 模式无效: %q，只允许 auto/enabled/disabled", e.Value)
}
