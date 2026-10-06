<script setup>
import { computed, watch } from "vue";
import { Message } from "../../utils/uiMessage.js";
import { useMenuTooltip } from "../../utils/menuTooltip.js";
import { useAgentStore } from "../../stores/agents.js";
import { useRuntimeStore } from "../../stores/runtime.js";
import { useSessionStore } from "../../stores/sessions.js";

// 菜单只拥有上下文展示和手动压缩交互。运行状态、预算和消息事实仍由 Runtime Store 持有。
const agents = useAgentStore();
const runtime = useRuntimeStore();
const sessions = useSessionStore();
const {
  tooltipVisible: contextTooltipVisible,
  onTooltipVisibleChange: onContextTooltipVisibleChange,
  dismissTooltip: dismissContextTooltip,
  onMenuVisibleChange: onContextMenuVisibleChange,
  onTriggerLeave: onContextTriggerLeave,
} = useMenuTooltip();
const sessionStore = sessions;
const running = computed(() => runtime.isSessionRunning(sessions.selectedID));
const contextUsage = computed(() => runtime.contextUsage(sessions.selectedID));
const contextAssembly = computed(() => runtime.contextAssembly(sessions.selectedID));
const activeRunState = computed(() => runtime.runState(sessions.selectedID));
const contextLoading = computed(() => runtime.isContextLoading(sessions.selectedID));
const contextCompacting = computed(() => runtime.isContextCompacting(sessions.selectedID));
const contextError = computed(() => runtime.contextError(sessions.selectedID));
// 执行中的 Turn 使用启动时冻结的 Manifest，不能被下一轮本地能力投影覆盖。
const contextManifest = computed(() => activeRunState.value?.runtime || runtime.contextManifest(sessions.selectedID));
const contextPercent = computed(() => {
  const value = Number(contextUsage.value?.percent || 0);
  return Number.isFinite(value) ? Math.min(100, Math.max(0, value)) : 0;
});
const contextRingOffset = computed(() => 2 * Math.PI * 8 * (1 - contextPercent.value / 100));

function formatTokens(value) {
  const tokens = Number(value);
  if (!Number.isFinite(tokens) || tokens < 0) return "--";
  return tokens >= 1000 ? `${Math.round(tokens / 1000)}k` : String(Math.round(tokens));
}
function formatRuntimeModel(manifest) { return manifest?.modelDisplayName || manifest?.modelID || "未配置"; }
function formatRuntimeModelRole(manifest) {
  return (manifest?.modelRole || manifest?.modelRoles?.activeRole || "chat") === "image" ? "图片" : "Chat";
}
function formatModelCapabilities(capabilities) {
  if (!capabilities) return "--";
  const names = [["tools", "Tools"], ["vision", "Vision"], ["files", "Files"], ["reasoning", "Reasoning"], ["json", "JSON"], ["audio", "Audio"]]
    .filter(([key]) => capabilities[key]).map(([, label]) => label);
  return names.join(" · ") || "无已声明能力";
}
function formatSandbox(manifest) { return `${manifest?.sandbox?.profile || "--"} · ${manifest?.sandbox?.networkMode || "--"}`; }
function formatRunPhase(phase) {
  return ({ waiting_approval: "等待审批", cancelling: "取消中", running: "运行中", maintaining: "回答已完成，正在整理记忆" })[phase] || "空闲";
}
function formatCapabilitySummary(manifest) {
  return `内置 ${manifest?.builtinToolNames?.length || 0} · MCP ${manifest?.mcpToolNames?.length || 0} · Skills ${manifest?.skillNames?.length || 0}`;
}
function formatNameList(values) { return Array.isArray(values) && values.length ? values.join(", ") : "无"; }
function formatMCPServers(manifest) { return formatNameList((manifest?.mcpServers || []).map(server => server?.serverName || server?.serverKey || "").filter(Boolean)); }
function formatUnavailableMCP(manifest) {
  return (manifest?.mcpUnavailable || []).map(item => `${item?.serverName || item?.serverKey || "未知 Server"}: ${String(item?.error || "当前不可用").trim()}`).join("；");
}
function formatWorkspace(manifest) { return `${manifest?.workspace?.mode || "--"} · ${manifest?.workspace?.rootDir || "--"}`; }

/** 手动压缩复用后端会话占用与摘要入口，不能与当前 Turn 并发改写上下文。 */
async function runCompaction() {
  const id = sessions.selectedID;
  if (!id) { Message.warning("请先选择一个会话"); return; }
  try {
    const result = await runtime.compactSessionContext(id);
    if (result?.compaction?.compacted) {
      Message.success(`Context 已压缩 · ${formatTokens(result.compaction.before?.usedTokens)} → ${formatTokens(result.compaction.after?.usedTokens)}`);
    } else Message.info("当前没有可安全压缩的历史");
  } catch (error) {
    Message.error(error?.message || String(error));
  }
}

// 切换会话或模型时静默重读，错误由 Store 留在当前会话，菜单展示非阻塞错误态。
// 模型切换按钮不再额外发起同一查询；这里是该界面生命周期的唯一刷新入口。
watch(() => [sessions.selectedID, agents.selectedAgent?.modelID], () => {
  if (sessions.selectedID) void runtime.refreshContextUsage(sessions.selectedID, { silent: true });
}, { immediate: true });
</script>

<template>
<span
    class="composer-context-control"
    @pointerdown.capture="dismissContextTooltip"
    @mouseleave="onContextTriggerLeave"
>
  <a-dropdown
      trigger="click"
      position="top"
      @popup-visible-change="onContextMenuVisibleChange"
      :disabled="
      !sessionStore.selectedID ||
      running ||
      contextCompacting
    "
  >
    <a-tooltip
        position="top"
        :popup-visible="contextTooltipVisible"
        @popup-visible-change="onContextTooltipVisibleChange"
    >
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
                  <span class="context-tooltip__value">Checkpoint {{
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
                        :title="`Chat ${contextManifest.modelRoles?.chatModelID || '--'} · Utility ${contextManifest.modelRoles?.utilityModelID || '--'} · 图片 ${contextManifest.modelRoles?.imageModelID || '--'}`">Chat / Utility{{
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
                <div v-for="module in contextManifest.extensions || []" :key="module.id" class="context-tooltip__row">
                  <span>{{ module.id }}</span>
                  <span class="context-tooltip__value" :title="formatNameList(module.toolNames)">{{ formatNameList(module.toolNames) }}</span>
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
          @click="runCompaction()"
      >
        压缩
      </a-doption>
    </template>
  </a-dropdown>
</span>
</template>
<style scoped>
.composer-context-control {
  display: inline-flex;
  flex: 0 0 auto;
}
.context-ring-button {
  display: inline-flex;
  width: 32px;
  height: 32px;
  flex: 0 0 32px;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: 0;
  border-radius: 8px;
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
.context-ring-button:focus-visible {
  outline: 2px solid var(--h-accent-border);
  outline-offset: 2px;
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
  width: 18px;
  height: 18px;
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
@keyframes context-ring-spin {
  from {
    transform: rotate(-90deg);
  }
  to {
    transform: rotate(270deg);
  }
}
@media (prefers-reduced-motion: reduce) {
  .context-ring-button, .context-ring__value { transition: none; }
  .context-ring-button--loading .context-ring { animation: none; }
}
</style>
