/**
 * Tool Trace UI 数据转换模块。
 *
 * Humbert 后端持久化的 Tool Calling 对话链类似：
 *
 * user
 * assistant(tool_calls)
 * tool(result)
 * assistant(tool_calls)
 * tool(result)
 * assistant(final)
 *
 * 对模型来说，这些都是必须保留的上下文。
 *
 * 对用户来说，我们把 Provider Reasoning 与 Tool Calling 分开：
 *
 * Humbert · model
 *
 * 思考过程
 *   Provider 返回的 reasoning_content
 *
 * 工具调用
 *   ├─ 查看目录
 *   └─ 读取 README.md
 *
 * 最终回答……
 *
 * 因此这里负责把底层 Message Protocol 转换成 UI Turn。
 */

/**
 * 判断 value 是否为普通对象。
 *
 * Array、null 都不属于这里需要处理的普通对象。
 */
function isObject(value) {
    return (
        value !== null &&
        typeof value === "object" &&
        !Array.isArray(value)
    );
}

/**
 * 安全读取对象中的字符串字段。
 *
 * 这里专门避免直接访问：
 *
 *   fn.arguments
 *
 * 因为部分 ESLint 配置会把 `.arguments`
 * 识别为对 Function.arguments 的非法引用。
 *
 * 使用：
 *
 *   readStringProperty(fn, "arguments")
 *
 * 可以保持 ToolCall JSON 原始字段名称不变，
 * 同时避免关闭 ESLint 安全规则。
 */
function readStringProperty(
    object,
    key,
) {
    if (!isObject(object)) {
        return "";
    }

    const value =
        object[key];

    return (
        typeof value === "string"
            ? value
            : ""
    );
}

/**
 * 安全读取 Message Metadata。
 */
export function metadataOf(message) {
    if (
        !message ||
        !isObject(message.metadata)
    ) {
        return {};
    }

    return message.metadata;
}

/**
 * 从 Assistant Message 中读取持久化 ToolCalls。
 */

/**
 * 读取后端从 JSONL v3 AssistantMessage 投影出的 Provider Reasoning。
 *
 * reasoning_content 是模型协议数据，不是 Tool Trace，也不是前端根据 Tool 数量伪造的
 * “思考过程”。历史 UI 只展示 Provider 实际返回并持久化下来的内容。
 */
export function reasoningOf(message) {
    if (
        !message ||
        message.role !== "assistant"
    ) {
        return "";
    }

    const metadata =
        metadataOf(message);

    const reasoning =
        readStringProperty(
            metadata,
            "reasoning_content",
        );

    return reasoning.trim();
}

export function toolCallsOf(message) {
    const metadata =
        metadataOf(message);

    if (
        !Array.isArray(
            metadata.tool_calls,
        )
    ) {
        return [];
    }

    return metadata.tool_calls.filter(
        (call) =>
            isObject(call) &&
            typeof call.id === "string" &&
            call.id.trim() !== "",
    );
}

/**
 * 判断是否是带 ToolCalls 的 Assistant Message。
 */
export function isToolCallMessage(
    message,
) {
    return (
        message?.role ===
        "assistant" &&
        toolCallsOf(message).length >
        0
    );
}

/**
 * 判断是否是 Tool Result Message。
 */
export function isToolResultMessage(
    message,
) {
    return (
        message?.role === "tool"
    );
}

/**
 * Tool 技术名称对应的用户友好名称。
 *
 * 后端仍然坚持稳定 snake_case，
 * 中文名称只属于 UI Presentation。
 */
export function toolDisplayName(
    toolName,
) {
    const names = {
        list_files:
            "查看目录",

        read_file:
            "读取文件",

        write_file:
            "写入文件",

        edit_file:
            "编辑文件",

        glob:
            "查找文件",

        grep:
            "搜索内容",

        shell_execute:
            "执行命令",

        http_request:
            "HTTP 请求",

        web_search:
            "搜索网页",

        web_fetch:
            "读取网页",

        run_command:
            "执行命令",

        list_agents:
            "查看可用 Agent",

        run_agent:
            "调用专业 Agent",
    };

    return (
        names[toolName] ||
        toolName ||
        "未知工具"
    );
}

