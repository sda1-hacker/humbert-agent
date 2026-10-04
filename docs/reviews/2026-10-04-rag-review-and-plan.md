# RAG 代码审查与接入计划

日期：2026-10-04。结论：现有代码值得保留，已经具备较完整的解析、分块与检索基础；接下来应先修复正确性问题，再补齐文档生命周期，最后通过现有模块接口接入 Agent。当前缺口主要是知识库业务与可验证的端到端闭环，继续增加检索算法不是最高优先级。

## 审查范围与验证边界

阅读了 `internal/rag` 的全部 37 个 Go 实现文件，覆盖 composition root、application、loader、chunker、transformer、searchcontent、embedding、indexer、retriever、RRF、rerank 与 search pipeline。该目录另有 33 个测试文件、253 个以 Test 开头的测试函数；阅读了相关用例并运行整个 RAG 测试集，没有将“运行全部测试”表述为“逐行审计全部测试”。

为规划接入，另外阅读了 component、app 模块装配与生命周期、Runtime 能力装配、Agent Profile、模型配置、Tool Scope/Guard/Reduction、文档提取、现有全文搜索、Wails 服务注册与消息 DTO，以及相关前端 API。这里没有重新审计整个非 RAG 项目的全部实现。

Humbert 的 Git HEAD 为 `3d01afdf687830b91ce47c6375dac09a438f2c2e`，实际审查对象是本次工作区，包括已有未提交修改。没有修改这些业务文件。

WeKnora 对照使用本地 checkout 的 `9114e4e4f905be71976a77c684d3f92731e6f9a6`（2026-09-27）。没有把 GitHub main 的滚动变化当作固定基线。参考了它的知识库模型、导入配置、过期解析任务处理、知识检索工具、上下文组装与原文定位。

验证结果：

- `go test -count=1 ./internal/rag/...`：13 个包全部通过。初次执行的两个 HTTP 测试因沙箱禁止监听本机端口而失败，获得放行后重跑通过。
- `go vet ./internal/rag/...`：通过。
- 新增[无外部依赖的复现程序](/Users/sda1_hacker/Desktop/humbert/humbert-agent/docs/reviews/2026-10-04-rag-probes/main.go)，验证了长段落、配置默认值、TopK 与父块去重、表格父子块偏移问题。
- 没有运行真实 PostgreSQL/pgvector/pg_search 集成测试、真实供应商 embedding/rerank 请求或 OCR 构建。数据库相关单元测试通过 fake transaction/queryer 检验行为，不能证明真实 DDL、扩展版本、执行计划或中文召回质量。

本报告中，“已复现”表示执行了最小用例；“代码确认”表示能从实现直接确定；“需集成验证”表示仍需要真实后端或评测集。

## 已有能力与应保留的设计

| 环节 | 当前实现 | 判断 |
|---|---|---|
| 解析 | Tabula，本地 PDF/DOCX/ODT/XLSX/PPTX/HTML/EPUB | 能作为一种 parser adapter，格式与资源限制需要补齐 |
| 分块 | recursive、heading、heuristic、auto；结构保护、overlap、表头恢复、父子块 | 功能较丰富，先修复 source mapping 和输入预算 |
| Eino 适配 | Loader、Transformer、Indexer、Retriever 契约 | 可以复用，但两条入库路径的语义有差异 |
| 入库 | documents/chunks/retrieval_index；父块不向量化；完整 Markdown 保存 | 主 application 路径正确，缺业务身份、版本和任务状态 |
| 检索 | pgvector halfvec(1024)、HNSW、ParadeDB BM25、并行两路召回 | 第一版足够，后端可运行性与质量尚未实测 |
| 融合 | Weighted RRF，原始通道分数与排名保留 | 排序信息保留较好，融合分不能当回答置信度 |
| 重排 | 独立 Scorer、HTTP provider、错误诊断、阈值与 fallback、MMR | 分层合适，默认权重和策略需要评测 |
| 上下文扩展 | child hit 与 parent content 分开，批量加载父块 | 原则正确，最终选择顺序和引用元数据需要调整 |
| 入口 | NewRAG / Ingest / Search / Close | 算法 façade 已有，尚未形成知识库产品 |

