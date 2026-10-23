package models

import (
	_ "embed"
	"encoding/json"
	"slices"
)

// 同一份 JSON 同时供 Vue 导入和 Go 嵌入；打包后无需从源码目录读取配置。
// 后端读取通用默认预算、协议和能力；各模型的建议预算仍只用于填充新建表单。
//
//go:embed catalog.json
var catalogJSON []byte

type modelPreset struct {
	ID                    string              `json:"id"`
	Capabilities          Capabilities        `json:"capabilities"`
	ReasoningReplay       ReasoningReplayMode `json:"reasoningReplay"`
	ReasoningReplayParams map[string]any      `json:"reasoningReplayParams"`
}

var presetCatalog = func() (catalog struct {
	Defaults struct {
		TimeoutMS       int                                  `json:"timeoutMS"`
		ContextWindow   int                                  `json:"contextWindow"`
		MaxOutputTokens int                                  `json:"maxOutputTokens"`
		ReasoningReplay map[ProviderType]ReasoningReplayMode `json:"reasoningReplay"`
	} `json:"defaults"`
	Providers []struct {
		Type                   ProviderType        `json:"type"`
		ReasoningReplay        ReasoningReplayMode `json:"reasoningReplay"`
		ReasoningReplayParams  map[string]any      `json:"reasoningReplayParams"`
		ReasoningReplayAliases []string            `json:"reasoningReplayAliases"`
		Models                 []modelPreset       `json:"models"`
	} `json:"providers"`
}) {
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		panic("内置模型预设无效: " + err.Error())
	}
	return catalog
}()

// 按协议与明确的模型 ID 查表，不用模糊名称猜测。兼容网关可复用相同模型 ID，
// 网关改名或修改回放协议时，由该模型的显式配置覆盖预设。
func presetForModel(provider ProviderType, name string) (modelPreset, bool) {
	for _, p := range presetCatalog.Providers {
		if p.Type != provider {
			continue
		}
		for _, model := range p.Models {
			if model.ID != name {
				continue
			}
			if model.ReasoningReplay == "" {
				model.ReasoningReplay = p.ReasoningReplay
			}
			if model.ReasoningReplayParams == nil {
				model.ReasoningReplayParams = p.ReasoningReplayParams
			}
			return model, true
		}
		if slices.Contains(p.ReasoningReplayAliases, name) {
			return modelPreset{ReasoningReplay: p.ReasoningReplay, ReasoningReplayParams: p.ReasoningReplayParams}, true
		}
	}
	return modelPreset{}, false
}
