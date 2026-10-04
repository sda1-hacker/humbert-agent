package postgres

import (
	"fmt"
	"strconv"
)

// SQL encodes BIGINT as text so decoding through map[string]any never loses
// precision above JavaScript's exact integer range. Missing values are legacy.
func documentRevision(metadata map[string]any) (int64, error) {
	v, exists := metadata["rag_document_revision"]
	if !exists {
		return 0, nil
	}
	s, ok := v.(string)
	if !ok {
		return 0, fmt.Errorf("rag retrieval: invalid document revision")
	}
	revision, err := strconv.ParseInt(s, 10, 64)
	if err != nil || revision < 0 {
		return 0, fmt.Errorf("rag retrieval: invalid document revision")
	}
	return revision, nil
}