值得保留的具体设计：

1. chunker 不依赖数据库、Tabula 或 Eino，适合独立回归和评测。
2. SearchContent 与权威正文分开。标题、breadcrumb 用于检索，不污染完整 Markdown。
3. embedding 调用在事务外，完整文档替换在事务内。模型失败不会先删除旧索引。
4. 同一运行时的入库与查询共享 embedder，避免当次装配中的模型错配。
5. parent 扩展不覆盖命中 child 的 Content，给引用与评测保留了正确的概念边界。
6. rerank 错误及全部低于阈值有诊断，便于后续显示降级原因。

## 优先修复的问题

这里的 P1 表示应在正式接入前解决，P2 表示应在第一版完善时处理；它们是本次接入的工作优先级。

### P1：表格父子分块的原文偏移会错位，已经复现

证据：[parent_child.go:227](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/parent_child.go:227) 对 parent.Content 再分块，[parent_child.go:305](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/parent_child.go:305) 直接把局部偏移加 parent.Start。父块 Content 可能已经包含自动补入的表头，它不再是原文区间的等长拷贝。

复现：30 行表格、parent size=160、child size=80；原文共 900 rune，生成的最后两个 child 区间为 [872,901) 与 [901,930)。这不仅是“Content 长度与区间长度不同”的合法 synthetic header 例外，而是子块坐标超出了原文边界。当前 ingestion 校验只检查非负与起止顺序，因此这些错误坐标仍可落库。

影响：引用高亮错位；按坐标切原文可能越界；父块内部已有 synthetic 内容时，后续真实行的位置会累计偏移。

建议：

- 分离 canonical source text 与用于展示/检索的 decorated content。
- 短期让 child 在原文 parent 区间上分块，再单独附加表头；或者保留 segment mapping，显式映射 synthetic 与 source 段。
- 校验所有真实 source range 都处于完整 Markdown 范围内，且实际正文片段能映射回源文本。
- 增加重复表头、表头加 overlap、中文/emoji、跨父块表格的回归用例。

### P1：TokenLimit 没有形成 embedding 输入硬约束，已经复现

证据：[strategy.go:231](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/strategy.go:231) 把近似 token 预算换成目标字符数；[merge.go:74](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/merge.go:74) 只对超过 7500 rune 的单元执行强制拆分；[strategy.go:515](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/chunker/strategy.go:515) 最后一层验证失败后仍返回结果。

复现：2000 个连续中文字符，ChunkSize=100、TokenLimit=32、Languages=zh，仍返回一个 2000 rune 的 chunk；现有估算函数给出 1176 token，诊断记录 rejected legacy，但 application 继续入库。

ChunkSize 被设计成目标值，可以允许保护块超过它；问题在于模型输入上限需要独立约束。当前也没有把 title、ContextHeader、重复表头加入最终 embedding 输入预算，按 64 条固定批量处理还可能超过供应商每批总 token 上限。

建议把“语义分块目标”与“模型请求上限”分开。在 SearchContent 构造完成后再次校验 provider 的单条与单批限制，支持 tokenizer 或保守预算，必要时按行/句拆分；无法拆分时返回明确的文档处理错误。避免通过静默截断改变权威正文和引用。

### P1：最终 TopK 与父块去重的顺序不合理，已经复现

证据：[pipeline.go:176](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/search/pipeline.go:176) 先截断 child 结果，再加载/去重 parent；[reranker.go:150](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rerank/reranker.go:150) 还有独立 TopK=5。Hybrid.TopK、Rerank.TopK、Search.FinalTopK 都能提前限制候选。

复现一：前五个 child 都来自父块 A，第六个来自 B；FinalTopK=5、CollapseSameParent=true，最终只得到 A 一个上下文。复现二：六个候选、Search.FinalTopK=10、默认无模型 rerank engine，仍只返回五条。

