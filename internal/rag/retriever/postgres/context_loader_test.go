package postgres

import (
	"context"
	"testing"
)

func TestParentLoaderLoadsParentsInOneQuery(t *testing.T) {
	db := &fakeQueryer{
		resultRows: &fakeRows{
			data: [][]any{
				parentRow(
					"parent-a",
					"doc-1",
					"Parent A Content",
				),
				parentRow(
					"parent-b",
					"doc-2",
					"Parent B Content",
				),
			},
		},
	}

	loader := newParentLoader(
		db,
		"kb-1",
	)

	parents, err := loader.LoadParents(
		context.Background(),
		[]string{
			"parent-a",
			"parent-b",
		},
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(db.calls) != 1 {
		t.Fatalf(
			"Parent Loader 应只有一次 SQL: %d",
			len(db.calls),
		)
	}

	if len(parents) != 2 {
		t.Fatalf(
			"应该加载2个 Parent: %d",
			len(parents),
		)
	}

	if parents["parent-a"].Content !=
		"Parent A Content" {

		t.Fatalf(
			"Parent A Content 错误: %+v",
			parents["parent-a"],
		)
	}
}

func TestParentLoaderDeduplicatesIDs(t *testing.T) {
	db := &fakeQueryer{
		resultRows: &fakeRows{},
	}

	loader := newParentLoader(
		db,
		"kb",
	)

	_, err := loader.LoadParents(
		context.Background(),
		[]string{
			"parent-a",
			"parent-a",
			"",
			"parent-b",
		},
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(db.calls) != 1 {
		t.Fatalf(
			"应该只执行一次 SQL: %d",
			len(db.calls),
		)
	}

	ids, ok := db.calls[0].args[1].([]string)

	if !ok {
		t.Fatalf(
			"第二个参数应为 []string: %T",
			db.calls[0].args[1],
		)
	}

	if len(ids) != 2 {
		t.Fatalf(
			"重复 Parent ID 应去重: %v",
			ids,
		)
	}
}

func TestParentLoaderEmptyInputDoesNotQuery(t *testing.T) {
	db := &fakeQueryer{}

	loader := newParentLoader(
		db,
		"kb",
	)

	parents, err := loader.LoadParents(
		context.Background(),
		nil,
	)

	if err != nil {
		t.Fatal(err)
	}

	if len(parents) != 0 {
		t.Fatalf(
			"空输入应该返回空 map: %v",
			parents,
		)
	}

	if len(db.calls) != 0 {
		t.Fatal(
			"空 Parent ID 不应该查询数据库",
		)
	}
}

func parentRow(
	chunkID string,
	documentID string,
	content string,
) []any {
	return []any{
		chunkID,
		documentID,
		content,
		"# Parent",
		0,
		0,
		len([]rune(content)),
		nil,
		[]byte(`{"title":"Parent Document"}`),
	}
}