/**
 * 尝试把 Tool Arguments JSON String
 * 解析成普通 Object。
 *
 * 非法 JSON、Array、Primitive 都统一返回空对象。
 */
export function parseToolArguments(
    value,
) {
    if (
        typeof value !== "string" ||
        !value.trim()
    ) {
        return {};
    }

    try {
        const parsed =
            JSON.parse(value);

        return isObject(parsed)
            ? parsed
            : {};
    } catch {
        return {};
    }
}

/**
 * 对结构化文本做 Pretty Print。
 *
 * 如果 value 是合法 JSON：
 *
 *   {"path":"README.md"}
 *
 * 会格式化成：
 *
 *   {
 *     "path": "README.md"
 *   }
 *
 * 如果不是 JSON，则保留原始文本。
 */
export function formatStructuredText(
    value,
) {
    if (
        value === null ||
        value === undefined
    ) {
        return "";
    }

    if (
        typeof value !== "string"
    ) {
        try {
            return JSON.stringify(
                value,
                null,
                2,
            );
        } catch {
            return String(value);
        }
    }

    const text =
        value.trim();

    if (!text) {
        return "";
    }

    try {
        return JSON.stringify(
            JSON.parse(text),
            null,
            2,
        );
    } catch {
        return value;
    }
}

/**
 * 根据 Tool Name + Arguments
 * 生成稳定、客观的用户可见活动描述。
 *
 * 这里不会展示模型隐藏推理。
 *
 * 示例：
 *
 * list_files {"path":"."}
 *   -> 查看 Workspace 根目录
 *
 * read_file {"path":"README.md"}
 *   -> 读取 README.md
 */
export function toolActionLabel(
    call,
) {
    const name =
        call?.name || "";

    const argumentsObject =
        parseToolArguments(
            call?.arguments || "",
        );

    switch (name) {
        case "list_files": {
            const path =
                readStringProperty(
                    argumentsObject,
                    "path",
                ) || ".";

            if (
                !path ||
                path === "."
            ) {
                return "查看 Workspace 根目录";
            }

            return `查看 ${path} 目录`;
        }

        case "read_file": {
            const path =
                readStringProperty(
                    argumentsObject,
                    "path",
                );

            if (path) {
                return `读取 ${path}`;
            }

            return "读取文件";
        }

        case "write_file": {
            const path =
                readStringProperty(
                    argumentsObject,
                    "path",
                );

            if (path) {
                return `写入 ${path}`;
            }

            return "写入文件";
        }

        case "edit_file": {
            const path =
                readStringProperty(
                    argumentsObject,
                    "path",
                );

            if (path) {
                return `编辑 ${path}`;
            }

            return "编辑文件";
        }

        case "glob": {
            const pattern =
                readStringProperty(
                    argumentsObject,
                    "pattern",
                );

            if (pattern) {
                return `查找 ${pattern}`;
            }

            return "查找文件";
        }

        case "grep": {
            const query =
                readStringProperty(
                    argumentsObject,
                    "query",
                );

            if (query) {
                return `搜索「${query}」`;
            }

            return "搜索内容";
        }

        case "shell_execute":
        case "run_command":
            return "执行命令";

        case "http_request": {
            const url =
                readStringProperty(
                    argumentsObject,
                    "url",
                );

            if (url) {
                return `请求 ${url}`;
            }

            return "发送 HTTP 请求";
        }

        case "web_search": {
            const query =
                readStringProperty(
                    argumentsObject,
                    "query",
                );

            if (query) {
                return `搜索「${query}」`;
            }

            return "搜索网页";
        }

        case "web_fetch": {
            const url =
                readStringProperty(
                    argumentsObject,
                    "url",
                );

            if (url) {
                return `读取 ${url}`;
            }

            return "读取网页";
        }

        default:
            return toolDisplayName(
                name,
            );
    }
}

