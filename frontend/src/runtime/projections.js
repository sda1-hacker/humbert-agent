import { formatStructuredText, toolActionLabel, toolDisplayName } from "../utils/toolTrace.js";

// 只读 DTO 投影与输入签名：没有 Store、IPC 或事件订阅，方便协议演进和独立验证。
export function normalizeStringList(value) {
  if (!Array.isArray(value)) {
    return [];
  }

  return value.filter((item) => typeof item === "string").map((item) => item.trim()).filter(Boolean);
}

/**
 * Runtime Manifest 是后端统一 Resolver 对“下一 Turn 会加载什么”的只读投影。
 * 前端只做展示边界归一化，不重新推导 Capability 选择。
 */
export function normalizeRuntimeManifest(value) {
  if (!value || typeof value !== "object") {
    return null;
  }

  const workspace = value.workspace && typeof value.workspace === "object" ? value.workspace : {};
  const sandbox = value.sandbox && typeof value.sandbox === "object" ? value.sandbox : {};

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
    mcpServers: Array.isArray(value.mcpServers) ? value.mcpServers.slice() : [],
    mcpUnavailable: Array.isArray(value.mcpUnavailable) ? value.mcpUnavailable.slice() : [],
    mcpTools: Array.isArray(value.mcpTools) ? value.mcpTools.slice() : [],
    mcpToolNames: normalizeStringList(value.mcpToolNames),
    exposedToolNames: normalizeStringList(value.exposedToolNames),
    extensions: Array.isArray(value.extensions) ? value.extensions.filter((item) => item && typeof item.id === "string").map((item) => ({
      id: item.id, revision: String(item.revision || ""), toolNames: normalizeStringList(item.toolNames),
    })) : [],
    workspace: { mode: workspace.mode ?? "", rootDir: workspace.rootDir ?? "" },
    sandbox: {
      profile: sandbox.profile ?? "",
      networkMode: sandbox.networkMode ?? "",
      nativeMode: sandbox.nativeMode ?? "",
      nativeBackend: sandbox.nativeBackend ?? "",
      nativeReady: Boolean(sandbox.nativeReady),
    },
  };
}

export function normalizeContextAssembly(value) {
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
    checkpointInjected: Boolean(value.checkpointInjected),
    latestCompactionID: typeof value.latestCompactionID === "string" ? value.latestCompactionID : "",
  };
}

export function normalizeActiveRunStatus(value) {
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
    approval: value.approval && typeof value.approval === "object" ? value.approval : null,
    runtime: normalizeRuntimeManifest(value.runtime),
  };
}

export function createLiveToolCall(event) {
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

/**
 * 把 Wails 返回的 Context Usage 归一化为 Composer 可直接消费的稳定结构。
 *
 * 后端已经负责预算合法性；前端仍把 IPC 返回视为外部输入，对数字做有限校验，避免异常
 * 值导致 SVG 环形进度出现 NaN/Infinity 或负长度。这里不修改后端语义，只做展示边界保护。
 */
export function normalizeContextUsage(value) {
  if (!value || typeof value !== "object") {
    return null;
  }

  const contextWindow = Number(value.contextWindow);
  const usedTokens = Number(value.usedTokens);
  const systemTokens = Number(value.systemTokens);
  const toolTokens = Number(value.toolTokens);
  const checkpointTokens = Number(value.checkpointTokens);
  const messageTokens = Number(value.messageTokens);
  const reserveTokens = Number(value.reserveTokens);
  const thresholdTokens = Number(value.thresholdTokens);
  const keepRecentTokens = Number(value.keepRecentTokens);
  const percent = Number(value.percent);

  if (!Number.isFinite(contextWindow) || contextWindow <= 0 || !Number.isFinite(usedTokens) || usedTokens < 0) {
    return null;
  }

  return {
    contextWindow,
    usedTokens,
    systemTokens: Number.isFinite(systemTokens) ? Math.max(0, systemTokens) : 0,
    toolTokens: Number.isFinite(toolTokens) ? Math.max(0, toolTokens) : 0,
    checkpointTokens: Number.isFinite(checkpointTokens) ? Math.max(0, checkpointTokens) : 0,
    messageTokens: Number.isFinite(messageTokens) ? Math.max(0, messageTokens) : 0,
    reserveTokens: Number.isFinite(reserveTokens) ? Math.max(0, reserveTokens) : 0,
    thresholdTokens: Number.isFinite(thresholdTokens) ? Math.max(0, thresholdTokens) : 0,
    keepRecentTokens: Number.isFinite(keepRecentTokens) ? Math.max(0, keepRecentTokens) : 0,
    percent: Number.isFinite(percent) ? Math.max(0, percent) : (usedTokens * 100 / contextWindow),
    needsCompaction: Boolean(value.needsCompaction),
    latestCompactionID: typeof value.latestCompactionID === "string" ? value.latestCompactionID : "",
    updatedAt: value.updatedAt ?? "",
  };
}

export function stableTextHash(value) {
  const text = String(value ?? "");
  let hash = 2166136261;
  for (let index = 0; index < text.length; index += 1) {
    hash ^= text.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0).toString(16).padStart(8, "0");
}

export function userInputSignature(content, attachments = []) {
  const files = Array.isArray(attachments)
  ? attachments.map((item) => {
    const data = String(item?.base64Data || "");
    return `${item?.name || ""}:${item?.mimeType || ""}:${data.length}:${stableTextHash(data)}`;
  })
  : [];
  return `${stableTextHash(content)}:${String(content || "").length}\n${files.join("|")}`;
}

