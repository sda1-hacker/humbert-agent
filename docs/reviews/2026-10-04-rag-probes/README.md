# RAG 审查复现

这些程序只调用本地 RAG 算法，不连接数据库、模型供应商，也不改写业务数据。用于给审查结论提供可重复的证据，不替代正式回归测试或 PostgreSQL 集成测试。

在 `humbert-agent` 项目根目录执行：

```sh
go run ./docs/reviews/2026-10-04-rag-probes
```

本机审查时使用以下命令隔离构建缓存，并避免已有 GOROOT 环境变量与 Go toolchain 冲突：

```sh
env -u GOROOT GOCACHE=/private/tmp/humbert-review-gocache GOTMPDIR=/private/tmp/humbert-review-gotmp go run ./docs/reviews/2026-10-04-rag-probes
```

2026-10-04 工作区上的输出：

```text
long_paragraph: chunks=1 first_runes=2000 token_estimate=1176 rejected=[{legacy single chunk for large document}]
overlap_zero: normalized=80
default_strategy: selected=legacy
collapse_after_limit: unique_contexts=1 (second parent is sixth candidate)
topk_layers: requested=10 candidates=6 returned=5
table_parent_child: source_runes=900 parents=10 children=40 out_of_bounds=2
table_invalid_range: [872,901) content="| Key | Value |\n| --- | --- |\n| row-28 | unique-value-28 |\n"
table_invalid_range: [901,930) content="| Key | Value |\n| --- | --- |\n| row-29 | unique-value-29 |\n"
```

用例依次检查：长段落超过 TokenLimit、显式零 overlap 被默认值覆盖、空策略实际选择 legacy、最终截断早于 parent 去重、rerank 默认 TopK 提前限制结果、synthetic 表头导致 child source range 超出完整 Markdown。

`legacy` 默认策略可以是有意的兼容选择；本用例只确认其实际行为。TokenLimit 的估算值也不是特定模型 tokenizer 的精确计数；问题是它没有构成模型输入的硬边界。
