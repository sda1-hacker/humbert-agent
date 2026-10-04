package embeddinginput

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/sda1-hacker/humbert-agent/internal/rag/searchcontent"
)

// Profile describes the embedding space without credentials or request limits.
// Revision must change when a provider deploys new weights behind a model alias.
type Profile struct {
	Provider      string                `json:"provider"`
	Endpoint      string                `json:"endpoint"`
	Model         string                `json:"model"`
	Revision      string                `json:"revision"`
	Dimensions    int                   `json:"dimensions"`
	InputVersion  string                `json:"input_version"`
	SearchBuilder searchcontent.Builder `json:"search_builder"`
}

func (p Profile) JSON() string { data, _ := json.Marshal(p); return string(data) }
func (p Profile) ID() string   { return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(p.JSON()))) }