/**
 * 从持久化 ToolCall 创建 UI Call。
 */
function createTraceCall(
    rawCall,
    assistantMessage,
) {
    const functionValue =
        isObject(
            rawCall?.function,
        )
            ? rawCall.function
            : {};

    const name =
        readStringProperty(
            functionValue,
            "name",
        );

    /**
     * ToolCall JSON 的字段规范就是：
     *
     *   "arguments"
     *
     * 这里必须保留这个协议字段，
     * 但不能写 functionValue.arguments，
     * 否则部分 ESLint 配置会触发
     * Function.arguments 限制。
     */
    const argumentsText =
        readStringProperty(
            functionValue,
            "arguments",
        );

    const call = {
        id:
        rawCall.id,

        name,

        displayName:
            toolDisplayName(name),

        arguments:
        argumentsText,

        formattedArguments:
            formatStructuredText(
                argumentsText,
            ),

        status:
            "requested",

        result:
            "",

        formattedResult:
            "",

        error:
            "",

        requestedAt:
            assistantMessage?.createdAt ||
            "",

        completedAt:
            "",
    };

    call.actionLabel =
        toolActionLabel(call);

    return call;
}

/**
 * 把 Tool Result 合并进对应 ToolCall。
 */
function applyToolResult(
    call,
    message,
) {
    const metadata =
        metadataOf(message);

    const status =
        metadata.is_error === true
            ? "failed"
            : (
                readStringProperty(
                    metadata,
                    "status",
                ) || "completed"
            );

    call.status =
        status;

    call.result =
        typeof message?.content ===
        "string"
            ? message.content
            : "";

    call.formattedResult =
        formatStructuredText(
            call.result,
        );

    call.error =
        readStringProperty(
            metadata,
            "error",
        );

    if (
        status === "failed" &&
        !call.error
    ) {
        call.error =
            call.result;
    }

    call.completedAt =
        typeof message?.createdAt ===
        "string"
            ? message.createdAt
            : "";
}

/**
 * 创建一组连续 Tool Trace。
 */
function createTraceGroup(
    message,
    sequence,
) {
    return {
        key:
            `trace:${
                message?.id ||
                sequence
            }`,

        calls: [],

        notes: [],

        /**
         * Provider 真正返回的 reasoning_content。
         *
         * Tool Calling 链可能包含多个 Assistant Step，因此按 Step 保存多个 Segment，
         * 最终由 AssistantTurn 统一放进可折叠 Thinking Card。
         */
        reasoningSegments: [],

        /**
         * callMap 只用于构建阶段快速通过 ToolCallID
         * 找到对应 Tool Result。
         *
         * finishTrace() 后会删除，
         * 不进入 Vue UI 状态。
         */
        callMap:
            new Map(),

        startedAt:
            message?.createdAt || "",

        completedAt:
            "",
    };
}

/**
 * 向 Trace 添加 Assistant ToolCalls。
 */
function appendAssistantToolCalls(
    trace,
    message,
) {
    const note =
        typeof message?.content ===
        "string"
            ? message.content.trim()
            : "";

    const reasoning =
        reasoningOf(message);

    if (
        reasoning &&
        !trace.reasoningSegments.includes(
            reasoning,
        )
    ) {
        trace.reasoningSegments.push(
            reasoning,
        );
    }

    /**
     * 某些模型会在 ToolCall 前公开输出：
     *
     * “我先查看一下工作区文件。”
     *
     * 这是模型主动提供给用户的 Assistant Content，
     * 可以作为过程说明展示。
     *
     * 它不是隐藏 Chain-of-Thought。
     */
    if (
        note &&
        !trace.notes.includes(note)
    ) {
        trace.notes.push(note);
    }

    for (
        const rawCall of
        toolCallsOf(message)
        ) {
        if (
            trace.callMap.has(
                rawCall.id,
            )
        ) {
            continue;
        }

        const call =
            createTraceCall(
                rawCall,
                message,
            );

        trace.calls.push(call);

        trace.callMap.set(
            call.id,
            call,
        );
    }
}