建议明确区分 RecallK、RerankCandidateK、最终上下文数量和 token budget。重排保留足够候选，按 parent/context 分组后做最终多样性选择，并从后续候选补齐。保留同一 parent 下所有被采用的 child 证据。当前 MMR 使用 child 文本的 token-set Jaccard，也不等于 parent 上下文去重；在 parent 分组之后再选上下文更符合最终输入目标。

### P1：模型一致性只覆盖当前进程，没有持久化 embedding profile

证据：[rag.go:234](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/rag.go:234) 与查询共享同一个 embedder，但 [schema.go:145](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/schema.go:145) 的索引表没有模型身份、输入格式版本或索引 generation。

触发场景：第一次用模型 A 入库，关闭应用，配置改为同样输出 1024 维的模型 B，再启动。维度校验全部通过，旧向量仍来自 A，查询向量却来自 B。共享 instance 无法防止这个跨启动问题。

建议每个 collection 绑定不可变 embedding profile，至少记录 provider/model/revision、dimension、输入拼装版本与必要预处理参数。影响语义空间的配置改变必须启动新 generation 并重建索引；新 generation 完成后切换，查询按已发布 generation 解析模型。API Key 轮换通常不应单独触发重新 embedding，凭证与语义 profile 需要区分。

1024 维本身不是错误。第一版可以继续限定一个经过验证的 profile；首先建立模型身份和重建机制，再考虑支持多维度。

### P1：缺少业务 DocumentID、版本与旧任务写入保护

证据：[loader.go:670](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/loader/tabula/loader.go:670) 用原始 URI 的 hash 作为 ID；[service.go:259](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/application/service.go:259) 直接使用 loader ID；[ingestion.go:140](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/ingestion.go:140) 无条件覆盖当前文档。

代码确认：

- 路径 hash 能让同 URI 重跑保持身份，但临时上传路径变化、文件重命名、相对/绝对路径别名会改变身份。
- 未保存源文件内容 hash、文档 revision、parser/chunker 配置版本；同文档重跑会重新调用全部 embedding。
- 同文档两次导入若先后完成顺序相反，旧内容仍可能覆盖新内容。事务保证一次替换完整，不能保证“用户最后提交的版本获胜”。
- 同步 Ingest 的接口没有任务状态、失败恢复或删除 tombstone；未来添加 goroutine 后这些问题会直接暴露。

建议由知识库业务层分配稳定 DocumentID，源路径仅是来源属性。保存 content hash、document revision、effective process config、embedding profile/generation；提交索引时以 revision/attempt 做条件检查。删除和取消也必须使旧 attempt 失效。用 hash+处理配置摘要做重复导入判定及 embedding 缓存，不能只用内容 hash 替代业务身份。

### P1：解析没有可中断和受控资源边界

证据：[loader.go:401](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/loader/tabula/loader.go:401) 同步调用不接受 context 的 ToMarkdown，只在完成后检查取消；文件验证基于 os.Stat、扩展名，未建立输入大小、ZIP 展开规模、输出上限与普通文件限制。

这是独立模块接入时需要处理的边界，当前没有对外暴露 RAG 导入入口，因此不能据此宣称现有桌面入口已经可以被利用。

项目已有[documenttext/extract.go:83](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/documenttext/extract.go:83) 的一次性 worker、超时、格式签名和容器规模检查，可复用其解析执行机制。但是该模块服务聊天附件，格式范围、512 KiB 输出上限和返回协议未必适合知识库；需要共享 ParserResult/执行器，让附件与 RAG 保留自己的大小策略，并保留 warnings、OCR 状态和 source locators。

导入已授权工作区文件时应通过已有 Sandbox 文件边界打开，再拷贝到模块自有 source 存储；工具不应把模型提供的任意绝对路径直接交给 Loader。

### P2：混合检索任一路失败会使整体失败

证据：[retriever.go:758](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/retriever/postgres/retriever.go:758)。两路虽然并发，仍等待全部完成后，只要 vector 或 keyword 返回错误就结束。FuseRRF 支持“某路没有结果”，不等于支持“某路故障”。父块读取失败也会丢掉已经获取的 child 结果。

