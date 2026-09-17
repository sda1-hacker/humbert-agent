<script setup>
import {
  computed,
  ref,
  watch,
} from "vue";

import {
  Message,
} from "@arco-design/web-vue";

import {
  IconSend,
  IconStop,
} from "@arco-design/web-vue/es/icon";

import {
  useAgentStore,
} from "../../stores/agents.js";

import {
  useModelStore,
} from "../../stores/models.js";

import {
  useRuntimeStore,
} from "../../stores/runtime.js";

import {
  useSessionStore,
} from "../../stores/sessions.js";

const fileInput =
    ref(null);

const MAX_ATTACHMENTS = 8;
const MAX_ATTACHMENT_BYTES = 12 * 1024 * 1024;
const MAX_ATTACHMENT_TOTAL_BYTES = 24 * 1024 * 1024;
const MAX_TEXT_ATTACHMENT_BYTES = 512 * 1024;

const TEXT_ATTACHMENT_MIME_TYPES = new Set([
  "application/json", "application/ld+json", "application/xml", "application/javascript",
  "application/x-javascript", "application/yaml", "application/x-yaml", "application/toml",
  "application/sql", "application/graphql",
]);

const TEXT_ATTACHMENT_EXTENSIONS = new Set([
  ".txt", ".md", ".markdown", ".json", ".jsonl", ".yaml", ".yml", ".xml", ".csv", ".tsv",
  ".go", ".js", ".jsx", ".ts", ".tsx", ".vue", ".py", ".rb", ".rs", ".java", ".kt",
  ".c", ".h", ".cc", ".cpp", ".cs", ".swift", ".sh", ".zsh", ".fish", ".ps1", ".sql",
  ".html", ".css", ".scss", ".less", ".toml", ".ini", ".conf", ".env", ".graphql",
]);

const IMAGE_ATTACHMENT_EXTENSIONS = new Set([".png", ".jpg", ".jpeg", ".gif", ".webp"]);

const sending =
    ref(false);

const switchingModel =
    ref(false);

const agentStore =
    useAgentStore();

const modelStore =
    useModelStore();

const runtimeStore =
    useRuntimeStore();

const sessionStore =
    useSessionStore();

const attachmentDrafts =
    ref({});

const draft =
    computed({
      get() {
        return sessionStore
            .draftForSession(
                sessionStore.selectedID,
            );
      },
      set(value) {
        sessionStore.setDraft(
            sessionStore.selectedID,
            value,
        );
      },
    });

const attachments =
    computed({
      get() {
        return attachmentDrafts
            .value[
                sessionStore.selectedID
                ] ?? [];
      },
      set(value) {
        const sessionID =
            sessionStore.selectedID;
        if (!sessionID) {
          return;
        }
        attachmentDrafts.value = {
          ...attachmentDrafts.value,
          [sessionID]: Array.isArray(value)
              ? value
              : [],
        };
      },
    });

const running =
    computed(() => (
        runtimeStore
            .isSessionRunning(
                sessionStore.selectedID,
            )
    ));

const selectedModelID =
    computed(() => (
        agentStore
            .selectedAgent
            ?.modelID ?? ""
    ));

const contextUsage =
    computed(() => (
        runtimeStore.contextUsage(
            sessionStore.selectedID,
        )
    ));

const contextManifest =
    computed(() => {
      const sessionID = sessionStore.selectedID;
      const active = runtimeStore.runState(sessionID);

      // Turn 正在执行时优先展示它已经冻结的 Runtime。这样某个 MCP Server 在
      // StartTurn 阶段被降级后，Context 面板不会马上被“下一 Turn 的本地投影”覆盖。
      return active?.runtime || runtimeStore.contextManifest(sessionID);
    });

const contextAssembly =
    computed(() => (
        runtimeStore.contextAssembly(
            sessionStore.selectedID,
        )
    ));

const activeRunState =
    computed(() => (
        runtimeStore.runState(
            sessionStore.selectedID,
        )
    ));


const contextLoading =
    computed(() => (
        runtimeStore.isContextLoading(
            sessionStore.selectedID,
        )
    ));

const contextCompacting =
    computed(() => (
        runtimeStore.isContextCompacting(
            sessionStore.selectedID,
        )
    ));

