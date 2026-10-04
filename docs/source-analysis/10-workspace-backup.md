# 10 工作区、搜索、备份与跨域用例

## 工作区归属与路径

[workspace/types.go](../../internal/workspace/types.go)、[manager.go](../../internal/workspace/manager.go)、[path.go](../../internal/workspace/path.go) 定义 managed/custom 两种工作区。managed 路径由应用工作区根目录和 Agent ID 派生；custom 指向用户选择的既有目录。Agent 删除可以清理 managed 数据，不能因删除配置而递归删除用户 custom 项目。

Manager 校验模式、Agent UUID、自定义目录有效性和实际路径，`Resolve` 产出标准化 Workspace。`OpenRoot` 返回限制在指定根内的 `os.Root`，供目录浏览、文件读取及相关工具使用。相对路径规范化拒绝越界路径；实际文件访问不能仅依靠字符串前缀判断安全。

工作区目录与权限规则不是同一件事。工作区提供默认路径和归属；工具访问仍需要 PathGuard，命令还经过 Runner/NativeSandbox。UI 浏览服务主要提供读能力，并不等价于把用户浏览的所有路径授予模型修改权限。

## 文件树、预览和统计

[workspace/browser.go](../../internal/workspace/browser.go) 实现 `ListDirectory` 和文件预览。目录默认最多 500 个结果，调用上限 2,000，输出 Truncated。结果包含相对路径、类型、大小、时间和隐藏标志，供前端排序和导航。

文本预览最多 512 KiB，验证 UTF-8 并区分二进制；图片预览上限 8 MiB，通过 MIME 和 data URL 交给前端。不能预览的文件返回元数据及相应类型，不能假定所有文件都会转成文本。路径和文件类型验证使用实际根目录访问，符号链接有单独处理。

[workspaceview/service.go](../../internal/workspaceview/service.go) 结合 Agent 配置与 Workspace Manager，提供概览、列表和预览。概览扫描最多 5,000 个普通文件，跳过符号链接及部分生成目录；带扫描时间、总字节、目录/文件数量和 StatsTruncated。它是有界扫描结果，截断时不能把数量当整个仓库的精确统计。

## 会话全文搜索

[searchindex/service.go](../../internal/searchindex/service.go) 创建会话与文档两个独立 Controller，数据库分别位于 cache 下的 `conversation-search.sqlite`、`document-search.sqlite`。它们是可重建投影，删除缓存不会删除会话事实。

[index.go](../../internal/searchindex/index.go) 使用 modernc SQLite、WAL、两条最大数据库连接和 busy timeout，创建普通元数据表与 FTS5 trigram 虚拟表。搜索词通过 SQL 参数和 FTS 表达式处理，不直接拼接任意 SQL。较短查询使用转义 LIKE 路径，较长查询使用 trigram MATCH。Service 拒绝超过 200 字的词，返回最多 50 个结果。

[sessions.go](../../internal/searchindex/sessions.go) 遍历 Agent 与 Session，按 Session UpdatedAt revision 决定是否重建该会话索引；标题、归档等变化可只更新元数据。索引读取活动分支，收集 user/assistant 的 text 和 file ExtractedText，不把 thinking、工具参数和任意 sidecar 全部塞进搜索结果。删除的 Session 通过 Prune 清除索引。

结果保留 Session ID、Agent ID、Entry ID、时间、角色和 snippet。前端点击结果时用 Entry ID 请求消息窗口，而不是只把会话打开后让用户在数千条消息中自己查找。

## 文档搜索与后台刷新

[document_scan.go](../../internal/searchindex/document_scan.go) 对某 Agent 当前工作区提取 PDF/docx/xlsx/pptx 文档，调用 documenttext 的同一提取链路；普通源代码文件的匹配由文件搜索工具承担，不属于这个文档索引。

扫描跳过 `.git`、IDE、node_modules、vendor 和常见构建目录及符号链接，最多遍历 5,000 个文件、处理 1,000 个文档。使用 root/path/size/mtime 判断是否需要重提取。空文件、超 12 MiB 或提取失败的文档可以写入空文本 revision，避免每次都重复失败。只有未截断扫描才按 seen 集合清除旧文档，避免将本轮未遍历到的文件误删。

[documents.go](../../internal/searchindex/documents.go) 将提取文本拆成可搜索行，返回文件路径、行号与片段。Agent/root 联合过滤避免混用已经切换的工作区索引。

[controller.go](../../internal/searchindex/controller.go) 延迟打开数据库。Acquire 立即取得当前索引和 Updating/Error 状态，距离上次检查超过 10 秒才启动异步刷新；写入串行化，刷新上下文最多 2 分钟。调用方 defer release，Close 取消刷新并等待读者与刷新任务结束，再关闭 DB。

所以首次搜索或刚修改文件后可能返回旧结果且 Updating=true。前端 polling 工具会按状态重新查询；“搜索返回成功”不等于已经同步扫描整个工作区。

## 网络搜索与抓取

[websearch/service.go](../../internal/websearch/service.go)、[backends.go](../../internal/websearch/backends.go) 与本地 SearchIndex 独立。它们查公开网页，不写入会话或文档搜索数据库。

