/**
 * 持久化消息与工具调用的解析和展示文案。
 *
 * 这里只解释消息协议，不判断一次调用是否真正修改了文件或创建了任务。
 * 副作用投影见 toolEffects.js；时间线拼接见 toolTrace.js。
 */
import { t } from "../i18n/index.js";

/**
 * 判断 value 是否为普通对象。
 *
 * Array、null 都不属于这里需要处理的普通对象。
 */
export function isObject(value) {
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
export function readStringProperty(
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

/** 从 Assistant Message 中读取持久化的工具调用。 */
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

        glob_files:
            "查找文件",

        grep_files:
            "搜索内容",

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

        browser:
            "浏览网页",

        list_agents:
            "查看可用 Agent",

        run_agent:
            "调用专业 Agent",

        schedule_task:
            "安排提醒或任务",

        context_resource:
            "读取上下文资料",
    };

    return t(names[toolName] || toolName || "未知工具");
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
                return t("查看 Workspace 根目录");
            }

            return t("查看 {path} 目录", { path });
        }

        case "read_file": {
            const path =
                readStringProperty(
                    argumentsObject,
                    "path",
                );

            if (path) {
                return t("读取 {path}", { path });
            }

            return t("读取文件");
        }

        case "write_file": {
            const path =
                readStringProperty(
                    argumentsObject,
                    "path",
                );

            if (path) {
                return t("写入 {path}", { path });
            }

            return t("写入文件");
        }

        case "edit_file": {
            const path =
                readStringProperty(
                    argumentsObject,
                    "path",
                );

            if (path) {
                return t("编辑 {path}", { path });
            }

            return t("编辑文件");
        }

        case "glob": {
            const pattern =
                readStringProperty(
                    argumentsObject,
                    "pattern",
                );

            if (pattern) {
                return t("查找 {pattern}", { pattern });
            }

            return t("查找文件");
        }

        case "grep": {
            const query =
                readStringProperty(
                    argumentsObject,
                    "query",
                );

            if (query) {
                return t("搜索「{query}」", { query });
            }

            return t("搜索内容");
        }

        case "shell_execute":
        case "run_command":
            return t("执行命令");

        case "http_request": {
            const url =
                readStringProperty(
                    argumentsObject,
                    "url",
                );

            if (url) {
                return t("请求 {url}", { url });
            }

            return t("发送 HTTP 请求");
        }

        case "web_search": {
            const query =
                readStringProperty(
                    argumentsObject,
                    "query",
                );

            if (query) {
                return t("搜索「{query}」", { query });
            }

            return t("搜索网页");
        }

        case "web_fetch": {
            const url =
                readStringProperty(
                    argumentsObject,
                    "url",
                );

            if (url) {
                return t("读取 {url}", { url });
            }

            return t("读取网页");
        }

        case "browser": {
            const action = readStringProperty(argumentsObject, "action");
            if (action === "screenshot") return t("截取网页");
            if (action === "open") return t("打开网页");
            if (action === "snapshot") return t("查看网页");
            if (action === "click") return t("点击网页元素");
            if (action === "type") return t("输入网页内容");
            if (action === "close") return t("关闭浏览器");
            return t("浏览网页");
        }

        case "context_resource":
            return t("读取上下文资料");

        default:
            return toolDisplayName(
                name,
            );
    }
}