建议把策略显式配置成 strict 或允许单路降级，返回 channel diagnostics、mode_used 和 degraded。取消必须继续传播；单路超时可使用另一条已经成功的路径；两路都失败必须返回失败，不能返回“知识库无答案”。父块读取的可恢复故障可以返回 child，并附 parent expansion 诊断。为数据库和整个搜索请求设置 deadline，避免 wg.Wait 等待无期限的底层操作。

需要同时开放 keyword/semantic/hybrid 请求模式。当前核心已有单独 retriever，但 application.SearchRequest 只有 CollectionID 与 Query，外层无法使用这些能力。

### P2：两条入库路径不具有相同保证

主路径 Service.Ingest → ReplaceDocument 会保存完整 Markdown，先 parent 后 child，且 parent 不进入 retrieval_index。

Eino Transformer → Indexer.Store 是另一条路径：它没有完整源文档输入，upsert SQL 不写 markdown；Store 对所有输入 chunk 建 retrieval index；按 document 的 chunk_index 去重，没有区分 parent 与 child 的序号空间；缺少 chunk type 时使用 `chunk`，主路径使用 `text`。

证据：[indexer.go:258](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:258)、[indexer.go:937](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/indexer.go:937)。

Store 的“完整文档替换”是代码明确声明的契约，不是发现了它违背自身契约的 bug。但调用者如果按 chunk 分批 Store，会不断删除前一批；仅凭 Eino Indexer 接口形状不容易意识到这个限制。

建议正式业务统一走 document envelope → ReplaceDocument。保留 Eino adapter 时明确其 flat-only/整文档调用边界，并尽量委托同一套持久化逻辑；不要让两套 SQL 和 metadata 常量继续各自演进。上下文标题应从 ChunkRecord.ContextHeader 等显式字段构造，减少结构化字段和 metadata 双写后发生不一致的可能。

### P2：配置默认值和有效配置的语义需要统一

已复现：

- ChunkOverlap=0 经 NormalizeSplitterConfig 变为 80，调用者无法以普通配置明确关闭 overlap；SplitText 对 0 的处理却不同。
- DefaultConfig.Strategy 为空，实际选择 legacy；auto、heading、heuristic 已实现，但默认没有启用。这可以作为兼容策略保留，不过应在知识库 UI 中显式显示有效策略。

另外：Hybrid 的 0 阈值会被替换成默认值，RRF 权重 0 会被替换成默认权重；Config.Validate 主要检查 DSN、embedding 与 rerank provider，未完整校验算法配置。部分浮点参数可能接受 NaN、越界权重或不一致的 dimension 配置，错误在运行时才暴露。

建议只保留一套有效配置解析器；用 optional 字段区分“未设置”和显式 0。知识库默认值 → 本次导入覆盖 → effective process config，后者保存到文档 revision。启动/保存配置时完成 finite、范围、TopK、预算及维度交叉校验；不要静默用默认值掩盖无效的显式输入。

### P2：数据库部署与 schema 演进尚未闭环

证据：[schema.go:255](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/rag/indexer/postgres/schema.go:255) 按语句逐一执行 CREATE IF NOT EXISTS；它不能给既有表自动补新增列，也没有 schema version、迁移锁与版本兼容校验。默认启动时自动创建扩展/索引，对桌面应用还会引入启动耗时和数据库高权限需求。

