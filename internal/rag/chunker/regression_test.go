package chunker

import (
	"fmt"
	"strings"
	"testing"
)

func TestParentChildTableCoordinatesUseSource(t *testing.T) {
	for _, overlap := range []int{0, 20, 80} {
		t.Run(fmt.Sprint(overlap), func(t *testing.T) {
			var table strings.Builder
			table.WriteString("| 序号😀 | 值 |\n| --- | --- |\n")
			for i := 0; i < 30; i++ {
				fmt.Fprintf(&table, "| 行-%02d😀 | 唯一值-%02d |\n", i, i)
			}
			text := table.String()
			source := []rune(text)
			cfg := DefaultConfig()
			cfg.ChunkOverlap = overlap
			cfg.TokenLimit = 32
			cfg.Languages = []string{LangChinese}
			parent, child := DeriveParentChildConfigs(cfg, 160, 80)
			result := SplitParentChild(text, parent, child)
			covered := make([]bool, len(source))
			for _, c := range result.Children {
				if ApproxTokenCount(c.EmbeddingContent(), LangChinese) > 32 {
					t.Fatalf("inherited header exceeded token target: %+v", c)
				}
				if c.Start < 0 || c.End > len(source) || c.Start >= c.End {
					t.Fatalf("invalid child range [%d,%d), source=%d", c.Start, c.End, len(source))
				}
				if !strings.HasSuffix(c.Content, string(source[c.Start:c.End])) {
					t.Fatalf("child does not contain its source slice: %+v", c)
				}
				if c.ParentIndex >= 0 {
					p := result.Parents[c.ParentIndex]
					if c.Start < p.Start || c.End > p.End {
						t.Fatalf("child escapes parent: %+v", c)
					}
				}
				for i := c.Start; i < c.End; i++ {
					covered[i] = true
				}
			}
			for i, ok := range covered {
				if !ok {
					t.Fatalf("uncovered source rune at %d", i)
				}
			}
		})
	}
}

func TestTokenTargetSplitsUnseparatedTextAndPreservesCoverage(t *testing.T) {
	text := strings.Repeat("中😀", 1000)
	cfg := SplitterConfig{ChunkSize: 100, TokenLimit: 32, Languages: []string{"zh"}}
	chunks, _ := SplitWithDiagnostics(text, cfg)
	if len(chunks) < 2 {
		t.Fatal("long unseparated text was not split")
	}
	var restored strings.Builder
	for i, c := range chunks {
		if ApproxTokenCount(c.EmbeddingContent(), "zh") > 32 {
			t.Fatalf("over budget: %+v", c)
		}
		if c.Seq != i {
			t.Fatal("non-contiguous sequence")
		}
		restored.WriteString(c.Content)
	}
	if restored.String() != text {
		t.Fatal("source was lost or duplicated with overlap disabled")
	}
}

func TestZeroOverlapSurvivesParentChildDerivation(t *testing.T) {
	p, c := DeriveParentChildConfigs(SplitterConfig{ChunkOverlap: 0}, 160, 80)
	if ensureDefaults(p).ChunkOverlap != 0 || ensureDefaults(c).ChunkOverlap != 0 {
		t.Fatal("explicit zero overlap changed")
	}
}
