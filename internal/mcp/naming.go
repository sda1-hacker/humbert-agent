package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

const maxExposedToolNameLength = 64

// NameExposedTool 把 MCP raw tool name 映射为 Humbert/Eino 模型侧稳定名称。
//
// 规则：
//   - server key 创建后不可修改；
//   - 已经是合法 snake_case 的 raw name 保持可读形式：mcp_github_search_issues；
//   - raw name 需要规范化或名称过长时追加稳定短哈希，避免 foo-bar / foo_bar 等碰撞；
//   - 最终始终符合 Humbert Tool Descriptor 的 <=64 snake_case 约束。
func NameExposedTool(serverKey string, rawToolName string) (string, error) {
	serverKey = strings.TrimSpace(serverKey)
	if err := validateServerKey(serverKey); err != nil {
		return "", err
	}
	raw, err := normalizeRawToolName(rawToolName)
	if err != nil {
		return "", err
	}

	slug := toSnakeSlug(raw)
	if slug == "" {
		return "", fmt.Errorf("无法从 MCP Tool 名称 %q 生成安全名称", raw)
	}

	prefix := "mcp_" + serverKey + "_"
	candidate := prefix + slug
	changed := raw != slug || !isSimpleSnakeName(raw)

	if !changed && len(candidate) <= maxExposedToolNameLength {
		return candidate, nil
	}

	hash := shortNameHash(serverKey + "\x00" + raw)
	maxSlugLen := maxExposedToolNameLength - len(prefix) - 1 - len(hash)
	if maxSlugLen < 1 {
		return "", fmt.Errorf("MCP Server Key %q 太长，无法生成 Tool Name", serverKey)
	}
	if len(slug) > maxSlugLen {
		slug = strings.TrimRight(slug[:maxSlugLen], "_")
		if slug == "" {
			slug = "tool"
		}
	}
	return prefix + slug + "_" + hash, nil
}

func toSnakeSlug(value string) string {
	var builder strings.Builder
	previousUnderscore := false
	for _, char := range strings.TrimSpace(value) {
		lower := unicode.ToLower(char)
		validLetter := lower >= 'a' && lower <= 'z'
		validDigit := lower >= '0' && lower <= '9'
		if validLetter || validDigit {
			builder.WriteRune(lower)
			previousUnderscore = false
			continue
		}
		if !previousUnderscore && builder.Len() > 0 {
			builder.WriteByte('_')
			previousUnderscore = true
		}
	}
	result := strings.Trim(builder.String(), "_")
	if result != "" && result[0] >= '0' && result[0] <= '9' {
		result = "tool_" + result
	}
	return result
}

func isSimpleSnakeName(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		valid := (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_'
		if !valid || (index == 0 && char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}

func shortNameHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:4])
}
