import {
    Call,
} from "@wailsio/runtime";

let bindingPromise = null;

const chatServiceName =
    "github.com/sda1-hacker/humbert-agent/internal/services.ChatService";

/**
 * 延迟加载 Wails ChatService Binding。
 *
 * 已存在的 StartTurn/CancelTurn 继续使用生成的 ByID binding；Context/Compaction 是本次
 * 新增方法，在补丁环境无法可靠重新运行 wails3 generator，因此先通过 Wails v3 的
 * Call.ByName 调用。用户本地重新生成 bindings 后也无需修改这里，ByName 与生成 binding
 * 可以并存，避免手写不稳定的 Method ID。
 */
function loadBinding() {
    if (!bindingPromise) {
        bindingPromise = import(
            "../../bindings/github.com/sda1-hacker/humbert-agent/internal/services/chatservice.js"
            ).catch((error) => {
            bindingPromise = null;


            throw new Error(
                "无法加载 ChatService Binding，请重新生成 Wails bindings",
                {
                    cause: error,
                },
            );
        });
    }

    return bindingPromise;
}

/**
 * 发起一个异步 User Turn。
 *
 * 真正 Assistant streaming output 通过 Wails Event 返回。
 */
export function startTurn(
    sessionID,
    content,
    attachments = [],
    retryUserMessageID = "",
) {
    return Call.ByName(
        `${chatServiceName}.StartTurn`,
        {
            sessionID,
            content,
            attachments,
            retryUserMessageID,
        },
    );
}

/**
 * 取消一个正在运行的 User Turn。
 */
export async function cancelTurn(
    requestID,
) {
    const binding =
        await loadBinding();

    return binding.CancelTurn(
        requestID,
    );
}

/**
 * 读取当前 Session 的 Context 使用情况。
 *
 * 该调用只做本地投影和 Token 估算，不会触发压缩或模型请求。sessionID 由当前 Session
 * Store 提供，不接受任意路径或外部资源地址。
 */
export function getContextStatus(sessionID) {
    return Call.ByName(
        `${chatServiceName}.ContextStatus`,
        sessionID,
    );
}

/**
 * 读取当前 Session 的统一 Runtime Context Overview。
 *
 * 返回值把 Context Usage 与同一次后端 Resolve 得到的 Model / Builtin Tools / Skills / MCP /
 * Workspace / Sandbox Manifest 绑定在一起，避免前端跨多个 Store 自行拼接瞬时状态。
 */
export function getContextOverview(sessionID) {
    return Call.ByName(
        `${chatServiceName}.ContextOverview`,
        sessionID,
    );
}

/**
 * 主动压缩当前 Session Context。
 *
 * updateMemory=false 对应“压缩”；true 对应“压缩并更新”，由后端在同一受控操作中额外
 * 强制刷新当前 Session memory.json。运行中的 Session 会被后端拒绝，前端不尝试绕过。
 */
export function compactContext(
    sessionID,
    updateMemory,
) {
    return Call.ByName(
        `${chatServiceName}.CompactContext`,
        sessionID,
        Boolean(updateMemory),
    );
}

/**
 * 处理一次等待中的 Tool Approval。
 *
 * 前端只提交 approvalID 和用户决策；真实 Tool Arguments 始终保存在 Eino checkpoint，
 * 不会经过 WebView 往返，防止批准内容与最终执行内容被客户端篡改。
 */
export function resolveApproval(approvalID, decision) {
    return Call.ByName(
        `${chatServiceName}.ResolveApproval`,
        {
            approvalID,
            decision,
        },
    );
}
