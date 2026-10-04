package rerank

import (
	"context"
	"math"
	"testing"
)

func TestExplicitZeroScoresAreNotReplacedByDefaults(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Threshold = 0
	cfg.DegradeFactor = 0
	cfg.DegradeFloor = 0
	cfg.FallbackMinScore = 0
	cfg.MMRLambda = 0
	e := NewEngine(nil, cfg)
	if e.config.Threshold != 0 || e.config.DegradeFactor != 0 || e.config.DegradeFloor != 0 || e.config.FallbackMinScore != 0 || e.config.MMRLambda != 0 {
		t.Fatalf("zero values replaced: %+v", e.config)
	}
	cfg.Threshold = math.NaN()
	if _, err := NewEngine(nil, cfg).Rerank(context.Background(), "query", nil); err == nil {
		t.Fatal("invalid config hidden by normalization")
	}
}
