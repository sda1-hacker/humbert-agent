package indexer

import (
	"context"
	"errors"
	"testing"

	eino "github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
)

type storeFunc func(context.Context, []*schema.Document, ...eino.Option) ([]string, error)

func (f storeFunc) Store(ctx context.Context, docs []*schema.Document, opts ...eino.Option) ([]string, error) {
	return f(ctx, docs, opts...)
}

func TestMultiKeepsIndependentInputsAndScope(t *testing.T) {
	first := storeFunc(func(_ context.Context, docs []*schema.Document, opts ...eino.Option) ([]string, error) {
		if o := eino.GetCommonOptions(nil, opts...); o.Index == nil || *o.Index != "kb" {
			t.Fatal("索引范围丢失")
		}
		docs[0].Content, docs[0].MetaData["title"] = "修改正文", "修改标题"
		return []string{"a"}, nil
	})
	second := storeFunc(func(_ context.Context, docs []*schema.Document, _ ...eino.Option) ([]string, error) {
		if docs[0].Content != "正文" || docs[0].MetaData["title"] != "标题" {
			t.Fatal("一路修改污染了另一路")
		}
		return []string{"a"}, nil
	})
	idx, err := NewMulti(first, second)
	if err != nil {
		t.Fatal(err)
	}
	docs := []*schema.Document{{ID: "a", Content: "正文", MetaData: map[string]any{"title": "标题"}}}
	ids, err := idx.Store(context.Background(), docs, eino.WithIndex("kb"))
	if err != nil || len(ids) != 1 || ids[0] != "a" || docs[0].Content != "正文" {
		t.Fatalf("%v %v", ids, err)
	}
}

func TestMultiStopsOnFailureOrChangedIDs(t *testing.T) {
	unavailable := errors.New("索引写入失败")
	for _, name := range []string{"failure", "changed-id", "duplicate-id"} {
		t.Run(name, func(t *testing.T) {
			first := storeFunc(func(context.Context, []*schema.Document, ...eino.Option) ([]string, error) {
				switch name {
				case "failure":
					return nil, unavailable
				case "changed-id":
					return []string{"a", "other"}, nil
				default:
					return []string{"a", "a"}, nil
				}
			})
			second := storeFunc(func(context.Context, []*schema.Document, ...eino.Option) ([]string, error) {
				t.Fatal("失败后不应继续写入其他索引")
				return nil, nil
			})
			idx, _ := NewMulti(first, second)
			_, err := idx.Store(context.Background(), []*schema.Document{{ID: "a"}, {ID: "b"}})
			if err == nil || name == "failure" && !errors.Is(err, unavailable) {
				t.Fatal(err)
			}
		})
	}
}