官方文档表明 `USING paradedb` 这个 access method 名称从 pg_search 0.25.0 加入；因此当前 SQL 有明确版本前提，不能只要求“装了 pg_search”。当前 DDL 不显式设置文本 tokenizer，官方默认是 unicode，可对中文、英文标识符等实际语料比较 ICU/ngram。见[ParadeDB 创建索引文档](https://www.paradedb.com/docs/reference/indexing/create-index)。

HNSW 索引是跨 collection 的全局索引，查询附加 collection/enabled 过滤。pgvector 文档说明近似索引过滤可能减少实际返回数量，可通过 iterative scans、参数调节、分区或小范围精确检索处理。这里应先通过真实数据的 EXPLAIN 和召回对照确认瓶颈，再选策略。见[pgvector Filtering](https://github.com/pgvector/pgvector#filtering)。

还有一处约束需要修正/集成验证：parent 外键包含 collection_id，ON DELETE SET NULL 未指定仅置空 parent_chunk_id；单独删除被引用 parent 时可能将 NOT NULL 的 collection_id 一起置空。当前整文档删除路径与未来单独编辑/删除 parent 是不同场景，应该分别测试。

建议版本化 migrations、固定经过验证的扩展版本、独立迁移入口、运行账号最小必要权限与 health check。当前固定 1024 维和 halfvec 可以保留，补充非零向量校验与真实精度/召回验证。connection pool 的容量、连接/查询/关闭超时也需要明确。

## 仍缺少的业务能力

### 知识库与文档管理

当前 CollectionID 是一个隔离字段，没有 collections 实体与 CRUD。至少要有 collection 的名称、描述、启停状态、处理默认配置和 embedding profile；document 的稳定身份、源文件引用、hash、revision、处理状态、错误与发布时间；Agent 对知识库的显式绑定。

首版接口建议：

- Create/List/Update/DeleteCollection，修改 embedding profile 走 Rebuild，而不是直接覆盖。
- ImportDocument、ListDocuments、GetDocument、ReadDocument、ReparseDocument、SetDocumentEnabled、DeleteDocument。
- GetIngestionJob、CancelIngestionJob、RetryIngestionJob。
- Get/SetAgentKnowledgeBindings。
- SearchKnowledge：支持模式、集合/文档过滤、limit 和诊断。

“删除文档”“禁用文档”“取消导入”“重新解析”必须是不同业务动作。集合删除还需要清理绑定、任务、source 文件与索引；Agent 删除应清理绑定，而共享知识库的所有权由独立策略决定。

### 基础文本格式

Loader 的 supportedExtensions 没有 .md/.txt/.csv。这对开发者 Agent 的知识库是直接缺口。优先补 Text/Markdown/CSV loader，统一 UTF-8/BOM/换行处理；代码知识库可以先支持限定文本类型，后续再做 AST-aware chunking。

URL 导入可以复用已有受控 web fetch 能力，但应作为独立 source adapter，保留最终 URL、抓取时间与快照。它可以晚于本地文件 MVP。

### 可恢复的导入任务

同步 Ingest 适合作为 worker 内部步骤；用户操作应快速返回 JobID，通过事件或查询获取进度。

建议状态为 queued → parsing → chunking → embedding → committing → ready，另有 failed/canceled。后台 worker 数量有上限；任务保存 lease/attempt、重试次数、阶段进度与配置快照。应用重启后恢复/重新领取未完成任务。

已在使用 PostgreSQL 时，可以先在同一个数据库持久化 jobs，用受限 worker 池和数据库领取机制实现，不必为了首版任务队列增加 Redis。不要复用 `internal/tasks` 的用户定时 Agent 任务作为解析任务：两者的所有权、重试及进度语义不同。

提交新的 generation 前保留旧版本可检索，failed 也应明确“旧版本仍可用”。删除、取消、重试必须以 revision/attempt 防止迟到的 worker 恢复已失效内容。

### 上下文预算与引用闭环

SearchResult 已区分 hit 与 context，但是还缺 collection、document revision、generation、独立 context range/header、原始格式 locator。当前 rune offset 只对应转换后的 Markdown，不代表 PDF 页码、PPT 页或 Excel 单元格。

建议分成 SearchHit、ContextBlock、Citation/SourceRef：

- SearchHit 保留 collection/document/revision/chunk、child range、原始通道与重排分。
- ContextBlock 保存实际注入的 parent/window、对应 source range、header，以及所有贡献的 hit。
- Citation 保存消息/工具调用中的稳定编号、文档版本、定位信息与实际采用的摘录。

多知识库合并时以 collection+document revision+chunk 为身份，不能直接按当前 ChunkID 融合；当前结果没有 CollectionID，未来直接混合不同 collection 会有冲突风险。

ContextBuilder 应在实际模型窗口预算内，对 parent 分组、去除 overlap 重复、按优先级选段，并保留结构化引用。若模型 tokenizer 不可用，用现有 ContextEngine 的保守估算与安全余量。引用登记应发生在最终预算裁剪之后；模型只能引用本轮实际提供的编号。Tool Reduction 可能截断/归档长结果，因此引用不能依赖一段随时被截断的 JSON 正文。

原文和引用在文档更新后的行为需要明确：历史回答保留引用 revision/摘录，预览可展示对应版本；若旧原件已清理，则标明原件不可用，不能把编号重新指向新文本。Go rune 与 JavaScript UTF-16 索引也需要统一转换，尤其是 emoji。

RRF、BM25 与 composite score 是排序信号，不应直接显示为“答案准确率”。rerank 失败、fallback_top1、无匹配必须有明确回答策略：可以继续普通聊天，但不得声称获得了知识库证据。

### 权限、配置与运行状态

桌面单用户产品首先需要 Agent/Session/知识库范围约束，暂时不需要复制企业多租户 RBAC。模型传入的 collection/document ID 只能缩小已绑定范围。已导入的本地文档应成为模块拥有的快照，源文件移走之后是否继续可用由明确的导入语义决定。

子 Agent 特别要处理两个身份：component.Request.AgentID 用于读取子 Agent 的绑定；Scope 保留父运行的授权身份。子 Agent 知识范围应受父任务允许范围约束，不能仅凭自己的绑定扩大访问。撤销访问后即使 Turn 曾冻结配置，也应在敏感读取时执行撤销检查。

现有 models.Registry 返回 ChatModel，不能把 embedding/rerank 伪装成聊天模型。先复用 Provider 配置概念与 credential.Store，新增类型明确的 embedding/rerank profile 和 factory；后续多个模块都需要时再抽出共用配置层。Key/DSN 密码不进入 UI DTO、Agent instruction 或 Module Revision 明文。

应用需要展示 RAG 的配置/连接/扩展/模型准备状态，且未配置 RAG 的普通聊天仍可启动。若已绑定的知识能力暂时不可用，应明确报出，不静默当作空知识库。启动 ContextOverview/Describe 不应连接数据库、执行 DDL 或调用模型。

当前应用备份会打包本地数据目录，但外部 PostgreSQL 不会因此被备份。知识库 source/config 与数据库事实应提供一致的导出/恢复协议，或者明确提供从 source 重建索引的恢复流程；不能把完整 Markdown、源文件和任务事实误放入可丢弃 cache。

## 应如何借鉴 WeKnora

最值得借鉴的是文档与索引的生命周期、有效处理配置、搜索范围、来源定位和工具返回的可读预算。WeKnora 的当前检索工具能搜索、按命中继续读文档、列文档，适合 Humbert 的 Agent 场景。参考[固定版本 search_knowledge](https://github.com/Tencent/WeKnora/blob/9114e4e4f905be71976a77c684d3f92731e6f9a6/internal/agent/tools/search_knowledge.go)。

它的 RAG QA 管线还包括 query understanding、merge、context assembly 与生成；Humbert 当前 Search 只完成检索结果返回，后面的回答与引用仍需接入。参考[固定版本 RAG 管线](https://github.com/Tencent/WeKnora/blob/9114e4e4f905be71976a77c684d3f92731e6f9a6/website-docs/02-architecture/04-rag-pipeline.md)。

原文定位不是简单保留 chunk offset：WeKnora 有独立 source blocks、格式 locator 和转换后 remapping。可以借鉴这一数据契约，先实现可靠 Markdown 引用，再逐格式扩展。参考[固定版本来源定位](https://github.com/Tencent/WeKnora/blob/9114e4e4f905be71976a77c684d3f92731e6f9a6/internal/application/service/knowledge_source_locators.go)。

Humbert 已有 Eino Runtime、模块 Provider、Wails Event、权限和 ContextEngine。第一版使用这些既有边界，业务规模达到需要时再引入额外的事件插件编排、分布式队列、GraphRAG、Wiki 或多数据源同步。

## 建议的接入架构

规划以“首版继续使用已有 PostgreSQL 检索后端、RAG 可选启用”为基线。这样可以先验证现有实现。若产品要求安装后完全离线、无需任何外部数据库，则需要另做存储方案验证；应在后端适配处实现，先测桌面资源占用与召回效果，再决定是否替换当前数据库。

建议保留 `internal/rag` 为算法和存储适配层，新增小型 `internal/knowledge` 模块，拥有知识库、文档、source、job、profile 与 Agent bindings。无需第一版就创建多种存储后端或通用框架。

~~~mermaid
flowchart TD
    UI[Vue 知识库界面] --> WS[KnowledgeService / Wails]
    WS --> K[Knowledge 模块：文档、配置、范围、任务]
    K --> JOB[持久化 Job + 受限 Worker]
    JOB --> PARSE[受控 Parser + Source Snapshot]
    PARSE --> ING[RAG Ingest / 版本化提交]
    ING --> DB[(PostgreSQL)]
    RT[Eino Runtime] --> CP[component.Provider]
    CP --> TOOL[rag_search_knowledge / rag_read_document / rag_list_documents]
    TOOL --> K
    K --> SEARCH[RAG Recall / RRF / Rerank]
    SEARCH --> CB[ContextBuilder + 引用登记]
    CB --> RT
~~~

接入位置已经存在：

1. [component/module.go:17](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/component/module.go:17) 定义 Module/Provider、Host 资源、工具贡献与冻结版本。
2. [app/modules.go:20](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/app/modules.go:20) 的 WithModules 接收 Installer；生命周期已有 Start/Stop/Close。
3. [runtime/extensions.go:82](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/runtime/extensions.go:82) 注册能力来源，Describe/Resolve 的工具进入现有 Guard 和 Schema 预算。
4. [services/enter.go:11](/Users/sda1_hacker/Desktop/humbert/humbert-agent/internal/services/enter.go:11) 的 All 支持追加 Wails Service。
5. 桌面入口的 [Bootstrap 调用](/Users/sda1_hacker/Desktop/humbert/humbert-agent/cmd/desktop/main.go:88) 与 [services.All 调用](/Users/sda1_hacker/Desktop/humbert/humbert-agent/cmd/desktop/main.go:158) 分别是传入模块和桌面服务的位置。

模块 ID 建议 `rag`，工具名采用 `rag_search_knowledge`、`rag_read_document`、`rag_list_documents`，满足现有扩展器要求的 `id_` 前缀。工具声明 RiskRead，业务层仍执行知识范围检查。第一版将绑定保存在知识模块，Agent Profile 不必包含全部 RAG 配置。

Selection 读取显式 Agent 绑定；Describe 返回纯本地 Schema；Resolve 冻结绑定/profile/config revision，构建 Eino InvokableTool。宿主提供权限、预算、事件、结果归档与执行。Provider 不反查 Application，业务依赖由构造函数注入。

关闭顺序：Stop 禁止新导入并取消/等待 worker，宿主等待已有 Agent Turn，最后 Close 释放 RAG pool。Close 需要有明确的等待边界；如果仍有任务使用连接，不应提前销毁 pool。

现有 `internal/searchindex` 是会话/工作区的 SQLite 搜索投影，继续服务相应 UI；知识库由 knowledge 模块管理。两种搜索的源数据所有权与更新语义不同，可以共用 UI 入口，后端协议仍应明确。

首版先提供 Agent 工具路径；“每轮自动检索再注入”作为后续明确 QA 模式。工具路径更容易复用现有运行链，支持模型按需搜索和继续读取。自动注入需要额外的 query rewrite、预算、引用与生成控制，等基础闭环有质量数据后再加。

## 分阶段实施与验收

| 阶段 | 具体交付 | 验收标准 |
|---|---|---|
| 1. 修复核心正确性 | source mapping、embedding 单条/单批预算、TopK 顺序、配置解析；统一正式入库路径 | 本报告复现有对应回归；所有合法源坐标可定位；显式 0 配置有效；有足够候选时父块去重能补齐 |
| 2. 固定真实后端基线 | versioned migrations、固定扩展版本、health check、独立测试数据库、真实 SQL 测试 | 新库/旧库升级成功；平面/父子入库、更新、删除、回滚、两 collection 隔离、BM25/dense/hybrid 均实测通过 |
| 3. 完成知识业务闭环 | collection/document/profile/revision、source 存储、文本 loader、job 状态/worker、Agent bindings | 导入→搜索→读原文→更新→取消/重试→删除完成；旧 attempt 不覆盖新版本；重启恢复；更换模型需显式重建 |
| 4. 接入 Eino Agent | component.Installer/Provider，三个只读工具，ContextBuilder、引用登记与运行诊断 | 未绑定不暴露工具；绑定后能搜索并续读；模型传 ID 不越范围；子 Agent 受父授权上限约束；结果纳入现有预算/归档 |
| 5. 完成 Wails/Vue | KnowledgeService、knowledge API/store、知识库管理、导入进度、Agent 选择、搜索调试、引用预览 | 新建库→导入→看到 ready→绑定 Agent→回答带可打开引用；重启后历史引用仍能解释；无匹配与故障可区分 |
| 6. 建立质量评测 | 冻结语料与标注问题集，通道/分块/重排消融，质量与耗时报告 | 每次参数调整能比较 Recall/MRR/nDCG、引用质量与成本；明确未找到答案的判定策略 |
| 7. 按评测补增强能力 | 多库全局候选排序、query rewrite、邻块窗口、URL source、OCR；后续图片/VLM/FAQ 等 | 每项新增能力有独立收益与回归证据，满足相应 source/citation 契约 |

阶段 6 的小型评测集应从阶段 2 就开始建立，不必等 UI 完成；表格阶段顺序表示主要交付依赖。阶段 3 的后台导入是桌面基础能力，不宜推迟到图像、GraphRAG 之后。

第一轮实现应限制为阶段 1 与阶段 2，避免同时更改 Agent 运行链与检索算法，便于定位失败。

真实集成测试重点：

- schema 重复启动、旧表升级、缺扩展/版本不兼容的明确错误。
- 完整 Markdown 与 source range 一致；parent 不进入 retrieval_index。
- 重解析后旧 chunk 不残留；embedding 或 commit 失败时旧版本完整可查。
- 同文档并发更新、取消后重试、删除后旧 worker 完成，验证 revision/attempt fence。
- 同 ID 在不同 collection 不串库；后续多库合并不会按 ChunkID 误去重。
- 单独删除 parent 的外键行为；禁用文档后的检索和阅读范围一致。
- 中文自然语言、标识符/错误码、长表格、长代码、无分隔符文本。
- 大 collection 的 ANN 查询在 collection/enabled 过滤下对照精确搜索，记录实际返回数和 Recall。
- 429/5xx/timeout/canceled 下的 embedding/rerank/单通道降级。

评测集建议先收集约 50–100 个真实问题，覆盖中文、英文、中英混合、表格、代码/错误码、多轮指代和库中不存在的答案。数量只是启动规模，随着失败样本持续扩充。标注 relevant document、chunk/source range、参考答案或无答案标签，并冻结文档版本。

检索指标采用 document 与 chunk 两个层次的 Recall@K、MRR/nDCG；回答指标关注证据覆盖、引用是否支持结论、无答案误答率；运行指标包括 P50/P95 查询耗时、每文档解析/embedding 时间、输入 token、模型费用与任务恢复情况。上线门槛在首轮测量后约定，当前没有数据支持给出一个声称已达标的准确率。

优先比较 flat 与 parent-child、legacy 与 auto、dense/BM25/hybrid、不同中文 tokenizer、rerank 前后、MMR 与 parent grouping、候选数和预算。现有 0.7/0.3 RRF、0.6/0.3/0.1 composite、0.3 rerank threshold 是起点，不能通过参考项目默认值证明适合本项目语料。

建议近期目标是：一份真实文档能够受控导入，更新与删除行为正确，一个绑定 Agent 可以检索、续读并返回可打开的稳定引用；有最小评测集证明效果。完成这个闭环之后，再扩展更多 RAG 能力。
