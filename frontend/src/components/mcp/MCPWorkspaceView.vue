<script setup>
import {
  computed,
  onMounted,
  reactive,
  ref,
  watch,
} from "vue";

import {
  Message,
} from "@arco-design/web-vue";

import {
  IconDown,
  IconLink,
  IconRefresh,
  IconSettings,
  IconThunderbolt,
} from "@arco-design/web-vue/es/icon";

import MCPToolDetailView from "./MCPToolDetailView.vue";

import AppPageHeader from "../ui/AppPageHeader.vue";
import EmptyState from "../ui/EmptyState.vue";
import StatusPill from "../ui/StatusPill.vue";

import {
  discoverMCPTools,
  refreshMCPTools,
  getAgentMCPTools,
  listMCPServers,
  setAgentMCPTools,
  setMCPToolRisk,
  testMCPConnection,
} from "../../api/mcp.js";

import {
  useAgentStore,
} from "../../stores/agents.js";

import {
  mcpConnectionStateLabel,
  mcpConnectionStateTone,
  mcpRiskLabel,
  mcpTransportLabel,
} from "../../utils/mcp.js";

const emit = defineEmits([
  "manage-servers",
]);

const agentStore = useAgentStore();

const servers = ref([]);
const selectedAgentID = ref("");
const selections = ref([]);
const loading = ref(false);
const savingSelection = ref(false);
const connectionState = reactive({});
const toolCatalog = reactive({});
const catalogLoading = reactive({});
const expandedServers = reactive({});
const selectedToolDetail = ref(null);

const selectedAgent = computed(() => (
    agentStore.items.find((item) => item.id === selectedAgentID.value) ?? null
));

// UI 中的 Project ID 就是 Agent ID。
const selectedAgentDomainID = computed(() => (
    selectedAgent.value?.id || ""
));

const selectedToolCount = computed(() => (
    selections.value.reduce(
        (count, selection) => count + (Array.isArray(selection?.tools) ? selection.tools.length : 0),
        0,
    )
));

const detailServer = computed(() => {
  const serverID = selectedToolDetail.value?.serverID;
  return servers.value.find((item) => item.id === serverID) ?? null;
});

const detailTool = computed(() => {
  const serverID = selectedToolDetail.value?.serverID;
  const rawName = selectedToolDetail.value?.rawName;
  const catalog = Array.isArray(toolCatalog[serverID]) ? toolCatalog[serverID] : [];
  return catalog.find((item) => item.rawName === rawName) ?? null;
});


function normaliseServers(values) {
  return Array.isArray(values) ? values : [];
}

function normaliseSelections(values) {
  return Array.isArray(values)
      ? values.map((value) => ({
        serverID: value?.serverID ?? "",
        tools: Array.isArray(value?.tools) ? [...value.tools] : [],
      }))
      : [];
}

async function loadServers() {
  loading.value = true;
  try {
    servers.value = normaliseServers(await listMCPServers());
    const serverIDs = new Set(servers.value.map((server) => server?.id).filter(Boolean));
    for (const key of Object.keys(toolCatalog)) {
      if (!serverIDs.has(key)) delete toolCatalog[key];
    }
    for (const key of Object.keys(catalogLoading)) {
      if (!serverIDs.has(key)) delete catalogLoading[key];
    }
    for (const key of Object.keys(expandedServers)) {
      if (!serverIDs.has(key)) delete expandedServers[key];
    }
    for (const key of Object.keys(connectionState)) delete connectionState[key];
    for (const server of servers.value) {
      const state = server?.connectionState || (server?.enabled === false ? "disabled" : "disconnected");
      const hasError = Boolean(server?.lastError) && (state === "error" || state === "degraded");
      connectionState[server.id] = {
        ok: state === "connected",
        tone: mcpConnectionStateTone(state),
        text: mcpConnectionStateLabel(state),
        detail: hasError ? server.lastError : "",
      };
    }
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    loading.value = false;
  }
}