/**
 * 向 Trace 添加 Tool Result。
 *
 * 正常情况下 Tool Result 一定会通过：
 *
 *   metadata.tool_call_id
 *
 * 找到对应 Assistant ToolCall。
 *
 * 如果历史数据异常导致 Tool Result 孤立，
 * UI 仍然保留该记录，避免静默丢失执行信息。
 */
function appendToolResult(
    trace,
    message,
) {
    const metadata =
        metadataOf(message);

    const callID =
        readStringProperty(
            metadata,
            "tool_call_id",
        );

    const toolName =
        readStringProperty(
            metadata,
            "tool_name",
        );

    let call =
        callID
            ? trace.callMap.get(
                callID,
            )
            : null;

    if (!call) {
        call = {
            id:
                callID ||
                `orphan:${
                    message?.id ||
                    trace.calls.length
                }`,

            name:
            toolName,

            displayName:
                toolDisplayName(
                    toolName,
                ),

            arguments:
                "",

            formattedArguments:
                "",

            status:
                "completed",

            result:
                "",

            formattedResult:
                "",

            error:
                "",

            requestedAt:
                "",

            completedAt:
                "",
        };

        call.actionLabel =
            toolActionLabel(call);

        trace.calls.push(call);

        trace.callMap.set(
            call.id,
            call,
        );
    }

    if (
        !call.name &&
        toolName
    ) {
        call.name =
            toolName;

        call.displayName =
            toolDisplayName(
                toolName,
            );

        call.actionLabel =
            toolActionLabel(call);
    }

    applyToolResult(
        call,
        message,
    );

    trace.completedAt =
        message?.createdAt ||
        trace.completedAt;
}

/**
 * 清理只用于构建阶段的内部字段。
 */
function finishTrace(trace) {
    if (!trace) {
        return null;
    }

    delete trace.callMap;

    return trace;
}

/**
 * 将底层 Message Protocol 转成聊天 UI Block。
 *
 * 最重要的是：
 *
 * assistant(tool_calls)
 * tool
 * assistant(final)
 *
 * 会被合并成一个 assistant-turn。
 *
 * 最终：
 *
 * {
 *   type: "assistant-turn",
 *   trace,
 *   message
 * }
 *
 * UI 因此能够固定展示成：
 *
 * Humbert · model
 *
 * 思考过程
 *
 * 最终回答
 */
