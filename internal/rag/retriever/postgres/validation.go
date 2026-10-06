package postgres

import (
	"fmt"
	"math"
)

// finite 判断分数是否为有限数值。
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// optionalTopK 检查调用级候选数量，零值由组件补齐默认值。
func optionalTopK(k int) error {
	if k < 0 || k > MaxTopK {
		return ErrInvalidTopK
	}
	return nil
}

// validateDimensions 确认向量维度与当前数据库列一致。
func validateDimensions(d int) error {
	if d != 0 && d != DefaultDimensions {
		return fmt.Errorf("postgres retriever: dimensions must match halfvec(%d)", DefaultDimensions)
	}
	return nil
}

// Validate 检查检索数量、阈值、维度或模型输入预算。
func (c VectorConfig) Validate() error {
	if err := optionalTopK(c.TopK); err != nil {
		return err
	}
	if !finite(c.ScoreThreshold) || c.ScoreThreshold < 0 || c.ScoreThreshold > 1 {
		return ErrInvalidThreshold
	}
	return validateDimensions(c.Dimensions)
}

// Validate 检查检索数量、阈值、维度或模型输入预算。
func (c BM25Config) Validate() error {
	if err := optionalTopK(c.TopK); err != nil {
		return err
	}
	if !finite(c.ScoreThreshold) || c.ScoreThreshold < 0 {
		return ErrInvalidThreshold
	}
	return nil
}