async function loadAgentSelection() {
  if (!selectedAgentID.value) {
    selections.value = [];
    return;
  }

  try {
    selections.value = normaliseSelections(
        await getAgentMCPTools(selectedAgentDomainID.value),
    );
  } catch (error) {
    selections.value = [];
    Message.error(error?.message ?? String(error));
  }
}

function selectedTools(serverID) {
  return selections.value.find((item) => item.serverID === serverID)?.tools ?? [];
}

function isToolEnabled(serverID, rawName) {
  return selectedTools(serverID).includes(rawName);
}

function discoveredTools(serverID) {
  return Array.isArray(toolCatalog[serverID]) ? toolCatalog[serverID] : [];
}

function allDiscoveredToolsEnabled(serverID) {
  const catalog = discoveredTools(serverID);
  return catalog.length > 0 && catalog.every((tool) => isToolEnabled(serverID, tool.rawName));
}

function unavailableSelectedTools(serverID) {
  if (!Array.isArray(toolCatalog[serverID])) return [];

  const enabled = selectedTools(serverID);
  const discovered = new Set(toolCatalog[serverID].map((item) => item.rawName));
  return enabled.filter((name) => !discovered.has(name));
}

function isServerExpanded(serverID) {
  return Boolean(expandedServers[serverID]);
}

function toggleServer(serverID) {
  if (!serverID) return;
  expandedServers[serverID] = !expandedServers[serverID];
}

function openToolDetail(server, tool) {
  if (!server?.id || !tool?.rawName) return;
  selectedToolDetail.value = {
    serverID: server.id,
    rawName: tool.rawName,
  };
}

function closeToolDetail() {
  selectedToolDetail.value = null;
}

async function updateToolRisk(serverID, rawName, risk) {
  if (!serverID || !rawName || savingSelection.value) return;
  savingSelection.value = true;
  try {
    await setMCPToolRisk(serverID, rawName, risk);
    const catalog = Array.isArray(toolCatalog[serverID]) ? toolCatalog[serverID] : [];
    const item = catalog.find((value) => value.rawName === rawName);
    if (item) {
      item.risk = risk;
      item.riskOverridden = risk !== "write";
    }
    Message.success("MCP Tool 风险级别已保存，将从下一轮 Runtime Snapshot 生效");
  } catch (error) {
    Message.error(error?.message ?? String(error));
    await refreshTools(servers.value.find((value) => value.id === serverID), false);
  } finally {
    savingSelection.value = false;
  }
}

async function refreshDetailTool() {
  const server = detailServer.value;
  if (!server) return;
  await refreshTools(server);
  if (!detailTool.value) {
    Message.warning("这个 Tool 已不在 Server 当前 Catalog 中");
    closeToolDetail();
  }
}

async function toggleTool(serverID, rawName, enabled) {
  if (!selectedAgentID.value || savingSelection.value) return;

  const next = normaliseSelections(selections.value);
  let selection = next.find((item) => item.serverID === serverID);
  if (!selection) {
    selection = {
      serverID,
      tools: [],
    };
    next.push(selection);
  }

  const toolSet = new Set(selection.tools);
  if (enabled) toolSet.add(rawName);
  else toolSet.delete(rawName);
  selection.tools = [...toolSet].sort();

  const compact = next.filter((item) => item.tools.length > 0);
  savingSelection.value = true;
  try {
    await setAgentMCPTools(selectedAgentDomainID.value, compact);
    selections.value = compact;
    Message.success(
        enabled
            ? `已为 ${selectedAgent.value?.name || "当前 Agent"} 启用 Tool`
            : `已为 ${selectedAgent.value?.name || "当前 Agent"} 停用 Tool`,
    );
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    savingSelection.value = false;
  }
}