const contextError =
    computed(() => (
        runtimeStore.contextError(
            sessionStore.selectedID,
        )
    ));

const contextPercent =
    computed(() => {
      const value =
          Number(
              contextUsage.value
                  ?.percent ?? 0,
          );

      if (!Number.isFinite(value)) {
        return 0;
      }

      return Math.min(
          100,
          Math.max(0, value),
      );
    });

const contextRingOffset =
    computed(() => {
      const circumference =
          2 * Math.PI * 8;

      return circumference * (
          1 -
          contextPercent.value / 100
      );
    });

const canSend =
    computed(() => (
        Boolean(
            sessionStore.selectedID,
        ) &&
        Boolean(
            selectedModelID.value,
        ) &&
        (draft.value.trim() !== "" || attachments.value.length > 0) &&
        !running.value &&
        !sending.value
    ));

/**
 * 把 Token 数转换为 Composer 中短而稳定的显示文本。
 *
 * OpenHanako 风格的 Context 提示更适合使用 k 单位；这里保留整数 token 在小值时的可读性，
 * 并避免前端用模型名称猜测 Context Window。所有数值都来自后端 ContextEngine Usage。
 */
function formatTokens(value) {
  const tokens =
      Number(value);

  if (
      !Number.isFinite(tokens) ||
      tokens < 0
  ) {
    return "--";
  }

  if (tokens >= 1000) {
    return `${Math.round(tokens / 1000)}k`;
  }

  return String(
      Math.round(tokens),
  );
}

function formatRuntimeModel(manifest) {
  return (
      manifest?.modelDisplayName ||
      manifest?.modelID ||
      "未配置"
  );
}

function formatRuntimeModelRole(manifest) {
  const role = manifest?.modelRole || manifest?.modelRoles?.activeRole || "chat";
  return role === "image" ? "图片" : "Chat";
}

function formatModelCapabilities(capabilities) {
  if (!capabilities) return "--";
  const labels = [
    ["tools", "Tools"], ["vision", "Vision"], ["files", "Files"],
    ["reasoning", "Reasoning"], ["json", "JSON"], ["audio", "Audio"],
  ].filter(([key]) => Boolean(capabilities[key])).map(([, label]) => label);
  return labels.length ? labels.join(" · ") : "无已声明能力";
}

function attachmentCapabilityError(items) {
  if (!Array.isArray(items) || items.length === 0) return "";
  const agent = agentStore.selectedAgent;
  const chat = modelStore.modelByID(agent?.modelID ?? "");
  if (!chat) return ""; // Runtime 仍会做最终校验。

  const needsVision = items.some((item) => isImageAttachment(item));
  // 文本类文件由后端确定性提取后作为普通 text part 发送，不依赖 Provider 的原生 Files 能力。
  const supports = (model) => Boolean(model) && (!needsVision || model.capabilities?.vision);
  if (supports(chat)) return "";

  const imageID = modelStore.multimedia.imageModelID || "";
  const imageModel = modelStore.modelByID(imageID);
  if (supports(imageModel)) return "";

  const missing = [];
  if (needsVision) missing.push("Vision");
  return `当前 Chat 模型无法处理所选附件（需要 ${missing.join(" + ")}），且没有可用的图片理解模型。请先在“设置 → 多媒体”中配置。`;
}

function isImageAttachment(file) {
  const mimeType = String(file?.mimeType || file?.type || "").toLowerCase().split(";", 1)[0].trim();
  if (mimeType.startsWith("image/")) return true;
  const name = String(file?.name || "").toLowerCase();
  const dot = name.lastIndexOf(".");
  return dot >= 0 && IMAGE_ATTACHMENT_EXTENSIONS.has(name.slice(dot));
}

function isTextAttachment(file) {
  const mimeType = String(file?.type || "").toLowerCase().split(";", 1)[0].trim();
  if (mimeType.startsWith("text/") || TEXT_ATTACHMENT_MIME_TYPES.has(mimeType)) return true;
  if (mimeType && mimeType !== "application/octet-stream") return false;
  const name = String(file?.name || "").toLowerCase();
  const dot = name.lastIndexOf(".");
  return dot >= 0 && TEXT_ATTACHMENT_EXTENSIONS.has(name.slice(dot));
}