export function buildConversationBlocks(
    messages,
) {
    if (!Array.isArray(messages)) {
        return [];
    }

    const blocks = [];

    let pendingTrace =
        null;

    let sequence =
        0;

    /**
     * 如果 Tool 执行之后 Turn 异常结束，
     * 没有最终 Assistant Message，
     * 仍然必须把 Trace 展示出来。
     */
    const flushTraceOnlyTurn =
        () => {
            if (!pendingTrace) {
                return;
            }

            const trace =
                finishTrace(
                    pendingTrace,
                );

            blocks.push({
                type:
                    "assistant-turn",

                key:
                    `assistant-turn:${trace.key}`,

                trace,

                reasoning:
                    trace.reasoningSegments
                        .join("\n\n"),

                message:
                    null,
            });

            pendingTrace =
                null;
        };

    for (const message of messages) {
        sequence += 1;

        if (
            isToolCallMessage(
                message,
            )
        ) {
            if (!pendingTrace) {
                pendingTrace =
                    createTraceGroup(
                        message,
                        sequence,
                    );
            }

            appendAssistantToolCalls(
                pendingTrace,
                message,
            );

            continue;
        }

        if (
            isToolResultMessage(
                message,
            )
        ) {
            if (!pendingTrace) {
                pendingTrace =
                    createTraceGroup(
                        message,
                        sequence,
                    );
            }

            appendToolResult(
                pendingTrace,
                message,
            );

            continue;
        }

        if (
            message?.role ===
            "assistant"
        ) {
            const trace =
                pendingTrace
                    ? finishTrace(
                        pendingTrace,
                    )
                    : null;

            const reasoningSegments =
                trace?.reasoningSegments
                    ? [...trace.reasoningSegments]
                    : [];

            const finalReasoning =
                reasoningOf(message);

            if (
                finalReasoning &&
                !reasoningSegments.includes(
                    finalReasoning,
                )
            ) {
                reasoningSegments.push(
                    finalReasoning,
                );
            }

            pendingTrace =
                null;

            blocks.push({
                type:
                    "assistant-turn",

                key:
                    `assistant-turn:${
                        message.id ||
                        sequence
                    }`,

                trace,

                reasoning:
                    reasoningSegments
                        .join("\n\n"),

                message,
            });

            continue;
        }

        /**
         * User/System 等普通消息意味着之前没有 Final Assistant
         * 的 Trace 已经结束。
         */
        flushTraceOnlyTurn();

        blocks.push({
            type:
                "message",

            key:
                `message:${
                    message?.id ||
                    sequence
                }`,

            message,
        });
    }

    flushTraceOnlyTurn();

    return blocks;
}

/**
 * 统计一个 Trace 中 Tool 的当前状态。
 */
export function summarizeToolTrace(
    calls,
) {
    const values =
        Array.isArray(calls)
            ? calls
            : [];

    let completed =
        0;

    let failed =
        0;

    let running =
        0;

    for (const call of values) {
        switch (call.status) {
            case "completed":
                completed += 1;
                break;

            case "failed":
                failed += 1;
                break;

            default:
                running += 1;
                break;
        }
    }

    return {
        total:
        values.length,

        completed,

        failed,

        running,
    };
}

/**
 * 把 Tool Result 正文尽量解析成普通 JSON Object。
 *
 * 文件变化展示只消费 Humbert 内置文件工具的结构化结果；解析失败时不会猜测工具已经
 * 对哪个文件产生了成功副作用，而是退回到 Tool Arguments 中可以确定的路径信息。
 */
function parseToolResultObject(call) {
    const raw =
        typeof call?.result === "string"
            ? call.result.trim()
            : "";

    if (!raw) {
        return {};
    }

    try {
        const parsed = JSON.parse(raw);
        return isObject(parsed) ? parsed : {};
    } catch {
        return {};
    }
}

/**
 * 判断一个 Tool Path 是否能直接交给 WorkspaceService 作为相对路径打开。
 *
 * Additional Write Path 等 Workspace 外绝对路径仍然会出现在本轮文件变化里，
 * 但不会提供“点击预览”，避免前端通过绝对路径扩大 Workspace 的安全读取边界。
 */