async function toggleAllTools(serverID, enabled) {
  if (!selectedAgentID.value || savingSelection.value) return;

  const catalog = discoveredTools(serverID);
  if (enabled && catalog.length === 0) {
    Message.warning("当前 Server 没有可启用的 Tool");
    return;
  }

  const next = normaliseSelections(selections.value);
  const existing = next.find((item) => item.serverID === serverID);
  if (enabled) {
    const selection = existing ?? {serverID, tools: []};
    if (!existing) next.push(selection);
    const toolSet = new Set(selection.tools);
    for (const tool of catalog) {
      if (tool?.rawName) toolSet.add(tool.rawName);
    }
    selection.tools = [...toolSet].sort();
  } else {
    const index = next.findIndex((item) => item.serverID === serverID);
    if (index >= 0) next.splice(index, 1);
  }

  const compact = next.filter((item) => item.tools.length > 0);
  savingSelection.value = true;
  try {
    await setAgentMCPTools(selectedAgentDomainID.value, compact);
    selections.value = compact;
    Message.success(
        enabled
            ? `已为 ${selectedAgent.value?.name || "当前 Agent"} 启用这个 Server 当前发现的全部 Tool`
            : `已停用 ${selectedAgent.value?.name || "当前 Agent"} 在这个 Server 下的全部 Tool`,
    );
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    savingSelection.value = false;
  }
}

async function refreshTools(server, announce = true, force = true) {
  if (!server?.id || catalogLoading[server.id]) return;
  if (server?.enabled === false) {
    Message.warning(`「${server.name}」已停用，请先到设置中启用`);
    return;
  }

  catalogLoading[server.id] = true;
  connectionState[server.id] = {
    ok: false,
    tone: "pending",
    text: "正在连接",
    detail: force ? "正在刷新 Tool Catalog…" : "正在读取 Tool Catalog…",
  };
  try {
    const values = force ? await refreshMCPTools(server.id) : await discoverMCPTools(server.id);
    toolCatalog[server.id] = Array.isArray(values) ? values : [];
    expandedServers[server.id] = true;
    server.connectionState = "connected";
    server.connected = true;
    server.lastError = "";
    connectionState[server.id] = {
      ok: true,
      tone: "ok",
      text: "已连接",
      detail: `已读取 ${toolCatalog[server.id].length} 个 Tool`,
    };
    if (announce) Message.success(`已读取 ${toolCatalog[server.id].length} 个 Tool`);
  } catch (error) {
    const message = error?.message ?? String(error);
    server.connectionState = "error";
    server.connected = false;
    server.lastError = message;
    connectionState[server.id] = {
      ok: false,
      tone: "error",
      text: "连接失败",
      detail: message,
    };
    Message.error(message);
  } finally {
    catalogLoading[server.id] = false;
  }
}

async function testServer(server) {
  if (!server?.id || catalogLoading[server.id]) return;
  if (server?.enabled === false) {
    Message.warning(`「${server.name}」已停用，请先到设置中启用`);
    return;
  }

  catalogLoading[server.id] = true;
  connectionState[server.id] = {
    ok: false,
    tone: "pending",
    text: "正在连接",
    detail: "正在执行 initialize + tools/list…",
  };
  try {
    const result = await testMCPConnection(server.id);
    server.connectionState = "connected";
    server.connected = true;
    server.lastError = "";
    connectionState[server.id] = {
      ok: true,
      tone: "ok",
      text: "已连接",
      detail: `${result?.toolCount ?? 0} 个 Tool · ${result?.durationMS ?? 0}ms`,
    };
    Message.success(`「${server.name}」连接成功`);
  } catch (error) {
    const message = error?.message ?? String(error);
    server.connectionState = "error";
    server.connected = false;
    server.lastError = message;
    connectionState[server.id] = {
      ok: false,
      tone: "error",
      text: "连接失败",
      detail: message,
    };
    Message.error(message);
  } finally {
    catalogLoading[server.id] = false;
  }
}

watch(
    () => agentStore.selectedID,
    (value) => {
      if (!selectedAgentID.value && value) selectedAgentID.value = value;
    },
    {immediate: true},
);

watch(selectedAgentID, () => void loadAgentSelection());

onMounted(async () => {
  if (agentStore.items.length === 0) await agentStore.load();
  if (!selectedAgentID.value) {
    selectedAgentID.value = agentStore.selectedID || agentStore.items[0]?.id || "";
  }
  await Promise.all([
    loadServers(),
    loadAgentSelection(),
  ]);
});
</script>