function formatSandbox(manifest) {
  const profile =
      manifest?.sandbox?.profile ||
      "--";
  const network =
      manifest?.sandbox?.networkMode ||
      "--";

  return `${profile} · ${network}`;
}

function formatRunPhase(phase) {
  switch (phase) {
    case "waiting_approval":
      return "等待审批";
    case "cancelling":
      return "取消中";
    case "running":
      return "运行中";
    case "maintaining":
      return "回答已完成，正在整理记忆";
    default:
      return "空闲";
  }
}

function formatCapabilitySummary(manifest) {
  const builtin =
      manifest?.builtinToolNames?.length ?? 0;
  const mcp =
      manifest?.mcpToolNames?.length ?? 0;
  const skills =
      manifest?.skillNames?.length ?? 0;

  return `内置 ${builtin} · MCP ${mcp} · Skills ${skills}`;
}

function formatNameList(values) {
  if (!Array.isArray(values) || values.length === 0) {
    return "无";
  }

  return values.join(", ");
}

function formatMCPServers(manifest) {
  const names =
      (manifest?.mcpServers ?? [])
          .map((server) => server?.serverName || server?.serverKey || "")
          .filter(Boolean);

  return formatNameList(names);
}

function formatUnavailableMCP(manifest) {
  const failures = Array.isArray(manifest?.mcpUnavailable)
      ? manifest.mcpUnavailable
      : [];

  return failures
      .map((item) => {
        const name = item?.serverName || item?.serverKey || "未知 Server";
        const error = String(item?.error || "当前不可用").trim();
        return `${name}: ${error}`;
      })
      .join("；");
}

function formatWorkspace(manifest) {
  const mode =
      manifest?.workspace?.mode ||
      "--";
  const root =
      manifest?.workspace?.rootDir ||
      "--";

  return `${mode} · ${root}`;
}

/**
 * 静默刷新当前 Session Context Usage。
 *
 * Session/Model 切换属于正常 UI 生命周期，读取失败不应该弹出全局错误打断用户；RuntimeStore
 * 会保留 contextError，Tooltip 会给出非阻塞提示。用户点击手动压缩时仍会展示真实错误。
 */
async function refreshContextUsage() {
  if (!sessionStore.selectedID) {
    return;
  }

  await runtimeStore
      .refreshContextUsage(
          sessionStore.selectedID,
          {
            silent: true,
          },
      );
}

/**
 * 主聊天页面切换当前 Agent Model。
 *
 * 更新 Agent.model_id 后：
 *
 * - 当前已经启动的 Turn 不受影响；
 * - 下一 Turn 由 RuntimeResolver 读取新模型；
 * - Context 环形进度立即按新模型的 Context Window / Tool Schema 重新计算。
 */
async function switchModel(
    modelID,
) {
  if (
      !modelID ||
      modelID ===
      selectedModelID.value
  ) {
    return;
  }

  const targetModel =
      modelStore
          .modelByID(modelID);

  switchingModel.value =
      true;

  try {
    await agentStore
        .switchSelectedModel(
            modelID,
        );

    await refreshContextUsage();

    Message.success(
        `已切换到 ${
            targetModel
                ?.displayName ??
            modelID
        }`,
    );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  } finally {
    switchingModel.value =
        false;
  }
}

