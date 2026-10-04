# 07 模型与凭据

源码：[types.go](../../internal/models/types.go)、[registry.go](../../internal/models/registry.go)、[store.go](../../internal/models/store.go)、[factory.go](../../internal/models/factory.go)、[runtime.go](../../internal/models/runtime.go)。

## 配置、服务与运行对象

Provider 保存 ID/name/type/base_url/credential_id/时间；Model保存ProviderID、远端ModelName、显示名、timeout_ms、窗口、最大输出、Capabilities和Enabled。MultimediaConfig保存ImageModelID。前端配置ID与供应商实际模型字符串不是同一个身份。

Store负责providers.json/models.json的读取、schema与原子写入；Registry负责业务校验、凭据变更、引用检查、SDK实例缓存和revision。删除Provider先检查所属Models，删除Model检查Agent角色和多媒体引用，避免留悬空配置。更新以显式APIKey变更标识区分“保持原密钥”与“更换”，保存失败有相应补偿。

`ResolveSnapshot`读缓存时仍核对存储；未命中取得写锁、重新检查、解析启用模型并构造SDK实例，返回实例和配置/revision/有效能力。配置变更清除缓存并增加revision；旧Turn继续使用其已冻结对象。缓存当前使用单个Registry锁，这一点列为可选并发优化，详见维护章。

## Provider 适配

| 类型 | Factory实际构造 |
| --- | --- |
| openai | Eino openai.ChatModel；要求系统Credential，MaxCompletionTokens使用配置最大输出 |
| openai_compatible | 同一Chat Completions适配器，BaseURL可配置、Credential可选；MaxTokens使用配置最大输出 |
| ollama | Eino ollama.ChatModel，BaseURL/Model/HTTPClient，NumPredict设置输出上限 |

主聊天实现的API标识为openai-completions或ollama。go.mod中存在agenticopenai依赖及独立示例，不代表主Factory当前使用Responses API。

`newStreamingHTTPClient` clone默认Transport并设置ResponseHeaderTimeout；Client.Timeout=0，让长流式回答不被“包括读取正文的总HTTP超时”截断。等待响应头仍有限时，Runtime Context负责整轮/任务期限。模型BaseURL是用户配置的供应商服务，可以有本机Ollama等合法场景，不能照搬公开网页工具的禁私网策略。

## 能力判断与思考回放

[capabilities.go](../../internal/models/capabilities.go) 对tools/vision/files/reasoning/json/audio使用auto/enabled/disabled三态。auto按Provider和ModelName关键词推断；显式设置覆盖推断。embedding/rerank/tts等名称默认不视为工具聊天模型；Files自动值保守为false。

这是一种本地启发式，不是远端探测或供应商真实能力保证。UI诊断可提醒冲突，Runtime在真正需要Tools/Vision/Files时再校验。Audio/JSON能力字段的存在不等于音频采集、语音转写或通用JSON模式产品链路全部实现。

[reasoning_replay.go](../../internal/models/reasoning_replay.go) 包装OpenAI类模型Generate/Stream，复制Assistant消息后移除ReasoningContent及reasoning输出部分，保留文本/调用。持久化thinking仍可供UI查看，但不能默认作为下一轮普通推理输入；Provider特有的回放身份通过其余Codec/策略边界处理。

`TestModel`是真实小请求测试，`DiagnoseModel`偏配置和能力检查；两者不能混为一项。本次文档工作没有再次向真实供应商发测试请求。

## 凭据存储

[credential/system.go](../../internal/credential/system.go) 的NewSystem基于go-keyring使用系统凭据库；secrets目录维护受控索引。Provider/MCP配置只存CredentialID引用，DTO只显示是否配置，不返还秘密值。

`Put`先读取原秘密，再写系统库/索引；索引保存失败尝试恢复旧系统值。Get能把旧.secret文件迁移到系统库，成功后移除旧文件；目录/ID/文件类型经过校验。`credential.New`保留文件Store实现，桌面Bootstrap实际选择NewSystem，不能据文件实现存在推断生产密钥都以明文落盘。

ExportAll从索引和遗留文件列出并取秘密，用于显式加密备份；ImportAll先记录旧值、排序写入，失败逆序恢复。凭据库与配置文件没有天然跨系统事务，补偿是代码中处理部分失败的方式。

[vault.go](../../internal/credential/vault.go) 的BackupVault使用独立keyring service保存待执行备份/恢复口令。口令不写pending JSON；操作完成/取消删除对应秘密。日志和Wails错误使用脱敏路径，避免第三方SDK原始错误暴露认证内容。