<template>
  <section class="mcp-workspace">
    <div class="mcp-workspace__scroll">
      <MCPToolDetailView
          v-if="selectedToolDetail && detailServer && detailTool"
          :server="detailServer"
          :tool="detailTool"
          :agent-name="selectedAgent?.name || '当前 Agent'"
          :enabled="isToolEnabled(detailServer.id, detailTool.rawName)"
          :saving="savingSelection"
          @back="closeToolDetail"
          @refresh="refreshDetailTool"
          @toggle="(value) => toggleTool(detailServer.id, detailTool.rawName, value)"
          @set-risk="(risk) => updateToolRisk(detailServer.id, detailTool.rawName, risk)"
      />

      <div v-else class="mcp-workspace__inner">
        <AppPageHeader
            eyebrow="Agent Connectors"
            title="连接器"
            description="选择 Agent / 项目，为它启用 MCP Server 提供的 Tool。Server 的安装与连接参数统一放在设置中管理。"
        >
          <template #aside>
            <div class="mcp-workspace__legend">
              <span class="mcp-workspace__legend-dot"></span>
              <span>Tool 开关从下一轮 Runtime Snapshot 开始生效</span>
            </div>
          </template>
        </AppPageHeader>

        <section class="mcp-control-panel">
          <div class="mcp-control-panel__top">
            <div class="mcp-control-panel__copy">
              <h2>为 Agent 配置 MCP Tools</h2>
              <p>
                这里仅负责 Agent 能力选择。添加、修改或删除 MCP Server，请进入「设置 → 连接器」。
              </p>
            </div>

            <div class="mcp-control-panel__actions">
              <a-button :loading="loading" @click="loadServers">
                <template #icon>
                  <IconRefresh/>
                </template>
                刷新
              </a-button>

              <a-button type="primary" @click="emit('manage-servers')">
                <template #icon>
                  <IconSettings/>
                </template>
                管理连接器
              </a-button>
            </div>
          </div>

          <div class="agent-picker">
            <div class="agent-picker__identity">
              <span class="agent-picker__avatar" aria-hidden="true">
                {{ (selectedAgent?.name || "A").slice(0, 1).toUpperCase() }}
              </span>

              <div class="agent-picker__copy">
                <span>当前 Agent / 项目</span>
                <small>
                  {{ selectedAgent ? `已启用 ${selectedToolCount} 个 MCP Tool` : "创建 Agent 后即可在这里配置" }}
                </small>
              </div>
            </div>

            <a-select
                v-model="selectedAgentID"
                class="agent-picker__select"
                :disabled="agentStore.items.length === 0"
                placeholder="选择 Agent / 项目"
            >
              <a-option
                  v-for="agent in agentStore.items"
                  :key="agent.id"
                  :value="agent.id"
              >
                {{ agent.name || agent.id }}
              </a-option>
            </a-select>
          </div>

          <div class="mcp-browse-hint">
            <IconLink/>
            <span>
              先读取 Server 的 Tools，再为当前 Agent 开启需要的能力。新发现的 Tool 默认保持关闭。
            </span>
          </div>
        </section>

        <EmptyState
            v-if="servers.length === 0 && !loading"
            title="还没有连接器"
            description="先到设置中添加一个 MCP Server，然后回来为 Agent 选择 Tool。"
        >
          <template #icon>
            <IconLink/>
          </template>
          <template #actions>
            <a-button type="primary" @click="emit('manage-servers')">前往设置</a-button>
          </template>
        </EmptyState>

        <div v-else class="mcp-server-list">
          <article
              v-for="server in servers"
              :key="server.id"
              class="mcp-server-card"
              :class="{ 'mcp-server-card--disabled': server.enabled === false }"
          >
            <header class="mcp-server-card__header">
              <div class="mcp-server-card__identity">
                <span class="mcp-server-card__icon"><IconLink/></span>
                <div class="mcp-server-card__copy">
                  <div class="mcp-server-card__title-row">
                    <strong>{{ server.name }}</strong>
                    <code>{{ server.key }}</code>
                    <span>{{ mcpTransportLabel(server.transport) }}</span>
                    <span v-if="server.enabled === false" class="mcp-server-card__disabled-chip">已停用</span>
                  </div>
                  <p>
                    {{ selectedTools(server.id).length }} 个 Tool 已为 {{ selectedAgent?.name || "当前 Agent" }} 启用
                    <template v-if="Array.isArray(toolCatalog[server.id])">
                      · 当前发现 {{ toolCatalog[server.id].length }} 个
                    </template>
                  </p>
                </div>
              </div>

              <div class="mcp-server-card__actions">
                <StatusPill
                    v-if="connectionState[server.id]"
                    :label="connectionState[server.id].text"
                    :tone="connectionState[server.id].tone || 'muted'"
                />

                <a-button
                    size="small"
                    :loading="catalogLoading[server.id]"
                    :disabled="server.enabled === false"
                    @click="testServer(server)"
                >
                  <template #icon>
                    <IconThunderbolt/>
                  </template>
                  {{ ["error", "warning"].includes(connectionState[server.id]?.tone) ? "重试" : "测试" }}
                </a-button>

                <a-button
                    size="small"
                    :loading="catalogLoading[server.id]"
                    :disabled="server.enabled === false"
                    @click="refreshTools(server)"
                >
                  <template #icon>
                    <IconRefresh/>
                  </template>
                  {{ Array.isArray(toolCatalog[server.id]) ? "刷新 Tools" : "读取 Tools" }}
                </a-button>

                <a-button
                    v-if="Array.isArray(toolCatalog[server.id])"
                    size="small"
                    class="mcp-server-card__collapse"
                    :aria-expanded="isServerExpanded(server.id)"
                    @click="toggleServer(server.id)"
                >
                  {{ isServerExpanded(server.id) ? "收起" : `展开 ${toolCatalog[server.id].length} 个 Tools` }}
                  <template #icon>
                    <IconDown
                        class="mcp-server-card__chevron"
                        :class="{ 'mcp-server-card__chevron--open': isServerExpanded(server.id) }"
                    />
                  </template>
                </a-button>
              </div>
            </header>

            <div
                v-if="connectionState[server.id]?.detail"
                class="mcp-server-card__diagnostic"
                :class="{
                  'mcp-server-card__diagnostic--error': connectionState[server.id]?.tone === 'error',
                  'mcp-server-card__diagnostic--warning': connectionState[server.id]?.tone === 'warning',
                }"
                :title="connectionState[server.id]?.detail"
            >
              <span class="mcp-server-card__diagnostic-dot" aria-hidden="true"></span>
              <span>{{ connectionState[server.id]?.detail }}</span>
            </div>

            <div
                v-if="isServerExpanded(server.id) && unavailableSelectedTools(server.id).length > 0"
                class="mcp-stale-tools"
            >
              <div class="mcp-stale-tools__title">已启用但当前未发现</div>

              <div
                  v-for="rawName in unavailableSelectedTools(server.id)"
                  :key="`stale-${rawName}`"
                  class="mcp-tool-row mcp-tool-row--warning"
              >
                <div class="mcp-tool-row__body">
                  <strong>{{ rawName }}</strong>
                  <p>Server 当前没有返回这个 Tool，可以从当前 Agent 配置中将它关闭。</p>
                </div>

                <div class="mcp-tool-row__toggle" @click.stop>
                  <span>已启用</span>
                  <a-switch
                      :model-value="true"
                      :disabled="!selectedAgentID || savingSelection"
                      @change="(value) => toggleTool(server.id, rawName, value)"
                  />
                </div>
              </div>
            </div>

            <div
                v-if="Array.isArray(toolCatalog[server.id]) && isServerExpanded(server.id)"
                class="mcp-tool-list"
            >
              <div class="mcp-tool-list__toolbar">
                <span>
                  已发现 {{ toolCatalog[server.id].length }} 个 · 当前 Agent 已启用 {{
                    selectedTools(server.id).length
                  }} 个
                </span>

                <div class="mcp-tool-list__bulk-actions">
                  <a-button
                      type="text"
                      size="mini"
                      :disabled="!selectedAgentID || savingSelection || toolCatalog[server.id].length === 0 || allDiscoveredToolsEnabled(server.id)"
                      @click.stop="toggleAllTools(server.id, true)"
                  >
                    启用已发现
                  </a-button>
                  <a-button
                      type="text"
                      size="mini"
                      :disabled="!selectedAgentID || savingSelection || selectedTools(server.id).length === 0"
                      @click.stop="toggleAllTools(server.id, false)"
                  >
                    全部停用
                  </a-button>
                </div>
              </div>

              <div v-if="toolCatalog[server.id].length === 0" class="mcp-tool-empty">
                Server 当前没有暴露 Tool。
              </div>

              <div
                  v-for="tool in toolCatalog[server.id]"
                  :key="tool.rawName"
                  class="mcp-tool-row mcp-tool-row--clickable"
                  :class="{ 'mcp-tool-row--enabled': isToolEnabled(server.id, tool.rawName) }"
                  role="button"
                  tabindex="0"
                  @click="openToolDetail(server, tool)"
                  @keydown.enter.prevent="openToolDetail(server, tool)"
              >
                <div class="mcp-tool-row__body">
                  <div class="mcp-tool-row__name">
                    <strong>{{ tool.rawName }}</strong>
                    <code>{{ tool.exposedName }}</code>
                  </div>
                  <p>{{ tool.description || "这个 MCP Tool 没有提供描述。" }}</p>
                  <div class="mcp-tool-row__meta">
                    <span>风险：{{ mcpRiskLabel(tool.risk) }}{{ tool.riskOverridden ? " · 已覆盖" : " · 默认" }}</span>
                    <span>查看详情 ›</span>
                  </div>
                </div>

                <div class="mcp-tool-row__toggle" @click.stop>
                  <span>{{ isToolEnabled(server.id, tool.rawName) ? "已启用" : "未启用" }}</span>
                  <a-switch
                      :model-value="isToolEnabled(server.id, tool.rawName)"
                      :disabled="!selectedAgentID || savingSelection"
                      @change="(value) => toggleTool(server.id, tool.rawName, value)"
                  />
                </div>
              </div>
            </div>

            <button
                v-else-if="!Array.isArray(toolCatalog[server.id])"
                type="button"
                class="mcp-discover-hint"
                :disabled="server.enabled === false"
                @click="refreshTools(server)"
            >
              <span>{{
                  server.enabled === false ? "Server 已停用；Tool 选择会保留" : "读取这个 Server 提供的 Tools"
                }}</span>
              <span aria-hidden="true">›</span>
            </button>
          </article>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.mcp-workspace {
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  background: var(--h-bg);
  color: var(--h-text);
}

