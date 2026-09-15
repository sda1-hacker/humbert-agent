package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/sda1-hacker/humbert-agent/internal/transcript"
)

// cursorMatches 判断旧 Memory Cursor 是否仍是当前 ActiveBranch 的有效前缀。
//
// Retry/Fork 后 CoveredLeafID 可能已经不在当前分支；即使同名节点仍可找到，也必须再校验
// 从 root 到该节点的 lineage hash，避免未来导入/修复等操作造成错误的增量合并。
func cursorMatches(cursor Cursor, branch []transcript.Entry) (coveredIndex int, ok bool) {
	coveredID := strings.TrimSpace(cursor.CoveredLeafID)
	if coveredID == "" || strings.TrimSpace(cursor.LineageHash) == "" {
		return -1, false
	}
	for index := range branch {
		if branch[index].ID != coveredID {
			continue
		}
		return index, lineageHash(branch[:index+1]) == cursor.LineageHash
	}
	return -1, false
}

// lineageHash 对 ActiveBranch 的稳定 Entry ID 序列计算 SHA-256。
//
// 只使用 Entry ID，不把消息正文写入 hash 输入，既能识别分支，又避免重复处理可能包含敏感
// 内容的大消息。0 字节分隔符消除简单字符串拼接造成的边界歧义。
func lineageHash(branch []transcript.Entry) string {
	hash := sha256.New()
	for _, entry := range branch {
		_, _ = hash.Write([]byte(entry.ID))
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}
