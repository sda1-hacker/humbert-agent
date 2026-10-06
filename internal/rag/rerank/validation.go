package rerank

import (
	"fmt"
	"math"
)

// Validate 检查候选数量、阈值、权重和 MMR 参数是否合法。
func (c Config) Validate() error {
	if c.TopK < 0 || c.MaxCandidates < 0 {
		return fmt.Errorf("rag rerank: negative candidate limit")
	}
	for _, v := range []float64{c.Threshold, c.DegradeFactor, c.DegradeFloor, c.FallbackMinScore, c.ModelWeight, c.BaseWeight, c.SourceWeight, c.MMRLambda} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
			return fmt.Errorf("rag rerank: scores, weights and diversity must be finite values in [0,1]")
		}
	}
	if c.ModelWeight+c.BaseWeight+c.SourceWeight > 1.000000001 {
		return fmt.Errorf("rag rerank: total weight exceeds 1")
	}
	return nil
}
