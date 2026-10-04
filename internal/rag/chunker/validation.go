package chunker

import "fmt"

func (c SplitterConfig) Validate() error {
	if c.ChunkSize < 0 || c.ChunkOverlap < 0 || c.TokenLimit < 0 {
		return fmt.Errorf("rag chunker: sizes, overlap and token limit must be non-negative")
	}
	switch c.Strategy {
	case "", StrategyAuto, StrategyHeading, StrategyHeuristic, StrategyRecursive, StrategyLegacy:
	default:
		return fmt.Errorf("rag chunker: unknown strategy %q", c.Strategy)
	}
	for _, s := range c.Separators {
		if s == "" {
			return fmt.Errorf("rag chunker: empty separator")
		}
	}
	return nil
}