function formatAttachmentSize(value) {
  const bytes = Number(value || 0);
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`;
}

function openAttachmentPicker() {
  if (!sessionStore.selectedID || running.value || sending.value) return;
  fileInput.value?.click();
}

function fileToBase64(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(reader.error || new Error(`读取 ${file.name} 失败`));
    reader.onload = () => {
      const value = String(reader.result || "");
      resolve(value.includes(",") ? value.slice(value.indexOf(",") + 1) : value);
    };
    reader.readAsDataURL(file);
  });
}

async function selectAttachments(event) {
  const input = event?.target;
  const files = Array.from(input?.files || []);
  if (input) input.value = "";
  if (files.length === 0) return;

  if (attachments.value.length + files.length > MAX_ATTACHMENTS) {
    Message.warning(`单条消息最多允许 ${MAX_ATTACHMENTS} 个附件`);
    return;
  }

  const candidateMetadata = [
    ...attachments.value,
    ...files.map((file) => ({
      name: file.name,
      mimeType: file.type || "application/octet-stream",
      sizeBytes: file.size
    })),
  ];
  const capabilityError = attachmentCapabilityError(candidateMetadata);
  if (capabilityError) {
    Message.warning(capabilityError);
    return;
  }

  let total = attachments.value.reduce((sum, item) => sum + Number(item.sizeBytes || 0), 0);
  const next = [];
  try {
    for (const file of files) {
      if (file.size <= 0) throw new Error(`${file.name} 是空文件`);
      if (file.size > MAX_ATTACHMENT_BYTES) throw new Error(`${file.name} 超过 12 MiB 限制`);
      const image = isImageAttachment(file);
      if (!image && !isTextAttachment(file)) {
        throw new Error(`${file.name} 暂不支持；当前文件附件仅支持图片、UTF-8 文本、源码和 JSON/YAML/XML 等文本格式`);
      }
      if (!image && file.size > MAX_TEXT_ATTACHMENT_BYTES) {
        throw new Error(`${file.name} 超过文本附件 512 KiB 限制`);
      }
      total += file.size;
      if (total > MAX_ATTACHMENT_TOTAL_BYTES) throw new Error("单条消息附件总大小不能超过 24 MiB");
      next.push({
        name: file.name,
        mimeType: file.type || "application/octet-stream",
        sizeBytes: file.size,
        base64Data: await fileToBase64(file),
      });
    }
  } catch (error) {
    Message.error(error?.message || String(error));
    return;
  }
  attachments.value = [...attachments.value, ...next];
}

function removeAttachment(index) {
  attachments.value = attachments.value.filter((_, current) => current !== index);
}

/**
 * 发送消息。
 *
 * RuntimeStore 会：
 *
 * 1. StartTurn；
 * 2. 等待 Runtime Event；
 * 3. Streaming；
 * 4. 最终重新加载 Session Transcript Message 与 Context Usage。
 */
async function send() {
  if (!canSend.value) {
    return;
  }

  const sessionID =
      sessionStore.selectedID;
  const content =
      sessionStore
          .draftForSession(
              sessionID,
          )
          .trim();
  const pendingAttachments = attachments.value.map((item) => ({...item}));
  const capabilityError = attachmentCapabilityError(pendingAttachments);
  if (capabilityError) {
    Message.warning(capabilityError);
    return;
  }

  sessionStore.clearDraft(
      sessionID,
  );
  attachmentDrafts.value = {
    ...attachmentDrafts.value,
    [sessionID]: [],
  };

  sending.value = true;

  try {
    await runtimeStore.send(
        sessionID,
        content,
        pendingAttachments.map(({name, mimeType, base64Data}) => ({name, mimeType, base64Data})),
    );
  } catch (error) {
    /* 启动失败时恢复文字和附件，避免用户输入丢失。 */
    sessionStore.setDraft(
        sessionID,
        content,
    );
    attachmentDrafts.value = {
      ...attachmentDrafts.value,
      [sessionID]: pendingAttachments,
    };

    Message.error(
        error?.message ??
        String(error),
    );
  } finally {
    sending.value = false;
  }
}

/**
 * 停止当前 Session Turn。
 */
async function stop() {
  try {
    await runtimeStore.cancel(
        sessionStore.selectedID,
    );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  }
}

/**
 * 执行 Context 菜单中的手动压缩动作。
 *
 * updateMemory=false 只产生 CompactionEntry；true 会在同一后端操作中额外刷新当前 Session
 * Memory。前端不会在运行中的 Turn 上强行压缩，后端仍会再次做 Session Busy 校验。
 */
async function runCompaction(
    updateMemory,
) {
  const sessionID =
      sessionStore.selectedID;

  if (!sessionID) {
    Message.warning(
        "请先选择一个会话",
    );
    return;
  }

  try {
    const result =
        await runtimeStore
            .compactSessionContext(
                sessionID,
                updateMemory,
            );

    const compacted =
        Boolean(
            result?.compaction
                ?.compacted,
        );
    const memoryUpdated =
        Boolean(
            result?.memory
                ?.updated,
        );

    if (compacted) {
      const before =
          formatTokens(
              result.compaction
                  ?.before
                  ?.usedTokens,
          );
      const after =
          formatTokens(
              result.compaction
                  ?.after
                  ?.usedTokens,
          );

      Message.success(
          updateMemory
              ? `Context 已压缩并更新 Session Memory · ${before} → ${after}`
              : `Context 已压缩 · ${before} → ${after}`,
      );
      return;
    }

    if (
        updateMemory &&
        memoryUpdated
    ) {
      Message.success(
          "当前没有可安全压缩的历史，Session Memory 已更新",
      );
      return;
    }

    Message.info(
        "当前没有可安全压缩的历史",
    );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  }
}

watch(
    () => [
      sessionStore.selectedID,
      selectedModelID.value,
    ],
    () => {
      void refreshContextUsage();
    },
    {
      immediate: true,
    },
);
</script>

<template>
  <!--
    Composer 是 ChatView 的正常第二部分。

    不使用：
      position: absolute
      position: fixed

    因此不会再覆盖或者被 MessageList 推出屏幕。
  -->
  <footer class="composer">
    <div class="composer-inner">
      <input
          ref="fileInput"
          type="file"
          multiple
          class="composer-file-input"
          @change="selectAttachments"
      />

      <div v-if="attachments.length" class="composer-attachments">
        <div
            v-for="(attachment, index) in attachments"
            :key="`${attachment.name}-${attachment.sizeBytes}-${index}`"
            class="composer-attachment"
        >
          <span class="composer-attachment__icon">{{ attachment.mimeType.startsWith('image/') ? '🖼' : '📎' }}</span>
          <span class="composer-attachment__body">
            <span class="composer-attachment__name">{{ attachment.name }}</span>
            <span class="composer-attachment__size">{{ formatAttachmentSize(attachment.sizeBytes) }}</span>
          </span>
          <button
              type="button"
              class="composer-attachment__remove"
              :aria-label="`移除 ${attachment.name}`"
              @click="removeAttachment(index)"
          >×
          </button>
        </div>
      </div>

      <a-textarea
          v-model="draft"
          :auto-size="{
          minRows: 2,
          maxRows: 6,
        }"
          :disabled="
          !sessionStore.selectedID
        "
          :placeholder="
          sessionStore.selectedID
            ? '给 Humbert 发送消息…'
            : '请先创建一个对话'
        "
          class="composer-textarea"
          @keydown.enter.exact.prevent="
          send
        "
      />

      <div
          class="composer-toolbar"
      >
        <div class="composer-context-area">
          <button
              type="button"
              class="composer-attach-button"
              :disabled="!sessionStore.selectedID || running || sending || attachments.length >= MAX_ATTACHMENTS"
              title="添加图片或文件"
              @click="openAttachmentPicker"
          >
            ＋ 附件
          </button>

          <!--
            Context 环形进度只展示 ContextEngine 的估算值，不自己重新计算 Token。
            Dropdown 的两个动作和后端 ManualCompact(updateMemory) 一一对应。
          -->
          <a-dropdown
              trigger="click"
              position="top"
              :disabled="
              !sessionStore.selectedID ||
              running ||
              contextCompacting
            "
          >
            <a-tooltip position="top">
              <button
                  type="button"
                  class="context-ring-button"
                  :class="{
                  'context-ring-button--warning':
                    contextUsage?.needsCompaction,
                  'context-ring-button--loading':
                    contextLoading || contextCompacting,
                }"
                  :disabled="!sessionStore.selectedID"
                  aria-label="查看 Context 使用情况与压缩选项"
              >
                <svg
                    class="context-ring"
                    viewBox="0 0 20 20"
                    aria-hidden="true"
                >
                  <circle
                      class="context-ring__track"
                      cx="10"
                      cy="10"
                      r="8"
                  />

                  <circle
                      class="context-ring__value"
                      cx="10"
                      cy="10"
                      r="8"
                      :style="{
                      strokeDashoffset:
                        contextRingOffset,
                    }"
                  />
                </svg>
              </button>

              <template #content>
                <div class="context-tooltip">
                  <template v-if="contextUsage">
                    <div class="context-tooltip__summary">
                      <div>
                        上下文
                        {{ formatTokens(contextUsage.contextWindow) }}
                      </div>

                      <div>
                        已用
                        {{ formatTokens(contextUsage.usedTokens) }}
                        ({{ Math.round(contextPercent) }}%)
                      </div>
                    </div>

                    <div class="context-tooltip__divider"/>

                    <div class="context-tooltip__breakdown">
                      <div class="context-tooltip__row">
                        <span>系统 / Agent</span>
                        <span>{{ formatTokens(contextUsage.systemTokens) }}</span>
                      </div>

                      <div class="context-tooltip__row">
                        <span>工具定义</span>
                        <span>{{ formatTokens(contextUsage.toolTokens) }}</span>
                      </div>

                      <div class="context-tooltip__row">
                        <span>对话消息</span>
                        <span>{{ formatTokens(contextUsage.messageTokens) }}</span>
                      </div>

                      <div class="context-tooltip__row">
                        <span>Session Memory</span>
                        <span>{{ formatTokens(contextUsage.memoryTokens) }}</span>
                      </div>

                      <div class="context-tooltip__row">
                        <span>压缩摘要</span>
                        <span>{{ formatTokens(contextUsage.checkpointTokens) }}</span>
                      </div>
                    </div>

                    <template v-if="contextAssembly">
                      <div class="context-tooltip__divider"/>

                      <div class="context-tooltip__breakdown context-tooltip__runtime">
                        <div class="context-tooltip__row">
                          <span>模型消息</span>
                          <span class="context-tooltip__value">{{
                              contextAssembly.visibleMessageCount
                            }} 条 · 近期 {{ contextAssembly.recentMessageCount }} 条</span>
                        </div>

                        <div class="context-tooltip__row">
                          <span>消息角色</span>
                          <span class="context-tooltip__value">User {{ contextAssembly.userMessageCount }} · Assistant {{
                              contextAssembly.assistantMessageCount
                            }} · Tool {{ contextAssembly.toolResultCount }}</span>
                        </div>

                        <div class="context-tooltip__row">
                          <span>Tool 事务</span>
                          <span class="context-tooltip__value">调用 {{
                              contextAssembly.toolCallCount
                            }} · 结果 {{ contextAssembly.toolResultCount }}</span>
                        </div>

                        <div class="context-tooltip__row">
                          <span>注入状态</span>
                          <span class="context-tooltip__value">Memory {{ contextAssembly.memoryInjected ? "是" : "否" }} · Checkpoint {{
                              contextAssembly.checkpointInjected ? "是" : "否"
                            }}</span>
                        </div>
                      </div>
                    </template>

                    <template v-if="contextManifest">
                      <div class="context-tooltip__divider"/>

                      <div class="context-tooltip__breakdown context-tooltip__runtime">
                        <div
                            v-if="activeRunState"
                            class="context-tooltip__row"
                        >
                          <span>当前 Turn</span>
                          <span class="context-tooltip__value">{{ formatRunPhase(activeRunState.phase) }}</span>
                        </div>

                        <div class="context-tooltip__row">
                          <span>模型</span>
                          <span class="context-tooltip__value">{{
                              formatRuntimeModel(contextManifest)
                            }} · {{ formatRuntimeModelRole(contextManifest) }}</span>
                        </div>

                        <div class="context-tooltip__row">
                          <span>模型能力</span>
                          <span class="context-tooltip__value">{{
                              formatModelCapabilities(contextManifest.modelCapabilities)
                            }}</span>
                        </div>

                        <div class="context-tooltip__row">
                          <span>Agent 能力</span>
                          <span class="context-tooltip__value">{{ formatCapabilitySummary(contextManifest) }}</span>
                        </div>

                        <div class="context-tooltip__row">
                          <span>模型角色</span>
                          <span class="context-tooltip__value"
                                :title="`Chat ${contextManifest.modelRoles?.chatModelID || '--'} · Utility ${contextManifest.modelRoles?.utilityModelID || '--'} · Memory ${contextManifest.modelRoles?.memoryModelID || '--'} · 图片 ${contextManifest.modelRoles?.imageModelID || '--'}`">Chat / Utility / Memory{{
                              contextManifest.modelRoles?.imageModelID ? ' / 图片' : ''
                            }}</span>
                        </div>

                        <div class="context-tooltip__row">
                          <span>Sandbox</span>
                          <span class="context-tooltip__value">{{ formatSandbox(contextManifest) }}</span>
                        </div>

                        <div
                            v-if="contextManifest.skillNames?.length"
                            class="context-tooltip__row"
                        >
                          <span>Skills</span>
                          <span
                              class="context-tooltip__value"
                              :title="formatNameList(contextManifest.skillNames)"
                          >{{ formatNameList(contextManifest.skillNames) }}</span>
                        </div>

                        <div
                            v-if="contextManifest.mcpServers?.length"
                            class="context-tooltip__row"
                        >
                          <span>MCP</span>
                          <span
                              class="context-tooltip__value"
                              :title="formatMCPServers(contextManifest)"
                          >{{ formatMCPServers(contextManifest) }}</span>
                        </div>

                        <div
                            v-if="contextManifest.mcpUnavailable?.length"
                            class="context-tooltip__row"
                        >
                          <span>MCP 降级</span>
                          <span
                              class="context-tooltip__value"
                              :title="formatUnavailableMCP(contextManifest)"
                          >{{ contextManifest.mcpUnavailable.length }} 个 Server 不可用</span>
                        </div>

                        <div class="context-tooltip__row">
                          <span>Workspace</span>
                          <span
                              class="context-tooltip__value"
                              :title="contextManifest.workspace?.rootDir || ''"
                          >{{ formatWorkspace(contextManifest) }}</span>
                        </div>
                      </div>
                    </template>

                    <div class="context-tooltip__divider"/>

                    <div class="context-tooltip__meta">
                      自动压缩阈值
                      {{ formatTokens(contextUsage.thresholdTokens) }}
                    </div>
                  </template>

                  <template v-else-if="contextError">
                    <div>Context 使用情况暂不可用</div>
                  </template>

                  <template v-else>
                    <div>正在计算 Context 使用情况…</div>
                  </template>
                </div>
              </template>
            </a-tooltip>

            <template #content>
              <a-doption
                  :disabled="running || contextCompacting"
                  @click="runCompaction(false)"
              >
                压缩
              </a-doption>

              <a-doption
                  :disabled="running || contextCompacting"
                  @click="runCompaction(true)"
              >
                压缩并更新
              </a-doption>
            </template>
          </a-dropdown>

          <span
              class="composer-hint"
          >
            Enter 发送 ·
            Shift+Enter 换行
          </span>
        </div>

        <div
            class="composer-actions"
        >
          <!--
            主页面真实 Model Switcher。

            读取共享 Pinia ModelStore，
            所以设置中新建 Model 后会直接出现在这里。
          -->
          <a-select
              :model-value="
              selectedModelID
            "
              :loading="
              modelStore.loading ||
              switchingModel
            "
              :disabled="
              !agentStore.selectedAgent
            "
              allow-search
              placeholder="选择模型"
              size="small"
              class="composer-model"
              @change="
              switchModel
            "
          >
            <a-option
                v-for="
                model in
                modelStore.enabledModels
              "
                :key="model.id"
                :value="model.id"
            >
              {{ model.displayName }}

              ·

              {{ model.providerName }}
            </a-option>
          </a-select>

          <a-button
              v-if="running"
              status="danger"
              shape="circle"
              @click="stop"
          >
            <template #icon>
              <IconStop/>
            </template>
          </a-button>

          <a-button
              v-else
              type="primary"
              shape="circle"
              :loading="sending"
              :disabled="!canSend"
              @click="send"
          >
            <template #icon>
              <IconSend/>
            </template>
          </a-button>
        </div>
      </div>
    </div>
  </footer>
</template>

<style scoped>
.composer {
  flex: 0 0 auto;

  width: 100%;

  padding: 12px 32px 20px;

  border-top: 1px solid var(--h-border);

  background: var(--h-bg);
}

.composer-inner {
  width: 100%;
  max-width: 840px;

  margin: 0 auto;

  padding: 10px;

  border: 1px solid var(--h-border-strong);

  border-radius: 14px;

  background: var(--h-surface);
}


.composer-file-input {
  display: none;
}

.composer-attachments {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
  margin-bottom: 8px;
}

.composer-attachment {
  display: flex;
  max-width: 260px;
  align-items: center;
  gap: 7px;
  padding: 6px 8px;
  border: 1px solid var(--h-border);
  border-radius: 8px;
  background: var(--h-surface);
}

.composer-attachment__body {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-direction: column;
}

.composer-attachment__name {
  overflow: hidden;
  color: var(--h-text);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.composer-attachment__size {
  color: var(--h-text-secondary);
  font-size: 9px;
}

.composer-attachment__remove,
.composer-attach-button {
  border: 0;
  background: transparent;
  color: var(--h-text-secondary);
  cursor: pointer;
}

.composer-attachment__remove:hover,
.composer-attach-button:hover:not(:disabled) {
  color: var(--h-accent);
}

.composer-attach-button {
  padding: 2px 4px;
  font-size: 11px;
}

.composer-attach-button:disabled {
  cursor: not-allowed;
  opacity: .45;
}

.composer-toolbar {
  display: flex;

  align-items: center;
  justify-content: space-between;

  gap: 12px;

  margin-top: 6px;
}

.composer-context-area {
  display: flex;

  min-width: 0;

  align-items: center;

  gap: 9px;
}

.composer-hint {
  color: var(--h-text-muted);

  font-size: 10px;
}

.context-ring-button {
  display: inline-flex;

  width: 26px;
  height: 26px;

  flex: 0 0 26px;

  align-items: center;
  justify-content: center;

  padding: 0;

  border: 0;

  border-radius: 999px;

  background: transparent;

  color: var(--h-text-muted);

  cursor: pointer;

  transition: background 140ms ease,
  color 140ms ease,
  opacity 140ms ease;
}

.context-ring-button:hover:not(:disabled) {
  background: var(--h-surface-hover);

  color: var(--h-text);
}

.context-ring-button:disabled {
  cursor: default;

  opacity: 0.45;
}

.context-ring-button--warning {
  color: var(--h-warning, var(--h-text));
}

.context-ring-button--loading {
  opacity: 0.62;
}

.context-ring-button--loading .context-ring {
  animation: context-ring-spin 900ms linear infinite;
}

.context-ring {
  width: 20px;
  height: 20px;

  transform: rotate(-90deg);
}

.context-ring__track,
.context-ring__value {
  fill: none;

  stroke-width: 3;
}

.context-ring__track {
  stroke: currentColor;

  opacity: 0.18;
}

.context-ring__value {
  stroke: currentColor;

  stroke-linecap: round;

  stroke-dasharray: 50.2655;

  transition: stroke-dashoffset 180ms ease;
}

.context-tooltip {
  min-width: 260px;

  max-width: 360px;

  font-size: 12px;

  line-height: 1.55;
}

.context-tooltip__summary {
  font-weight: 500;
}

.context-tooltip__divider {
  height: 1px;

  margin: 7px 0;

  background: rgba(255, 255, 255, 0.16);
}

.context-tooltip__breakdown {
  display: grid;

  gap: 3px;
}

.context-tooltip__row {
  display: flex;

  align-items: center;
  justify-content: space-between;

  gap: 18px;
}

.context-tooltip__row span:first-child,
.context-tooltip__meta {
  opacity: 0.76;
}

.context-tooltip__row span:last-child {
  font-variant-numeric: tabular-nums;
}

.context-tooltip__value {
  max-width: 220px;

  overflow: hidden;

  text-align: right;

  text-overflow: ellipsis;

  white-space: nowrap;
}

.context-tooltip__runtime {
  gap: 4px;
}

.context-tooltip__meta {
  white-space: nowrap;
}

.composer-actions {
  display: flex;

  min-width: 0;

  align-items: center;

  gap: 8px;
}

.composer-model {
  width: 210px;
}

/*
 * Composer 外层本身已经提供 Border，
 * Textarea 不应该出现第二层输入框边界。
 */
:deep(
  .composer-textarea.arco-textarea-wrapper
) {
  border: 0 !important;

  background: transparent !important;

  box-shadow: none !important;
}

:deep(
  .composer-textarea textarea
) {
  padding: 5px 6px;

  resize: none;

  background: transparent;

  color: var(--h-text);

  font-size: 14px;

  line-height: 1.65;
}

:deep(
  .composer-textarea
    textarea::placeholder
) {
  color: var(--h-text-muted);
}

@keyframes context-ring-spin {
  from {
    transform: rotate(-90deg);
  }

  to {
    transform: rotate(270deg);
  }
}

@media (
max-width: 800px
) {
  .composer {
    padding-right: 20px;

    padding-left: 20px;
  }

  .composer-hint {
    display: none;
  }

  .composer-model {
    width: 170px;
  }

  .composer-actions {
    margin-left: auto;
  }
}
</style>