.mcp-workspace__scroll {
  width: 100%;
  height: 100%;
  overflow: auto;
}

.mcp-workspace__inner {
  width: min(var(--h-content-wide), calc(100% - (var(--h-page-gutter) * 2)));
  min-width: 0;
  margin: 0 auto;
  padding: var(--h-page-top) 0 var(--h-page-bottom);
}

.mcp-workspace__legend {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 7px;
  padding-bottom: 3px;
  color: var(--h-text-muted);
  font-size: 11px;
}

.mcp-workspace__legend-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--h-success);
}

.mcp-control-panel {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 14px;
  margin-bottom: 14px;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-lg);
  background: var(--h-surface);
  padding: 18px;
}

.mcp-control-panel__top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 18px;
}

.mcp-control-panel__copy h2 {
  margin: 0;
  color: var(--h-text);
  font-size: 17px;
  font-weight: 600;
}

.mcp-control-panel__copy p {
  max-width: 760px;
  margin: 6px 0 0;
  color: var(--h-text-secondary);
  font-size: 12px;
  line-height: 1.7;
}

.mcp-control-panel__actions {
  display: flex;
  flex: 0 0 auto;
  gap: 8px;
}

.agent-picker {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-md);
  background: var(--h-bg);
  padding: 11px 12px;
}

