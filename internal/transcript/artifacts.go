package transcript

import (
	"encoding/json"
	"strings"
)

// FileArtifactPaths 从已确认成功的 ToolCall 参数中提取文件路径。调用方负责先核对
// ToolResult；这里不把“发起了调用”误当成“操作已完成”。
func FileArtifactPaths(toolName string, arguments json.RawMessage) (read []string, modified []string) {
	var input struct {
		Path    string `json:"path"`
		Changes []struct {
			Path string `json:"path"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return nil, nil
	}
	path := strings.TrimSpace(input.Path)
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read_file":
		if path != "" {
			read = append(read, path)
		}
	case "write_file", "edit_file":
		if path != "" {
			modified = append(modified, path)
		}
	case "apply_patch":
		for _, change := range input.Changes {
			if path := strings.TrimSpace(change.Path); path != "" {
				modified = append(modified, path)
			}
		}
	}
	return read, modified
}
