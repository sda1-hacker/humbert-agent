package embeddinginput

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/sda1-hacker/humbert-agent/internal/rag/searchcontent"
)

// Profile 描述向量空间身份，不包含凭据和请求限额。模型别名背后的权重变化时，应同步修改 Revision。
type Profile struct {
	Provider      string                `json:"provider"`
	Endpoint      string                `json:"endpoint"`
	Model         string                `json:"model"`
	Revision      string                `json:"revision"`
	Dimensions    int                   `json:"dimensions"`
	InputVersion  string                `json:"input_version"`
	SearchBuilder searchcontent.Builder `json:"search_builder"`
}

// JSON 返回稳定字段顺序的配置快照，供模型档案保存与比较。
func (p Profile) JSON() string { data, _ := json.Marshal(p); return string(data) }

// ID 根据配置快照计算向量空间标识，防止不同模型混用索引。
func (p Profile) ID() string { return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(p.JSON()))) }
