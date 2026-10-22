# RAG 上传与查询示例

两个独立入口共用 [example.go](example.go) 的 Config、CollectionID、FilePath 和 Question。用于验证真实文档解析、图片识别、Embedding、数据库发布与检索；示例本身不运行回答 Agent。

## 先核对配置

编辑 `example.go`：

- FilePath 改为待导入的本地普通文件；Question 改为待验证问题。
- Database 改为自己的测试数据库连接与已有角色。示例账号只供本地实验，初始化不会创建角色。
- Embedding.Provider、BaseURL、Model、Dimensions 必须与实际服务匹配。当前固定示例选 Ollama 和 128 维，这不是 RAG 后端的固定要求。
- `RecognizeImages` 当前为 true，`provider=agent` 会读取 Humbert 的应用模型配置与系统凭据库。没有图片模型时，可先关闭图片识别验证正文；纯图片/扫描件则无法仅靠文件保存完成文本索引。
- 调整 Chunking、Hybrid、Search 和可选 Rerank 参数时，区分需要重新上传的索引参数与仅查询生效的参数。

需要 PostgreSQL 14+、pgvector 0.6.2+、pg_search 0.20.10+ 和相应模型服务。服务与扩展文件需预先安装；数据库不存在时，上传入口可以使用已有角色创建它。输入及图片可能发送到显式配置的模型服务。

## 运行

从仓库根目录执行：

```bash
go run ./examples/5-rag/upload
go run ./examples/5-rag/query
```

`examples/5-rag` 是共享 Go package，没有 main；不再使用 `go run ./examples/5-rag 文件路径`。这两个入口不读取 `rag.example.yaml`、文件位置参数或旧的 `RAG_DATABASE_URL`/`RAG_EMBEDDING_MODEL` 变量；实际参数以 `Config()` 为准。

上传依次建库/检查扩展/迁移、保存原件、解析、父子切分、生成真实向量并发布，再从数据库回读文档状态和分块。同路径重传沿用文档业务 ID并产生新版本，不同路径分配不同身份。示例文件、图片和缓存位于进程工作目录下 `.rag-example/`。

查询入口只连接并检查已有 schema、维度和模型档案，不自动初始化。它分别执行 semantic、keyword、hybrid，打印证据、父块上下文、原始通道分数、精排状态及耗时。Ctrl+C 取消模型、解析和 SQL，外层总时限为 10 分钟。

## 评测

[query/main.go](query/main.go) 的 references 默认空。按问题填写来源文件、已发布 ContentHash 和归一化处理文本的 rune 范围，再运行查询。程序从完整已发布分块建立相关集合，不能用当前命中结果反推分母。

- P@K = 命中的相关子块数 / K；不足 K 条按未命中计算。
- R@K = 命中的相关子块数 / 全部标注的相关子块数。
- RR@K 是首条相关结果名次的倒数；多问题取平均才叫 MRR。
- nDCG@K 使用二元相关性及对数排名折扣。

评测要求保留独立子块排名，当前示例关闭 CollapseSameParent。没有有效标注时仍显示结果，不输出假质量分数；正文或解析规则改变后需重新核对 ContentHash 与范围。模型回答的正确性和忠实度不在此示例中评估。

## 单独迁移

迁移 CLI 使用另一条配置入口：

```bash
go run ./cmd/rag-migrate -config rag.example.yaml
go run ./cmd/rag-migrate -config rag.example.yaml -documents-only
```

YAML 由 `rag/config.Load` 读取，可通过 `HUMBERT_RAG_*` 覆盖；documents-only 只初始化权威文档库。CLI 不会自动读取 example.go 的固定数据库参数，运行前须核对目标。桌面知识库则由设置页管理，不直接读取这两份示例配置。

完整流程及验证边界见 [RAG 源码详解](../../docs/RAG源码详解.md)。