.agent-picker__identity {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 10px;
}

.agent-picker__avatar {
  display: grid;
  width: 32px;
  height: 32px;
  flex: 0 0 32px;
  place-items: center;
  border: 1px solid var(--h-border-strong);
  border-radius: 50%;
  background: var(--h-accent-soft);
  color: var(--h-accent-hover);
  font-size: 12px;
  font-weight: 600;
}

.agent-picker__copy {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 2px;
}

.agent-picker__copy > span {
  color: var(--h-text);
  font-size: 12px;
  font-weight: 600;
}

.agent-picker__copy small {
  color: var(--h-text-muted);
  font-size: 10px;
}

.agent-picker__select {
  width: min(320px, 44%);
  min-width: 210px;
}

.mcp-browse-hint {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  border-top: 1px solid var(--h-border);
  padding-top: 11px;
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.6;
}

.mcp-browse-hint :deep(svg) {
  flex: 0 0 auto;
  margin-top: 1px;
  color: var(--h-accent);
}

.mcp-server-list {
  display: flex;
  min-width: 0;
  flex-direction: column;
  overflow: hidden;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-lg);
  background: var(--h-surface);
}

.mcp-server-card + .mcp-server-card {
  border-top: 1px solid var(--h-border);
}

.mcp-server-card__header {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: 14px 18px;
  padding: 15px 16px;
}

