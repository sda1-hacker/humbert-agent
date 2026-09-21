import { defineStore } from "pinia";

import {
    Events,
} from "@wailsio/runtime";

import {
    cancelTurn,
    compactContext,
    getContextOverview,
    resolveApproval,
    startTurn,
} from "../api/chat.js";

import {
    useSessionStore,
} from "./sessions.js";

import {
    formatStructuredText,
    toolActionLabel,
    toolDisplayName,
} from "../utils/toolTrace.js";

const runtimeEventName =
    "humbert:runtime:event";

let unsubscribeRuntimeEvent = null;

/**
 * 同一动画帧内尚未提交给 Vue 的 Assistant Text Delta。
 *
 * 模型可能以很高频率返回 SSE Chunk。如果每个 Chunk 都立即修改 Pinia，Vue 会持续
 * 进行文本重排、scrollHeight 计算和滚动处理。这里把一帧内的 Delta 合并后再提交，
 * 不丢失任何文本，但显著降低 Streaming 抖动。
 */
const pendingContentDeltaBuffers =
    new Map();

/**
 * 同一动画帧内尚未提交给 Vue 的 Provider Reasoning Delta。
 *
 * Reasoning 与最终正文必须分开保存和展示，因此不能和 Content Buffer 混在一起。
 */
const pendingReasoningDeltaBuffers =
    new Map();

/**
 * sessionID -> requestAnimationFrame / setTimeout Handle。
 */
const pendingDeltaFrames =
    new Map();

function scheduleUIFrame(callback) {
    if (
        typeof globalThis.requestAnimationFrame ===
        "function"
    ) {
        return globalThis.requestAnimationFrame(
            callback,
        );
    }

    return globalThis.setTimeout(
        callback,
        16,
    );
}

function cancelUIFrame(handle) {
    if (
        typeof globalThis.cancelAnimationFrame ===
        "function"
    ) {
        globalThis.cancelAnimationFrame(
            handle,
        );

        return;
    }

    globalThis.clearTimeout(handle);
}

/**
 * 把 Wails 返回的 Context Usage 归一化为 Composer 可直接消费的稳定结构。
 *
 * 后端已经负责预算合法性；前端仍把 IPC 返回视为外部输入，对数字做有限校验，避免异常
 * 值导致 SVG 环形进度出现 NaN/Infinity 或负长度。这里不修改后端语义，只做展示边界保护。
 */
function normalizeStringList(value) {
    if (!Array.isArray(value)) {
        return [];
    }

    return value
        .filter((item) => typeof item === "string")
        .map((item) => item.trim())
        .filter(Boolean);
}

/**
 * Runtime Manifest 是后端统一 Resolver 对“下一 Turn 会加载什么”的只读投影。
 * 前端只做展示边界归一化，不重新推导 Capability 选择。
 */
function normalizeRuntimeManifest(value) {
    if (!value || typeof value !== "object") {
        return null;
    }

    const workspace =
        value.workspace && typeof value.workspace === "object"
            ? value.workspace
            : {};
    const sandbox =
        value.sandbox && typeof value.sandbox === "object"
            ? value.sandbox
            : {};

    return {
        agentID: value.agentID ?? "",
        agentName: value.agentName ?? "",
        modelID: value.modelID ?? "",
        modelDisplayName: value.modelDisplayName ?? "",
        modelRevision: Number(value.modelRevision) || 0,
        toolRevision: Number(value.toolRevision) || 0,
        builtinToolNames: normalizeStringList(value.builtinToolNames),
        skillRevision: value.skillRevision ?? "",
        skillNames: normalizeStringList(value.skillNames),
        mcpRevision: Number(value.mcpRevision) || 0,
        mcpServers: Array.isArray(value.mcpServers)
            ? value.mcpServers.slice()
            : [],
        mcpUnavailable: Array.isArray(value.mcpUnavailable)
            ? value.mcpUnavailable.slice()
            : [],
        mcpTools: Array.isArray(value.mcpTools)
            ? value.mcpTools.slice()
            : [],
        mcpToolNames: normalizeStringList(value.mcpToolNames),
        exposedToolNames: normalizeStringList(value.exposedToolNames),
        workspace: {
            mode: workspace.mode ?? "",
            rootDir: workspace.rootDir ?? "",
        },
        sandbox: {
            profile: sandbox.profile ?? "",
            networkMode: sandbox.networkMode ?? "",
            nativeMode: sandbox.nativeMode ?? "",
            nativeBackend: sandbox.nativeBackend ?? "",
            nativeReady: Boolean(sandbox.nativeReady),
        },
    };
}

