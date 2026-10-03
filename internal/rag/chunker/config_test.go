package chunker

import (
	"reflect"
	"testing"
)

// TestDefaultConfig 验证默认配置。
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.ChunkSize != 512 {
		t.Fatalf(
			"默认 ChunkSize 错误: want=512 got=%d",
			cfg.ChunkSize,
		)
	}

	if cfg.ChunkOverlap != 80 {
		t.Fatalf(
			"默认 ChunkOverlap 错误: want=80 got=%d",
			cfg.ChunkOverlap,
		)
	}

	wantSeparators := []string{
		"\n\n",
		"\n",
		"。",
	}

	if !reflect.DeepEqual(
		cfg.Separators,
		wantSeparators,
	) {
		t.Fatalf(
			"默认 Separators 错误: want=%v got=%v",
			wantSeparators,
			cfg.Separators,
		)
	}
}

// TestDefaultConfigDoesNotShareSeparators
// 验证不同 DefaultConfig 不共享同一个 separators 底层数组。
func TestDefaultConfigDoesNotShareSeparators(t *testing.T) {
	cfg1 := DefaultConfig()
	cfg2 := DefaultConfig()

	cfg1.Separators[0] = "changed"

	if cfg2.Separators[0] == "changed" {
		t.Fatal(
			"DefaultConfig 返回的 Separators 不应该共享底层 slice",
		)
	}
}

// TestNormalizeSplitterConfigDefaults
// 空配置应该恢复成默认值。
func TestNormalizeSplitterConfigDefaults(t *testing.T) {
	cfg := NormalizeSplitterConfig(
		SplitterConfig{},
	)

	if cfg.ChunkSize != DefaultChunkSize {
		t.Fatalf(
			"ChunkSize 默认值错误: %d",
			cfg.ChunkSize,
		)
	}

	if cfg.ChunkOverlap != DefaultChunkOverlap {
		t.Fatalf(
			"ChunkOverlap 默认值错误: %d",
			cfg.ChunkOverlap,
		)
	}

	if !reflect.DeepEqual(
		cfg.Separators,
		DefaultSeparators(),
	) {
		t.Fatalf(
			"Separators 默认值错误: %v",
			cfg.Separators,
		)
	}
}

// TestNormalizeSplitterConfigKeepsCustomValues
// 用户明确传入的正常配置不能被默认值覆盖。
func TestNormalizeSplitterConfigKeepsCustomValues(t *testing.T) {
	cfg := NormalizeSplitterConfig(
		SplitterConfig{
			ChunkSize:    1000,
			ChunkOverlap: 100,

			Separators: []string{
				"\n",
				"。",
			},

			Strategy: StrategyHeading,

			TokenLimit: 512,

			Languages: []string{
				"zh",
			},
		},
	)

	if cfg.ChunkSize != 1000 {
		t.Fatalf(
			"ChunkSize 被错误修改: %d",
			cfg.ChunkSize,
		)
	}

	if cfg.ChunkOverlap != 100 {
		t.Fatalf(
			"ChunkOverlap 被错误修改: %d",
			cfg.ChunkOverlap,
		)
	}

	if cfg.Strategy != StrategyHeading {
		t.Fatalf(
			"Strategy 被错误修改: %s",
			cfg.Strategy,
		)
	}

	if cfg.TokenLimit != 512 {
		t.Fatalf(
			"TokenLimit 被错误修改: %d",
			cfg.TokenLimit,
		)
	}
}

// TestNormalizeSplitterConfigZeroOverlap
//
// 这个测试非常有价值。
//
// 它不是在证明“这样设计最好”，
//
// 而是在固定当前 WeKnora 的真实行为：
//
//	ChunkOverlap <= 0
//	    ↓
//	DefaultChunkOverlap
//
// 所以 0 最终也是 80。
func TestNormalizeSplitterConfigZeroOverlap(t *testing.T) {
	cfg := NormalizeSplitterConfig(
		SplitterConfig{
			ChunkSize:    512,
			ChunkOverlap: 0,
		},
	)

	if cfg.ChunkOverlap != DefaultChunkOverlap {
		t.Fatalf(
			"当前对标行为要求 overlap=0 归一化为 %d, got=%d",
			DefaultChunkOverlap,
			cfg.ChunkOverlap,
		)
	}
}

// TestDeriveParentChildConfigs
// 验证 Parent / Child 配置推导。
func TestDeriveParentChildConfigs(t *testing.T) {
	base := SplitterConfig{
		ChunkSize:    512,
		ChunkOverlap: 80,

		Separators: []string{
			"\n\n",
			"\n",
			"。",
		},

		Strategy: StrategyAuto,

		TokenLimit: 512,

		Languages: []string{
			"zh",
		},
	}

	parent, child :=
		DeriveParentChildConfigs(
			base,
			0,
			0,
		)

	// Parent 默认 4096。
	if parent.ChunkSize != DefaultParentChunkSize {
		t.Fatalf(
			"Parent ChunkSize 错误: want=%d got=%d",
			DefaultParentChunkSize,
			parent.ChunkSize,
		)
	}

	// Parent 不应该继承 TokenLimit。
	if parent.TokenLimit != 0 {
		t.Fatalf(
			"Parent 不应该继承 TokenLimit: got=%d",
			parent.TokenLimit,
		)
	}

	// Child 默认 384。
	if child.ChunkSize != DefaultChildChunkSize {
		t.Fatalf(
			"Child ChunkSize 错误: want=%d got=%d",
			DefaultChildChunkSize,
			child.ChunkSize,
		)
	}

	// 384 / 5 = 76。
	if child.ChunkOverlap != DefaultChildChunkSize/5 {
		t.Fatalf(
			"Child overlap 错误: want=%d got=%d",
			DefaultChildChunkSize/5,
			child.ChunkOverlap,
		)
	}

	// Child 应继承 TokenLimit。
	if child.TokenLimit != base.TokenLimit {
		t.Fatalf(
			"Child TokenLimit 错误: want=%d got=%d",
			base.TokenLimit,
			child.TokenLimit,
		)
	}

	if child.Strategy != StrategyAuto {
		t.Fatalf(
			"Child Strategy 错误: %s",
			child.Strategy,
		)
	}
}
