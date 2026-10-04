package postgres

import (
	"fmt"
	"math"
)

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func optionalTopK(k int) error {
	if k < 0 || k > MaxTopK {
		return ErrInvalidTopK
	}
	return nil
}
func validateDimensions(d int) error {
	if d != 0 && d != DefaultDimensions {
		return fmt.Errorf("postgres retriever: dimensions must match halfvec(%d)", DefaultDimensions)
	}
	return nil
}

func (c VectorConfig) Validate() error {
	if err := optionalTopK(c.TopK); err != nil {
		return err
	}
	if !finite(c.ScoreThreshold) || c.ScoreThreshold < 0 || c.ScoreThreshold > 1 {
		return ErrInvalidThreshold
	}
	return validateDimensions(c.Dimensions)
}
func (c BM25Config) Validate() error {
	if err := optionalTopK(c.TopK); err != nil {
		return err
	}
	if !finite(c.ScoreThreshold) || c.ScoreThreshold < 0 {
		return ErrInvalidThreshold
	}
	return nil
}
func (c HybridConfig) Validate() error {
	if err := optionalTopK(c.TopK); err != nil {
		return err
	}
	if err := optionalTopK(c.ChannelTopK); err != nil {
		return err
	}
	if !finite(c.VectorThreshold) || c.VectorThreshold < 0 || c.VectorThreshold > 1 || !finite(c.KeywordThreshold) || c.KeywordThreshold < 0 {
		return ErrInvalidThreshold
	}
	if c.Timeout < 0 || c.ChannelTimeout < 0 {
		return fmt.Errorf("postgres hybrid: negative timeout")
	}
	if c.FailurePolicy != "" && c.FailurePolicy != FailureStrict && c.FailurePolicy != FailureAllowPartial {
		return fmt.Errorf("postgres hybrid: invalid failure policy %q", c.FailurePolicy)
	}
	if err := validateDimensions(c.Dimensions); err != nil {
		return err
	}
	return c.RRF.Validate()
}
