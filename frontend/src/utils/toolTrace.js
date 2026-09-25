/**
 * 将消息和工具事务拼接成用户可读的对话时间线。
 *
 * JSONL 中的 assistant(tool_calls) 与 tool(result) 必须保持顺序和关联；
 * 这里仅构造 UI 投影，不修改原始记录，也不把 reasoning 当作工具结果。
 * 对外继续重导出原有工具函数，兼容现有组件和 Store 的引用路径。
 */
import {
    isObject,
    readStringProperty,
    metadataOf,
    reasoningOf,
    toolCallsOf,
    isToolCallMessage,
    isToolResultMessage,
    toolDisplayName,
    formatStructuredText,
    toolActionLabel,
} from "./toolProtocol.js";

export {
    metadataOf,
    reasoningOf,
    toolCallsOf,
    isToolCallMessage,
    isToolResultMessage,
    toolDisplayName,
    parseToolArguments,
    formatStructuredText,
    toolActionLabel,
} from "./toolProtocol.js";
export { browserNeedsHumanVerification, browserScreenshotOfCall, fileChangesOfCalls, scheduledTasksOfCalls } from "./toolEffects.js";

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

        // 展示投影保留模型步骤与工具调用的原始顺序；JSONL 协议不变。
        steps: [],

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
    if (reasoning || note) {
        trace.steps.push({
            type: "thinking",
            key: `thinking:${message?.id || trace.steps.length}`,
            content: reasoning,
            note,
            status: "completed",
        });
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

        trace.steps.push({
            type: "tool",
            key: `tool:${call.id}`,
            call,
        });

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

        trace.steps.push({
            type: "tool",
            key: `tool:${call.id}`,
            call,
        });

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
 * activity.steps 则保留每个 Assistant Step 与工具请求的时间顺序。
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

                activity: trace.steps,

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

            const finalReasoning =
                reasoningOf(message);

            const activity = trace?.steps
                ? [...trace.steps]
                : [];

            if (finalReasoning) {
                activity.push({
                    type: "thinking",
                    key: `thinking:${message.id || sequence}`,
                    content: finalReasoning,
                    note: "",
                    status: "completed",
                });
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

                activity,

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