function normalizeContextAssembly(value) {
    if (!value || typeof value !== "object") {
        return null;
    }

    const number = (input) => {
        const parsed = Number(input);
        return Number.isFinite(parsed) ? Math.max(0, Math.trunc(parsed)) : 0;
    };

    return {
        visibleMessageCount: number(value.visibleMessageCount),
        recentMessageCount: number(value.recentMessageCount),
        userMessageCount: number(value.userMessageCount),
        assistantMessageCount: number(value.assistantMessageCount),
        toolResultCount: number(value.toolResultCount),
        toolCallCount: number(value.toolCallCount),
        memoryInjected: Boolean(value.memoryInjected),
        checkpointInjected: Boolean(value.checkpointInjected),
        latestCompactionID:
            typeof value.latestCompactionID === "string"
                ? value.latestCompactionID
                : "",
    };
}

function normalizeActiveRunStatus(value) {
    if (!value || typeof value !== "object" || !value.requestID) {
        return null;
    }

    return {
        requestID: value.requestID ?? "",
        runID: value.runID ?? "",
        sessionID: value.sessionID ?? "",
        phase: value.phase ?? "running",
        startedAt: value.startedAt ?? "",
        waitingApprovalID: value.waitingApprovalID ?? "",
        approval:
            value.approval && typeof value.approval === "object"
                ? value.approval
                : null,
        runtime: normalizeRuntimeManifest(value.runtime),
    };
}

function createLiveToolCall(event) {
    const call = {
        id: event?.toolCallID ?? "",
        name: event?.toolName ?? "",
        displayName: toolDisplayName(event?.toolName ?? ""),
        arguments: event?.toolArguments ?? "",
        formattedArguments: formatStructuredText(event?.toolArguments ?? ""),
        status: "running",
        result: "",
        formattedResult: "",
        error: "",
        durationMS: 0,
        requestedAt: event?.occurredAt ?? "",
        completedAt: "",
    };
    call.actionLabel = toolActionLabel(call);
    return call;
}

function normalizeContextUsage(value) {
    if (!value || typeof value !== "object") {
        return null;
    }

    const contextWindow =
        Number(value.contextWindow);
    const usedTokens =
        Number(value.usedTokens);
    const systemTokens =
        Number(value.systemTokens);
    const toolTokens =
        Number(value.toolTokens);
    const memoryTokens =
        Number(value.memoryTokens);
    const referenceTokens =
        Number(value.referenceTokens);
    const checkpointTokens =
        Number(value.checkpointTokens);
    const messageTokens =
        Number(value.messageTokens);
    const reserveTokens =
        Number(value.reserveTokens);
    const thresholdTokens =
        Number(value.thresholdTokens);
    const keepRecentTokens =
        Number(value.keepRecentTokens);
    const percent =
        Number(value.percent);

    if (
        !Number.isFinite(contextWindow) ||
        contextWindow <= 0 ||
        !Number.isFinite(usedTokens) ||
        usedTokens < 0
    ) {
        return null;
    }

    return {
        contextWindow,
        usedTokens,
        systemTokens:
            Number.isFinite(systemTokens)
                ? Math.max(0, systemTokens)
                : 0,
        toolTokens:
            Number.isFinite(toolTokens)
                ? Math.max(0, toolTokens)
                : 0,
        memoryTokens:
            Number.isFinite(memoryTokens)
                ? Math.max(0, memoryTokens)
                : 0,
        referenceTokens:
            Number.isFinite(referenceTokens)
                ? Math.max(0, referenceTokens)
                : 0,
        checkpointTokens:
            Number.isFinite(checkpointTokens)
                ? Math.max(0, checkpointTokens)
                : 0,
        messageTokens:
            Number.isFinite(messageTokens)
                ? Math.max(0, messageTokens)
                : 0,
        reserveTokens:
            Number.isFinite(reserveTokens)
                ? Math.max(0, reserveTokens)
                : 0,
        thresholdTokens:
            Number.isFinite(thresholdTokens)
                ? Math.max(0, thresholdTokens)
                : 0,
        keepRecentTokens:
            Number.isFinite(keepRecentTokens)
                ? Math.max(0, keepRecentTokens)
                : 0,
        percent:
            Number.isFinite(percent)
                ? Math.max(0, percent)
                : (
                    usedTokens * 100 /
                    contextWindow
                ),
        needsCompaction:
            Boolean(value.needsCompaction),
        latestCompactionID:
            typeof value.latestCompactionID === "string"
                ? value.latestCompactionID
                : "",
        updatedAt:
            value.updatedAt ?? "",
    };
}

/**
 * RuntimeStore 只保存尚未持久化完成的前端实时状态。
 *
 * Session JSONL v3 永远是历史消息的唯一真实数据源：
 *
 *   - assistant.delta / assistant.reasoning.delta 只用于实时 UI；
 *   - Assistant Step 完成后由后端一次性持久化完整 schema.Message；
 *   - Turn 进入终态后重新读取 Session JSONL，再清理 Streaming State。
 */