.mcp-server-card__identity {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  gap: 11px;
}

.mcp-server-card__icon {
  display: grid;
  width: 32px;
  height: 32px;
  flex: 0 0 32px;
  place-items: center;
  border: 1px solid var(--h-border-strong);
  border-radius: 9px;
  background: var(--h-bg);
  color: var(--h-accent-hover);
}

.mcp-server-card__copy {
  min-width: 0;
}

.mcp-server-card__title-row {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 7px;
}

.mcp-server-card__title-row strong {
  color: var(--h-text);
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
}

.mcp-server-card__title-row code,
.mcp-server-card__title-row span {
  color: var(--h-text-muted);
  font-size: 10px;
}

.mcp-server-card__title-row span {
  border: 1px solid var(--h-border);
  border-radius: 999px;
  padding: 1px 6px;
}

.mcp-server-card__copy p {
  margin: 5px 0 0;
  color: var(--h-text-muted);
  font-size: 10px;
}

.mcp-server-card__actions {
  display: flex;
  min-width: 0;
  max-width: min(620px, 58vw);
  flex: 0 1 auto;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 7px;
}

.mcp-server-card__actions :deep(.h-status-pill) {
  max-width: 150px;
}

.mcp-server-card__diagnostic {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  gap: 8px;
  margin: -3px 16px 14px 59px;
  border: 1px solid var(--h-border);
  border-radius: 9px;
  background: var(--h-bg);
  padding: 8px 10px;
  color: var(--h-text-secondary);
  font-size: 10px;
  line-height: 1.55;
}

.mcp-server-card__diagnostic > span:last-child {
  display: -webkit-box;
  min-width: 0;
  overflow: hidden;
  overflow-wrap: anywhere;
  word-break: break-word;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
}

.mcp-server-card__diagnostic-dot {
  width: 6px;
  height: 6px;
  flex: 0 0 6px;
  margin-top: 5px;
  border-radius: 50%;
  background: var(--h-text-muted);
}

.mcp-server-card__diagnostic--error {
  border-color: color-mix(in srgb, var(--h-danger) 28%, var(--h-border));
  background: var(--h-danger-soft, var(--h-bg));
  color: var(--h-danger, var(--h-text-secondary));
}

.mcp-server-card__diagnostic--error .mcp-server-card__diagnostic-dot {
  background: var(--h-danger, var(--h-text-muted));
}

.mcp-server-card__diagnostic--warning {
  border-color: color-mix(in srgb, var(--h-warning) 28%, var(--h-border));
  background: var(--h-warning-soft);
  color: var(--h-warning);
}