auto 模式按 AnySearch → Bing → DuckDuckGo 顺序尝试；记录每次成功、空结果、错误或质量不足诊断，并决定是否 fallback。后端负责 HTTP 请求、字符集和结果解析；Service 统一规范化查询、域名过滤、去重和结果上限。过滤需要候选超采样，避免仅请求少量结果后被过滤为空。

全部自动后端失败时，当前接口可返回空 Results 与 all_failed 诊断而没有 Go error；调用方应检查结果和 diagnostics。显式指定后端与 auto fallback 的错误行为不同。不要只凭 `err == nil` 宣称找到资料。

[websearch_tool.go](../../internal/tools/builtin/websearch_tool.go) 暴露 `web_search`；[webfetch_tool.go](../../internal/tools/builtin/webfetch_tool.go) 获取正文、解析 HTML 并输出结构化文本。网络地址校验、正文上限和网页不可信内容的边界见第 05/06 章。网页 HTML 抽取规则会受站点变动影响，单元测试不能证明外部站点永久可用。

## 备份格式、密钥与一致性

[databackup/archive.go](../../internal/databackup/archive.go) 定义 ZIP 清单，Format=1，每个文件记录相对路径、大小和 SHA-256；上限为 100,000 文件与 20 GiB 累计内容。检查拒绝路径穿越、重复或保留名称、符号链接/非普通文件，以及清单不匹配和内容哈希不匹配。

导出跳过应用根下 logs/cache/tmp 及待执行计划文件。备份目标不能位于数据根内，否则扫描会把输出也包含进自己。输出先写同目录私有临时文件，关闭、Sync 后 rename；失败不直接暴露半个正式备份。

[encrypted.go](../../internal/databackup/encrypted.go) 使用 age 的 scrypt passphrase recipient，口令至少 12 个字符。ZIP 流直接写入加密 writer；系统凭据通过单独导出的 map 加入加密归档，旧 secrets 目录不直接复制。凭据导出有独立大小和格式校验，不是对整个系统钥匙串做备份。

校验/恢复通过 age.DecryptReaderAt 在加密文件上提供随机读取，再交 zip.NewReader，不先生成完整的明文 ZIP 临时副本。校验逐项读取并核对清单/哈希；凭据项单独限 10 MiB。恢复仍需要 stage 目录和旧根副本所需磁盘空间，不能只按 `.age` 压缩文件大小判断空间需求。

源码仍保留普通 ZIP 的 Create/Verify/Restore，主要用于基础能力、兼容和测试；当前桌面导出入口安排 `.age` 加密备份。不能因看到 ZIP 底层函数就将桌面导出说明写成明文备份。

备份不会替模型缓存、CDP 会话或审批 checkpoint 保存进程内状态。恢复后的活动执行需要相应模块协调为 interrupted；只有持久事实可以重建。

## 为什么备份/恢复安排到启动阶段

[schedule.go](../../internal/databackup/schedule.go) 将目的路径、备份文件 SHA-256 和加密标志写到 pending plan，口令存入独立 BackupVault。JSON 中不保存明文口令。计划备份与恢复互斥，支持状态读取和取消。

桌面 [AppService](../../internal/services/appservice.go) 通过原生文件选择器安排计划，不在正在写 SQLite/JSONL 的应用中直接替换数据目录。桌面入口取得实例锁后，在 Bootstrap 打开业务 Store 前执行 pending 操作，见第 01 章。安排导出后返回路径，不能误解为文件已立即写完。

恢复先验证归档和安排时的文件哈希，再解密/检查内容，解包到新的 stage 目录。验证通过后，将原根移到带时间的 before-restore 目录，将 stage rename 为正式根。新根提交失败时尝试恢复旧目录。加密恢复还导入系统凭据；导入失败时，将新根隔离并回退原数据，凭据导入自身也有补偿逻辑。

这里有多个文件系统/系统凭据操作，不是一个数据库事务。失败返回包含回滚失败信息，旧数据副本用于恢复；不能用“原子 rename”概括所有阶段都绝不会失败。

## 跨模块用例：明确操作归属

[usecases/agent_lifecycle.go](../../internal/usecases/agent_lifecycle.go) 编排 Agent/Session 删除。删除 Agent 先 SuspendAgent，借 Runtime 的删除边界关闭冲突执行并调用 Agent 删除，再清理 Session 元数据和会话权限规则。删除 Session 交给 Tasks 的 DeleteConversation，先解决任务引用，再清理会话规则。这些操作不能简单变为 UI 分别调用多个 Store 删除接口。

[skill_maintenance.go](../../internal/usecases/skill_maintenance.go) 先查 AgentsUsingSkill，有引用时给出 Agent 名称并拒绝删除；通过检查后才调用 Skill Manager。它避免领域包互相依赖，统一管理跨域不变量。

[mcp_configuration.go](../../internal/usecases/mcp_configuration.go) 将 UI 的 Secret 输入转换为凭据引用，再提交 Server 配置。更新创建新凭据 ID，避免覆盖仍被旧运行快照使用的引用；准备/提交失败清理新建凭据，成功后按引用生命周期维护旧凭据。创建配置与保存凭据并非单一事务，因此用显式补偿处理。

[vision.go](../../internal/usecases/vision.go) 编排视觉辅助调用和输出处理，与模型 Registry、Multimodal/Context 共用接口。其配置、限制和消息桥接见第 03/07 章，不应在 Wails DTO 层另造一套视觉模型适配。