function stableTextHash(value) {
    const text = String(value ?? "");
    let hash = 2166136261;
    for (let index = 0; index < text.length; index += 1) {
        hash ^= text.charCodeAt(index);
        hash = Math.imul(hash, 16777619);
    }
    return (hash >>> 0).toString(16).padStart(8, "0");
}

function userInputSignature(content, attachments = []) {
    const files = Array.isArray(attachments)
        ? attachments.map((item) => {
            const data = String(item?.base64Data || "");
            return `${item?.name || ""}:${item?.mimeType || ""}:${data.length}:${stableTextHash(data)}`;
        })
        : [];
    return `${stableTextHash(content)}:${String(content || "").length}\n${files.join("|")}`;
}

export const useRuntimeStore =
    defineStore(
        "runtime",
        {
            state: () => ({
                /**
                 * sessionID -> requestID
                 */
                activeRuns: {},

                /**
                 * sessionID -> modelID
                 */
                activeModels: {},

                /**
                 * sessionID -> 当前活动 Turn 的统一生命周期状态。
                 */
                runStates: {},

                /**
                 * sessionID -> 当前活动 Turn 的实时 Tool Lifecycle。
                 * 历史 Tool Trace 仍然以 Session JSONL 为准。
                 */
                liveToolCalls: {},

                /**
                 * sessionID -> partial assistant visible text
                 */
                streamingContents: {},

                /**
                 * sessionID -> partial provider reasoning text
                 */
                streamingReasonings: {},

                /**
                 * StartTurn binding 尚未返回的 Session。
                 */
                startingSessions: {},

                /**
                 * sessionID -> 最近进入终态的 requestID。
                 *
                 * 用于解决 completion event 比 StartTurn Promise 更早到达的竞态。
                 */
                terminalRequests: {},

                /**
                 * sessionID -> 最近一次 Runtime Failure 的用户可见错误。
                 *
                 * 错误不是 Session 持久化事实，因此只保存在当前前端进程；下一次发送会清理。
                 */
                terminalErrors: {},

                // 保存初始化失败的消息收据；同一内容重试时复用后端消息，避免重复追加。
                failedStarts: {},

                /**
                 * sessionID -> Context Usage。
                 *
                 * 这是后端 ContextEngine 的展示快照，不是历史事实；切换模型、完成 Turn 或
                 * 手动压缩后都可以直接覆盖。
                 */
                contextUsages: {},

                /**
                 * sessionID -> 与 Context Usage 同一次 Build 得到的安全 Context Assembly 摘要。
                 */
                contextAssemblies: {},

                /**
                 * sessionID -> 与 Context Usage 同一次 Resolve 得到的 Runtime Manifest。
                 */
                contextManifests: {},

                /**
                 * sessionID -> 是否正在读取 Context Usage。
                 */
                contextLoading: {},

                /**
                 * sessionID -> 是否正在执行手动 Context Compaction。
                 */
                contextCompacting: {},

                /**
                 * sessionID -> 最近一次 Context 状态读取错误。
                 * 自动刷新失败不会改变 Turn 状态，Composer 只把它作为非阻塞提示。
                 */
                contextErrors: {},

                /**
                 * sessionID -> 当前待处理 Approval Request。
                 *
                 * Approval 是进程内 Runtime Control State，不写入 Session Transcript。后端
                 * approval.requested/resolved/expired 事件是唯一事实源，前端只缓存安全 DTO。
                 */
                pendingApprovals: {},

                /** approvalID -> 是否正在提交用户决策。 */
                approvalResolving: {},

                /** approvalID -> 最近一次提交错误。 */
                approvalErrors: {},
            }),

            getters: {
                isSessionRunning:
                    (state) =>
                        (sessionID) => {
                            return Boolean(
                                state.activeRuns[
                                    sessionID
                                    ] ||
                                state.startingSessions[
                                    sessionID
                                    ],
                            );
                        },

                streamingContent:
                    (state) =>
                        (sessionID) => {
                            return (
                                state.streamingContents[
                                    sessionID
                                    ] ?? ""
                            );
                        },

                streamingReasoning:
                    (state) =>
                        (sessionID) => {
                            return (
                                state.streamingReasonings[
                                    sessionID
                                    ] ?? ""
                            );
                        },

                terminalError:
                    (state) =>
                        (sessionID) => {
                            return (
                                state.terminalErrors[
                                    sessionID
                                    ] ?? ""
                            );
                        },

                modelIDForSession:
                    (state) =>
                        (sessionID) => {
                            return (
                                state.activeModels[
                                    sessionID
                                    ] ?? ""
                            );
                        },

                runState:
                    (state) =>
                        (sessionID) => (
                            state.runStates[
                                sessionID
                                ] ?? null
                        ),

                liveTools:
                    (state) =>
                        (sessionID) => (
                            state.liveToolCalls[
                                sessionID
                                ] ?? []
                        ),

                contextUsage:
                    (state) =>
                        (sessionID) => {
                            return (
                                state.contextUsages[
                                    sessionID
                                    ] ?? null
                            );
                        },

                contextAssembly:
                    (state) =>
                        (sessionID) => {
                            return (
                                state.contextAssemblies[
                                    sessionID
                                    ] ?? null
                            );
                        },

                contextManifest:
                    (state) =>
                        (sessionID) => {
                            return (
                                state.contextManifests[
                                    sessionID
                                    ] ?? null
                            );
                        },

                isContextLoading:
                    (state) =>
                        (sessionID) => Boolean(
                            state.contextLoading[
                                sessionID
                                ],
                        ),

                isContextCompacting:
                    (state) =>
                        (sessionID) => Boolean(
                            state.contextCompacting[
                                sessionID
                                ],
                        ),

                contextError:
                    (state) =>
                        (sessionID) => (
                            state.contextErrors[
                                sessionID
                                ] ?? ""
                        ),

                pendingApproval:
                    (state) =>
                        (sessionID) => (
                            state.pendingApprovals[
                                sessionID
                                ] ?? null
                        ),

                isApprovalResolving:
                    (state) =>
                        (approvalID) => Boolean(
                            state.approvalResolving[
                                approvalID
                                ],
                        ),

                approvalError:
                    (state) =>
                        (approvalID) => (
                            state.approvalErrors[
                                approvalID
                                ] ?? ""
                        ),
            },

            actions: {
                /**
                 * 全应用只注册一个 Wails Runtime Listener。
                 */
                initialiseEvents() {
                    if (unsubscribeRuntimeEvent) {
                        return;
                    }

                    unsubscribeRuntimeEvent =
                        Events.On(
                            runtimeEventName,
                            (event) => {
                                void this.handleEvent(
                                    event?.data,
                                );
                            },
                        );
                },

                /**
                 * 释放 Runtime Listener 和所有尚未执行的动画帧刷新任务。
                 */
                disposeEvents() {
                    if (unsubscribeRuntimeEvent) {
                        unsubscribeRuntimeEvent();
                        unsubscribeRuntimeEvent =
                            null;
                    }

                    for (
                        const handle of
                        pendingDeltaFrames.values()
                        ) {
                        cancelUIFrame(handle);
                    }

                    pendingDeltaFrames.clear();
                    pendingContentDeltaBuffers.clear();
                    pendingReasoningDeltaBuffers.clear();
                },

                /**
                 * 把 Text / Reasoning Delta 放入同一个 Session 的动画帧缓冲。
                 *
                 * @param {string} sessionID Session ID。
                 * @param {"content"|"reasoning"} kind Delta 类型。
                 * @param {string} delta 本次增量文本。
                 */
                queueStreamingDelta(
                    sessionID,
                    kind,
                    delta,
                ) {
                    if (
                        !sessionID ||
                        !delta
                    ) {
                        return;
                    }

                    const target =
                        kind === "reasoning"
                            ? pendingReasoningDeltaBuffers
                            : pendingContentDeltaBuffers;

                    target.set(
                        sessionID,
                        (
                            target.get(
                                sessionID,
                            ) ?? ""
                        ) + delta,
                    );

                    if (
                        pendingDeltaFrames.has(
                            sessionID,
                        )
                    ) {
                        return;
                    }

                    const handle =
                        scheduleUIFrame(
                            () => {
                                pendingDeltaFrames.delete(
                                    sessionID,
                                );

                                this.flushStreamingDelta(
                                    sessionID,
                                );
                            },
                        );

                    pendingDeltaFrames.set(
                        sessionID,
                        handle,
                    );
                },

                /**
                 * 立即把某个 Session 尚未提交的 Text / Reasoning Delta 写入 Pinia。
                 *
                 * Turn 进入终态前必须先 Flush，确保最后一帧文本不会因为随后清理 Runtime
                 * State 而丢失。
                 */
                flushStreamingDelta(sessionID) {
                    if (!sessionID) {
                        return;
                    }

                    const handle =
                        pendingDeltaFrames.get(
                            sessionID,
                        );

                    if (handle !== undefined) {
                        cancelUIFrame(handle);
                        pendingDeltaFrames.delete(
                            sessionID,
                        );
                    }

                    const contentDelta =
                        pendingContentDeltaBuffers.get(
                            sessionID,
                        ) ?? "";

                    const reasoningDelta =
                        pendingReasoningDeltaBuffers.get(
                            sessionID,
                        ) ?? "";

                    pendingContentDeltaBuffers.delete(
                        sessionID,
                    );

                    pendingReasoningDeltaBuffers.delete(
                        sessionID,
                    );

                    if (contentDelta) {
                        this.streamingContents[
                            sessionID
                            ] =
                            (
                                this.streamingContents[
                                    sessionID
                                    ] ?? ""
                            ) + contentDelta;
                    }

                    if (reasoningDelta) {
                        this.streamingReasonings[
                            sessionID
                            ] =
                            (
                                this.streamingReasonings[
                                    sessionID
                                    ] ?? ""
                            ) + reasoningDelta;
                    }
                },

                /**
                 * 清理尚未 Flush 的 Session Delta。
                 */
                discardStreamingDelta(sessionID) {
                    if (!sessionID) {
                        return;
                    }

                    const handle =
                        pendingDeltaFrames.get(
                            sessionID,
                        );

                    if (handle !== undefined) {
                        cancelUIFrame(handle);
                        pendingDeltaFrames.delete(
                            sessionID,
                        );
                    }

                    pendingContentDeltaBuffers.delete(
                        sessionID,
                    );

                    pendingReasoningDeltaBuffers.delete(
                        sessionID,
                    );
                },

                /**
                 * 统一处理实时 Tool Lifecycle。
                 *
                 * MessageList 不再单独订阅 Runtime Event，因此同一个 Session 的 Turn、Approval、
                 * Streaming 与 Tool 状态都由 RuntimeStore 维护一份事实源。
                 */
                applyToolLifecycleEvent(event) {
                    const sessionID = event?.sessionID;
                    const callID = event?.toolCallID;
                    if (!sessionID || !callID) {
                        return;
                    }

                    const calls = Array.isArray(this.liveToolCalls[sessionID])
                        ? this.liveToolCalls[sessionID]
                        : [];
                    let call = calls.find((item) => item.id === callID) ?? null;
                    if (!call) {
                        call = createLiveToolCall(event);
                        calls.push(call);
                        this.liveToolCalls[sessionID] = calls;
                    }

                    if (event.toolName) {
                        call.name = event.toolName;
                        call.displayName = toolDisplayName(event.toolName);
                    }
                    if (typeof event.toolArguments === "string" && event.toolArguments) {
                        call.arguments = event.toolArguments;
                        call.formattedArguments = formatStructuredText(event.toolArguments);
                    }
                    call.actionLabel = toolActionLabel(call);

                    switch (event.type) {
                        case "tool.started":
                            call.status = "running";
                            call.requestedAt = event.occurredAt || call.requestedAt;
                            break;
                        case "tool.completed":
                            call.status = "completed";
                            call.durationMS = Number.isFinite(event.durationMS)
                                ? event.durationMS
                                : 0;
                            call.completedAt = event.occurredAt || "";
                            break;
                        case "tool.failed":
                            call.status = "failed";
                            call.durationMS = Number.isFinite(event.durationMS)
                                ? event.durationMS
                                : 0;
                            call.error = event.error || "";
                            call.completedAt = event.occurredAt || "";
                            break;
                        default:
                            break;
                    }
                },

                /**
                 * 从后端刷新指定 Session 的 Context Usage。
                 *
                 * silent=true 用于 Turn 完成、Session 切换等自动刷新：失败时只保留错误状态，
                 * 不影响消息终态清理。用户显式操作则使用默认 false，让调用组件展示错误。
                 */
                async refreshContextUsage(
                    sessionID,
                    options = {},
                ) {
                    if (!sessionID) {
                        return null;
                    }

                    this.contextLoading[
                        sessionID
                        ] = true;

                    try {
                        const raw =
                            await getContextOverview(
                                sessionID,
                            );
                        const usage =
                            normalizeContextUsage(
                                raw?.usage,
                            );
                        const assembly =
                            normalizeContextAssembly(
                                raw?.assembly,
                            );
                        const manifest =
                            normalizeRuntimeManifest(
                                raw?.runtime,
                            );
                        const active =
                            normalizeActiveRunStatus(
                                raw?.active,
                            );

                        if (!usage || !assembly || !manifest) {
                            throw new Error(
                                "后端返回了无效的 Runtime Context Overview",
                            );
                        }

                        this.contextUsages[
                            sessionID
                            ] = usage;
                        this.contextAssemblies[
                            sessionID
                            ] = assembly;
                        this.contextManifests[
                            sessionID
                            ] = manifest;

                        // Overview.Active 是后端活动 Run 的恢复入口。它让 UI 在重新挂载、
                        // 切换 Session 后不必完全依赖“恰好没有错过”的 turn.started event。
                        if (active) {
                            this.runStates[
                                sessionID
                                ] = active;
                            this.activeRuns[
                                sessionID
                                ] = active.requestID;
                            this.activeModels[
                                sessionID
                                ] = active.runtime?.modelID ?? "";
                            if (active.approval?.id) {
                                this.pendingApprovals[
                                    sessionID
                                    ] = active.approval;
                            } else {
                                delete this.pendingApprovals[
                                    sessionID
                                    ];
                            }
                        } else if (!this.startingSessions[sessionID]) {
                            // 如果 UI 错过了终态 Event（例如 WebView 重新挂载），Overview 是
                            // 后端活动 Run 的恢复真相；没有 active 就清理前端陈旧运行态。
                            delete this.runStates[sessionID];
                            delete this.activeRuns[sessionID];
                            delete this.activeModels[sessionID];
                            delete this.liveToolCalls[sessionID];
                            const pending = this.pendingApprovals[sessionID];
                            if (pending?.id) {
                                delete this.approvalResolving[pending.id];
                                delete this.approvalErrors[pending.id];
                            }
                            delete this.pendingApprovals[sessionID];
                        }

                        delete this.contextErrors[
                            sessionID
                            ];

                        return usage;
                    } catch (error) {
                        this.contextErrors[
                            sessionID
                            ] =
                            error?.message ??
                            String(error);

                        if (!options.silent) {
                            throw error;
                        }

                        return null;
                    } finally {
                        delete this.contextLoading[
                            sessionID
                            ];
                    }
                },

                /**
                 * 执行用户主动 Context Compaction。
                 *
                 * Runtime 后端会再次检查 Session Busy，因此这里的前端检查只是即时 UX，不能
                 * 被当成权限或并发保护。成功后使用响应携带的最新 Usage，避免额外 IPC；如果
                 * 响应没有合法 Usage，则退回一次只读刷新。
                 */
                async compactSessionContext(
                    sessionID,
                    updateMemory = false,
                ) {
                    if (!sessionID) {
                        throw new Error(
                            "请先选择一个会话",
                        );
                    }
                    if (
                        this.isSessionRunning(
                            sessionID,
                        )
                    ) {
                        throw new Error(
                            "当前会话仍在生成回复，暂时不能压缩 Context",
                        );
                    }

                    this.contextCompacting[
                        sessionID
                        ] = true;

                    try {
                        const result =
                            await compactContext(
                                sessionID,
                                updateMemory,
                            );
                        const usage =
                            normalizeContextUsage(
                                result?.contextUsage,
                            );

                        if (usage) {
                            this.contextUsages[
                                sessionID
                                ] = usage;
                            delete this.contextErrors[
                                sessionID
                                ];
                        } else {
                            await this.refreshContextUsage(
                                sessionID,
                            );
                        }

                        return result;
                    } finally {
                        delete this.contextCompacting[
                            sessionID
                            ];
                    }
                },

                /**
                 * 发起新 Turn。
                 */
                async send(
                    sessionID,
                    content,
                    attachments = [],
                ) {
                    if (
                        this.isSessionRunning(
                            sessionID,
                        )
                    ) {
                        throw new Error(
                            "当前会话仍在生成回复",
                        );
                    }

                    this.discardStreamingDelta(
                        sessionID,
                    );

                    this.startingSessions[
                        sessionID
                        ] = true;

                    this.streamingContents[
                        sessionID
                        ] = "";

                    this.streamingReasonings[
                        sessionID
                        ] = "";

                    this.liveToolCalls[
                        sessionID
                        ] = [];
                    delete this.runStates[
                        sessionID
                        ];

                    delete this.terminalErrors[
                        sessionID
                        ];

                    try {
                        const retry = this.failedStarts[sessionID];
                        const signature = userInputSignature(content, attachments);
                        const result =
                            await startTurn(
                                sessionID,
                                content,
                                attachments,
                                retry?.signature === signature ? retry.userMessageID : "",
                            );

                        if (result.startError) {
                            this.failedStarts[sessionID] = {
                                signature,
                                userMessageID: result.userMessageID,
                            };
                            this.terminalErrors[sessionID] = result.startError;
                            throw new Error(result.startError);
                        }
                        delete this.failedStarts[sessionID];

                        if (
                            this.terminalRequests[
                                sessionID
                                ] !==
                            result.requestID
                        ) {
                            this.activeRuns[
                                sessionID
                                ] =
                                result.requestID;
                        }

                        const initialUsage =
                            normalizeContextUsage(
                                result.contextUsage,
                            );
                        if (initialUsage) {
                            this.contextUsages[
                                sessionID
                                ] = initialUsage;
                            delete this.contextErrors[
                                sessionID
                                ];
                        }

                        const initialAssembly =
                            normalizeContextAssembly(
                                result.contextAssembly,
                            );
                        if (initialAssembly) {
                            this.contextAssemblies[
                                sessionID
                                ] = initialAssembly;
                        }

                        const frozenManifest =
                            normalizeRuntimeManifest(
                                result.runtime,
                            );
                        if (frozenManifest) {
                            this.contextManifests[
                                sessionID
                                ] = frozenManifest;
                            if (
                                this.terminalRequests[sessionID] !== result.requestID
                            ) {
                                this.runStates[sessionID] = {
                                    requestID: result.requestID,
                                    runID: result.runID ?? "",
                                    sessionID,
                                    phase: this.runStates[sessionID]?.requestID === result.requestID
                                        ? this.runStates[sessionID].phase : "running",
                                    startedAt: "",
                                    waitingApprovalID: "",
                                    approval: null,
                                    runtime: frozenManifest,
                                };
                            }
                        }

                        const sessionStore =
                            useSessionStore();

                        try {
                            await sessionStore.refreshMessages(sessionID);
                        } catch {
                            // 运行已被接受，读取历史失败不能伪装成发送失败并恢复重复草稿。
                            // 后续终态事件仍会重新同步历史。
                            this.terminalErrors[sessionID] = "消息已发送，暂时无法刷新历史。";
                        }

                        return result;
                    } catch (error) {
                        // 发送失败也可能已经写入历史，刷新后让用户看到真实已保存的输入。
                        // 刷新失败保留原始发送错误；收据不清除，下一次重试仍可复用。
                        try {
                            await useSessionStore().refreshMessages(sessionID);
                        } catch {
                            this.terminalErrors[sessionID] = "消息发送或历史刷新失败，请检查会话后重试。";
                        }
                        this.discardStreamingDelta(
                            sessionID,
                        );

                        delete this
                            .streamingContents[
                            sessionID
                            ];

                        delete this
                            .streamingReasonings[
                            sessionID
                            ];

                        throw error;
                    } finally {
                        delete this
                            .startingSessions[
                            sessionID
                            ];
                    }
                },

                /**
                 * 提交一次 Human Approval 决策。
                 *
                 * 只把 approvalID + decision 发送给后端；Tool 原始参数不进入 WebView。后端
                 * 成功接受后不会等待整个 Agent Turn 完成，后续状态继续通过 Runtime Event
                 * 驱动。重复点击由前端 loading 和后端 Pending→Resolving 两层共同防护。
                 */
                async resolveToolApproval(approvalID, decision) {
                    if (!approvalID) {
                        throw new Error("Approval ID 不能为空");
                    }
                    if (this.approvalResolving[approvalID]) {
                        return null;
                    }

                    this.approvalResolving[approvalID] = true;
                    delete this.approvalErrors[approvalID];

                    try {
                        return await resolveApproval(
                            approvalID,
                            decision,
                        );
                    } catch (error) {
                        this.approvalErrors[approvalID] =
                            error?.message ?? String(error);
                        throw error;
                    } finally {
                        delete this.approvalResolving[approvalID];
                    }
                },

                async cancel(sessionID) {
                    const requestID =
                        this.activeRuns[
                            sessionID
                            ];

                    if (!requestID) {
                        return;
                    }

                    const previousPhase =
                        this.runStates[sessionID]?.phase ?? "running";
                    if (this.runStates[sessionID]) {
                        this.runStates[sessionID].phase = "cancelling";
                    }

                    try {
                        await cancelTurn(
                            requestID,
                        );
                    } catch (error) {
                        if (
                            this.runStates[sessionID]?.requestID === requestID
                        ) {
                            this.runStates[sessionID].phase = previousPhase;
                        }
                        throw error;
                    }
                },

                /**
                 * 处理 Go Runtime Event。
                 */
                async handleEvent(data) {
                    if (
                        !data ||
                        typeof data !==
                        "object" ||
                        !data.sessionID
                    ) {
                        return;
                    }

                    switch (data.type) {
                        case "turn.maintaining": {
                            const run = this.runStates[data.sessionID];
                            if (run?.requestID === data.requestID && run.phase !== "cancelling") {
                                run.phase = "maintaining";
                            }
                            break;
                        }
                        case "turn.started": {
                            this.discardStreamingDelta(
                                data.sessionID,
                            );
                            const frozenManifest =
                                normalizeRuntimeManifest(
                                    data.runtime,
                                );
                            if (frozenManifest) {
                                this.contextManifests[
                                    data.sessionID
                                    ] = frozenManifest;
                            }

                            this.activeRuns[
                                data.sessionID
                                ] = data.requestID;
                            this.activeModels[
                                data.sessionID
                                ] = data.modelID ?? "";
                            this.runStates[
                                data.sessionID
                                ] = {
                                requestID: data.requestID ?? "",
                                runID: data.runID ?? "",
                                sessionID: data.sessionID,
                                phase: "running",
                                startedAt: data.occurredAt ?? "",
                                waitingApprovalID: "",
                                approval: null,
                                runtime: frozenManifest,
                            };
                            this.liveToolCalls[
                                data.sessionID
                                ] = [];

                            this.streamingContents[
                                data.sessionID
                                ] = "";
                            this.streamingReasonings[
                                data.sessionID
                                ] = "";

                            break;
                        }

                        case "assistant.reasoning.delta":
                            if (
                                this.terminalRequests[
                                    data.sessionID
                                    ] ===
                                data.requestID
                            ) {
                                break;
                            }

                            this.activeRuns[
                                data.sessionID
                                ] =
                                data.requestID;

                            this.activeModels[
                                data.sessionID
                                ] =
                                data.modelID ?? "";

                            if (
                                this.runStates[data.sessionID]?.requestID === data.requestID
                            ) {
                                this.runStates[data.sessionID].phase = "running";
                            }

                            this.queueStreamingDelta(
                                data.sessionID,
                                "reasoning",
                                data.delta ?? "",
                            );

                            break;

                        case "assistant.delta":
                            if (
                                this.terminalRequests[
                                    data.sessionID
                                    ] ===
                                data.requestID
                            ) {
                                break;
                            }

                            this.activeRuns[
                                data.sessionID
                                ] =
                                data.requestID;

                            this.activeModels[
                                data.sessionID
                                ] =
                                data.modelID ?? "";

                            if (
                                this.runStates[data.sessionID]?.requestID === data.requestID
                            ) {
                                this.runStates[data.sessionID].phase = "running";
                            }

                            this.queueStreamingDelta(
                                data.sessionID,
                                "content",
                                data.delta ?? "",
                            );

                            break;

                        case "tool.started":
                        case "tool.completed":
                        case "tool.failed":
                            if (
                                this.terminalRequests[data.sessionID] === data.requestID
                            ) {
                                break;
                            }
                            this.activeRuns[data.sessionID] = data.requestID;
                            this.applyToolLifecycleEvent(data);
                            if (
                                this.runStates[data.sessionID]?.requestID === data.requestID
                            ) {
                                this.runStates[data.sessionID].phase = "running";
                            }
                            break;

                        case "approval.requested": {
                            const approval = data.approval;
                            if (
                                approval &&
                                typeof approval === "object" &&
                                approval.id
                            ) {
                                this.pendingApprovals[data.sessionID] = approval;
                                delete this.approvalErrors[approval.id];
                                if (
                                    this.runStates[data.sessionID]?.requestID === data.requestID
                                ) {
                                    this.runStates[data.sessionID].phase = "waiting_approval";
                                    this.runStates[data.sessionID].waitingApprovalID = approval.id;
                                    this.runStates[data.sessionID].approval = approval;
                                }
                            }
                            break;
                        }

                        case "approval.resolved":
                        case "approval.expired": {
                            const approvalID = data.approval?.id;
                            const current = this.pendingApprovals[data.sessionID];
                            if (
                                current &&
                                (!approvalID || current.id === approvalID)
                            ) {
                                delete this.pendingApprovals[data.sessionID];
                            }
                            if (approvalID) {
                                delete this.approvalResolving[approvalID];
                                delete this.approvalErrors[approvalID];
                            }
                            if (
                                this.runStates[data.sessionID]?.requestID === data.requestID
                            ) {
                                this.runStates[data.sessionID].phase = "running";
                                this.runStates[data.sessionID].waitingApprovalID = "";
                                this.runStates[data.sessionID].approval = null;
                            }
                            break;
                        }

                        case "turn.completed":
                        case "turn.failed":
                        case "turn.cancelled":
                            await this.finalise(
                                data,
                            );

                            break;

                        default:
                            break;
                    }
                },

                /**
                 * 处理所有 Runtime 终态。
                 */
                async finalise(data) {
                    this.flushStreamingDelta(
                        data.sessionID,
                    );

                    this.terminalRequests[
                        data.sessionID
                        ] =
                        data.requestID;

                    delete this.activeRuns[
                        data.sessionID
                        ];

                    const pendingApproval =
                        this.pendingApprovals[data.sessionID];
                    if (
                        pendingApproval &&
                        (!data.requestID || pendingApproval.requestID === data.requestID)
                    ) {
                        delete this.pendingApprovals[data.sessionID];
                        delete this.approvalResolving[pendingApproval.id];
                        delete this.approvalErrors[pendingApproval.id];
                    }

                    if (
                        data.type ===
                        "turn.failed" &&
                        typeof data.error ===
                        "string" &&
                        data.error.trim()
                    ) {
                        this.terminalErrors[
                            data.sessionID
                            ] =
                            data.error.trim();
                    } else if (
                        data.type ===
                        "turn.completed"
                    ) {
                        delete this.terminalErrors[
                            data.sessionID
                            ];
                    }

                    const sessionStore =
                        useSessionStore();

                    try {
                        await sessionStore
                            .refreshMessages(
                                data.sessionID,
                            );

                        await this.refreshContextUsage(
                            data.sessionID,
                            {
                                silent: true,
                            },
                        );
                    } finally {
                        this.discardStreamingDelta(
                            data.sessionID,
                        );

                        delete this
                            .streamingContents[
                            data.sessionID
                            ];

                        delete this
                            .streamingReasonings[
                            data.sessionID
                            ];

                        delete this
                            .activeModels[
                            data.sessionID
                            ];

                        delete this.runStates[
                            data.sessionID
                            ];
                        delete this.liveToolCalls[
                            data.sessionID
                            ];
                    }
                },
            },
        },
    );
