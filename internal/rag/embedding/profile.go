package embedding

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

// NewProfile 构造向量空间身份；维度直接取统一模型配置，不把密钥和请求限额写入档案。
func NewProfile(c ragconfig.Embedding, builder ragconfig.SearchText) Profile {
	c = c.Effective()
	provider := c.Provider
	if provider == ragconfig.ProviderOpenAI {
		// 这是向量空间数据格式的稳定标识，与入口兼容无关。
		provider = "openai-compatible"
	}
	if len(builder.TitleKeys) == 0 && builder.ContextHeaderKey == "" {
		builder = builder.Effective()
	}
	return Profile{Provider: provider, Endpoint: c.BaseURL, Model: c.Model, Revision: c.ModelRevision,
		Dimensions: c.Dimensions, InputVersion: "humbert-search-content-v1", SearchBuilder: builder}
}

// Profile 描述向量空间身份，不包含凭据和请求限额。模型别名背后的权重变化时，应同步修改 Revision。
type Profile struct {
	Provider      string               `json:"provider"`
	Endpoint      string               `json:"endpoint"`
	Model         string               `json:"model"`
	Revision      string               `json:"revision"`
	Dimensions    int                  `json:"dimensions"`
	InputVersion  string               `json:"input_version"`
	SearchBuilder ragconfig.SearchText `json:"search_builder"`
}

// JSON 返回稳定字段顺序的配置快照，供模型档案保存与比较。
func (p Profile) JSON() string { data, _ := json.Marshal(p); return string(data) }

// ID 根据配置快照计算向量空间标识，防止不同模型混用索引。
func (p Profile) ID() string { return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(p.JSON()))) }