function normalizeBrowsableWorkspacePath(value) {
    if (typeof value !== "string") {
        return { path: "", browsable: false };
    }

    const original = value.trim();
    if (!original) {
        return { path: "", browsable: false };
    }

    const normalized = original.replaceAll("\\", "/");
    const absolute =
        normalized.startsWith("/") ||
        normalized.startsWith("~/") ||
        /^[a-zA-Z]:\//.test(normalized) ||
        normalized.startsWith("//");

    const parts = normalized
        .split("/")
        .filter((part) => part && part !== ".");
    const escapesRoot = parts.some((part) => part === "..");

    return {
        path: normalized.replace(/^\.\//, ""),
        browsable: !absolute && !escapesRoot,
    };
}

/**
 * 合并同一个 Turn 内对同一路径的连续操作。
 *
 * UI 关注的是“这一轮最终涉及了哪些文件”，而不是把底层每一次工具调用重复平铺：
 *
 *   新建 hello.go -> 再编辑 hello.go
 *
 * 最终仍显示“生成 hello.go”；如果随后又删除，则显示“删除 hello.go”。
 */
function mergeTurnFileChange(target, change) {
    if (!change?.path) {
        return;
    }

    const key = change.path;
    const previous = target.get(key);

    if (
        previous?.operation === "created" &&
        change.operation === "modified"
    ) {
        return;
    }

    target.set(key, change);
}

/**
 * 从一次 Assistant Turn 的成功文件工具调用中提取“本轮文件变化”。
 *
 * 这份数据不持久化第二份数据库：历史页面直接从该 Turn 已经持久化的 ToolCall / ToolResult
 * 生成，实时页面也可以使用相同 Call 结构。这样文件操作结果和聊天天然拥有同一生命周期，
 * 而“工作区”页面只负责查看当前文件系统。
 *
 * 返回项：
 *   operation: created / modified / copied / moved / deleted / written
 *   path:      当前/目标路径
 *   fromPath:  move_file 的原路径，其它操作为空
 *   browsable: 是否可以安全地按当前 Workspace 相对路径打开
 */
export function fileChangesOfCalls(calls) {
    const values = Array.isArray(calls) ? calls : [];
    const changes = new Map();

    for (const call of values) {
        // 失败、仍在运行、等待审批的 Tool 都不能展示成已经发生的文件变化。
        if (call?.status !== "completed") {
            continue;
        }

        const name = call?.name || "";
        const input = parseToolArguments(call?.arguments || "");
        const output = parseToolResultObject(call);

        const append = (rawPath, operation, extra = {}) => {
            const normalized = normalizeBrowsableWorkspacePath(rawPath);
            if (!normalized.path) {
                return;
            }
            mergeTurnFileChange(changes, {
                operation,
                path: normalized.path,
                browsable: normalized.browsable && operation !== "deleted",
                ...extra,
            });
        };

        switch (name) {
            case "write_file": {
                const rawPath =
                    readStringProperty(output, "path") ||
                    readStringProperty(input, "path");
                let operation = "written";
                if (output.created === true) {
                    operation = "created";
                } else if (output.overwritten === true) {
                    operation = "modified";
                }
                append(rawPath, operation);
                break;
            }

            case "edit_file": {
                append(
                    readStringProperty(output, "path") ||
                    readStringProperty(input, "path"),
                    "modified",
                );
                break;
            }

            case "apply_patch": {
                const patchChanges = Array.isArray(input.changes)
                    ? input.changes
                    : [];
                const resultFiles = Array.isArray(output.files)
                    ? output.files
                    : [];

                for (let index = 0; index < patchChanges.length; index += 1) {
                    const patch = patchChanges[index];
                    if (!isObject(patch)) continue;

                    // apply_patch 的 result.files 使用 Sandbox 归一化后的 display path，
                    // 优先采用它可以让“绝对 Workspace 输入”仍然得到安全可点击的相对路径。
                    const resultPath =
                        typeof resultFiles[index] === "string"
                            ? resultFiles[index]
                            : "";
                    append(
                        resultPath || readStringProperty(patch, "path"),
                        patch.create === true ? "created" : "modified",
                    );
                }
                break;
            }

            case "copy_file": {
                append(
                    readStringProperty(output, "destination") ||
                    readStringProperty(input, "destination"),
                    "copied",
                );
                break;
            }

            case "move_file": {
                const destination =
                    readStringProperty(output, "destination") ||
                    readStringProperty(input, "destination");
                const source =
                    readStringProperty(output, "source") ||
                    readStringProperty(input, "source");
                const normalizedSource = normalizeBrowsableWorkspacePath(source);
                append(destination, "moved", {
                    fromPath: normalizedSource.path,
                });
                break;
            }

            case "delete_file": {
                append(
                    readStringProperty(output, "path") ||
                    readStringProperty(input, "path"),
                    "deleted",
                );
                break;
            }

            default:
                break;
        }
    }

    return [...changes.values()];
}