.mcp-server-card__diagnostic--warning .mcp-server-card__diagnostic-dot {
  background: var(--h-warning);
}

.mcp-server-card__collapse {
  min-width: 78px;
}

.mcp-server-card__chevron {
  transition: transform 160ms ease;
}

.mcp-server-card__chevron--open {
  transform: rotate(180deg);
}

.mcp-tool-list,
.mcp-stale-tools {
  border-top: 1px solid var(--h-border);
}

.mcp-tool-list__toolbar {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  border-bottom: 1px solid var(--h-border);
  padding: 7px 12px 7px 16px;
  background: color-mix(in srgb, var(--h-bg) 72%, var(--h-surface) 28%);
  color: var(--h-text-muted);
  font-size: 10px;
}

.mcp-tool-list__bulk-actions {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 2px;
}

.mcp-stale-tools {
  background: var(--h-warning-soft);
}

.mcp-stale-tools__title {
  padding: 10px 16px 0;
  color: var(--h-warning);
  font-size: 10px;
  font-weight: 600;
}

.mcp-tool-row {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 22px;
  padding: 13px 16px;
  transition: background-color 120ms ease;
}

.mcp-tool-row + .mcp-tool-row {
  border-top: 1px solid var(--h-border);
}

.mcp-tool-row--enabled {
  background: var(--h-accent-soft);
}

.mcp-tool-row--warning {
  background: transparent;
}

.mcp-tool-row__body {
  min-width: 0;
}

.mcp-tool-row__body > strong,
.mcp-tool-row__name strong {
  color: var(--h-text);
  font-size: 12px;
  font-weight: 600;
}

.mcp-tool-row__name {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 8px;
}

.mcp-tool-row__name code {
  color: var(--h-text-muted);
  font-size: 10px;
}

.mcp-tool-row__body p {
  display: -webkit-box;
  max-width: 820px;
  margin: 5px 0 0;
  overflow: hidden;
  color: var(--h-text-secondary);
  font-size: 10px;
  line-height: 1.55;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.mcp-tool-row--clickable {
  cursor: pointer;
  transition: border-color .16s ease, background .16s ease, transform .16s ease;
}

.mcp-tool-row--clickable:hover {
  border-color: var(--h-border-strong);
  background: var(--h-surface-hover, var(--h-bg));
}

.mcp-tool-row--clickable:focus-visible {
  outline: 2px solid var(--h-accent-border);
  outline-offset: 2px;
}

.mcp-tool-row__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 14px;
  margin-top: 7px;
  color: var(--h-text-muted);
  font-size: 10px;
}

.mcp-tool-row__toggle {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 10px;
}

.mcp-tool-row__toggle span {
  color: var(--h-text-muted);
  font-size: 10px;
}

.mcp-tool-empty,
.mcp-discover-hint {
  border: 0;
  padding: 15px 16px;
  color: var(--h-text-muted);
  font-size: 10px;
}

.mcp-discover-hint {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  border-top: 1px solid var(--h-border);
  background: transparent;
  cursor: pointer;
  text-align: left;
}

.mcp-discover-hint:hover {
  background: var(--h-surface-hover);
  color: var(--h-accent-hover);
}

@media (max-width: 920px) {
  .mcp-workspace__inner {
    width: calc(100% - (var(--h-page-gutter) * 2));
  }

  .mcp-control-panel__top,
  .agent-picker {
    align-items: stretch;
    flex-direction: column;
  }

  .mcp-server-card__header {
    grid-template-columns: minmax(0, 1fr);
  }

  .agent-picker__select {
    width: 100%;
    min-width: 0;
  }

  .mcp-server-card__actions {
    max-width: none;
    justify-content: flex-start;
  }

  .mcp-server-card__diagnostic {
    margin-left: 16px;
  }

  .mcp-tool-list__toolbar {
    align-items: flex-start;
    flex-direction: column;
  }
}

.mcp-server-card--disabled {
  opacity: .72;
}

.mcp-server-card__disabled-chip {
  border-color: var(--h-border-strong) !important;
  color: var(--h-text-muted) !important;
}
</style>
