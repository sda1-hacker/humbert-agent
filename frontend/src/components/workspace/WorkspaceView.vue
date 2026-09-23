<script setup>
import {
  computed,
  onMounted,
  onUnmounted,
  ref,
  watch,
} from "vue";

import {
  Message,
} from "@arco-design/web-vue";

import WorkspaceFileTree
  from "./WorkspaceFileTree.vue";
import WorkspacePreview
  from "./WorkspacePreview.vue";
import DeskNotes from "./DeskNotes.vue";
import { searchWorkspaceDocuments } from "../../api/workspace.js";

const emit = defineEmits(['open-session']);

import {
  useAgentStore,
} from "../../stores/agents.js";
import {
  useWorkspaceStore,
} from "../../stores/workspace.js";
import {
  formatBytes,
} from "../../utils/workspace.js";

const agentStore = useAgentStore();
const workspaceStore = useWorkspaceStore();

const refreshing = ref(false);
const documentQuery = ref("");
const documentResults = ref([]);
const documentSearching = ref(false);
const documentSearched = ref(false);
let documentSearchSequence = 0;

watch(() => workspaceStore.agentID, () => {
  documentSearchSequence += 1;
  documentResults.value = [];
  documentSearched.value = false;
});

async function searchDocuments() {
  const query = documentQuery.value.trim();
  if (!query || !workspaceStore.agentID) return;
  const sequence = ++documentSearchSequence;
  documentSearching.value = true;
  try {
    const results = await searchWorkspaceDocuments(workspaceStore.agentID, query);
    if (sequence === documentSearchSequence) {
      documentResults.value = Array.isArray(results) ? results : [];
      documentSearched.value = true;
    }
  } catch (error) {
    if (sequence === documentSearchSequence) Message.error(error?.message ?? String(error));
  } finally {
    if (sequence === documentSearchSequence) documentSearching.value = false;
  }
}

async function openDocumentResult(result) {
  try { await workspaceStore.openPath(result.path); }
  catch (error) { Message.error(error?.message ?? String(error)); }
}

/**
 * 文件树折叠状态属于纯 UI 偏好，不应该写入 Agent Profile 或 Workspace 配置。
 *
 * 工作区现在只剩“文件树 + 文件预览”两块，因此这里只保留左栏折叠状态。
 */
const panelStateStorageKey =
    "humbert.workspace.panels.v2";

const leftCollapsed = ref(false);

let revisionRefreshTimer = null;

const modeLabel = computed(() => (
    workspaceStore.overview?.mode === "custom"
        ? "自定义工作区"
        : "Humbert 管理工作区"
));

/**
 * 左栏折叠后保留 42px 的恢复入口；预览区始终占据剩余空间。
 */
const contentStyle = computed(() => ({
  gridTemplateColumns: [
    leftCollapsed.value
        ? "42px"
        : "minmax(220px, 286px)",
    "minmax(360px, 1fr)",
  ].join(" "),
}));

/**
 * Runtime / 主动助手只负责告诉 Store“底层 Workspace 可能变化”。
 *
 * 页面可见时再执行合并刷新，避免一次 Turn 内多个 write/edit 事件产生重复 IPC。
 */
watch(
    () => workspaceStore.revision,
    () => {
      if (revisionRefreshTimer !== null) {
        clearTimeout(revisionRefreshTimer);
      }

      revisionRefreshTimer = setTimeout(() => {
        revisionRefreshTimer = null;
        void refreshAll(false);
      }, 250);
    },
);

onMounted(() => {
  restorePanelState();

  /**
   * 工作区选择和聊天 Agent 选择是两份状态：
   *
   * - 第一次进入工作区：默认跟随当前聊天 Agent；
   * - 用户在工作区切换到其它 Agent/项目后：保持工作区自己的选择；
   * - 如果原选择已经被删除：回退到当前聊天 Agent或 Agent 列表第一项。
   */
  const existing = agentStore.items.some(
      (item) => item.id === workspaceStore.agentID,
  );
  const initialAgentID = existing
      ? workspaceStore.agentID
      : (
          agentStore.selectedID ||
          agentStore.items[0]?.id ||
          ""
      );

  if (
      initialAgentID &&
      (
          workspaceStore.agentID !== initialAgentID ||
          !workspaceStore.overview
      )
  ) {
    void loadWorkspace(initialAgentID);
  }
});

onUnmounted(() => {
  if (revisionRefreshTimer !== null) {
    clearTimeout(revisionRefreshTimer);
    revisionRefreshTimer = null;
  }
});

function restorePanelState() {
  if (typeof window === "undefined") {
    return;
  }

  try {
    const raw = window.localStorage
        .getItem(panelStateStorageKey);

    if (!raw) {
      return;
    }

    const state = JSON.parse(raw);
    leftCollapsed.value =
        Boolean(state?.leftCollapsed);
  } catch {
    // 布局偏好读取失败不应该影响文件访问，因此静默使用默认展开状态。
  }
}

function persistPanelState() {
  if (typeof window === "undefined") {
    return;
  }

  try {
    window.localStorage.setItem(
        panelStateStorageKey,
        JSON.stringify({
          leftCollapsed: leftCollapsed.value,
        }),
    );
  } catch {
    // localStorage 只是界面偏好；写入失败时不阻塞任何工作区能力。
  }
}

function setLeftCollapsed(value) {
  leftCollapsed.value = Boolean(value);
  persistPanelState();
}

async function loadWorkspace(agentID) {
  if (!agentID) {
    return;
  }

  try {
    await workspaceStore.load(agentID);
  } catch (error) {
    Message.error(
        error?.message ?? String(error),
    );
  }
}

/**
 * 顶部选择器用于在不同 Agent 对应的 Workspace 之间切换。
 *
 * Humbert 目前没有独立 Project 实体，因此一个 Agent 所绑定的 Workspace 就是当前阶段
 * 的“项目入口”。以后增加 Project 模型时，只需要扩展选择器数据源，不需要重写文件浏览器。
 */
async function switchWorkspace(agentID) {
  if (!agentID || agentID === workspaceStore.agentID) {
    return;
  }

  await loadWorkspace(agentID);
}

async function refreshAll(showMessage = true) {
  if (
      !workspaceStore.agentID ||
      refreshing.value
  ) {
    return;
  }

  refreshing.value = true;

  try {
    await workspaceStore.refresh();

    if (showMessage) {
      Message.success("工作区已刷新");
    }
  } catch (error) {
    if (showMessage) {
      Message.error(
          error?.message ?? String(error),
      );
    }
  } finally {
    refreshing.value = false;
  }
}

async function toggleDirectory(path) {
  try {
    await workspaceStore.toggleDirectory(path);
  } catch (error) {
    Message.error(
        error?.message ?? String(error),
    );
  }
}

async function selectEntry(entry) {
  try {
    await workspaceStore.selectEntry(entry);
  } catch (error) {
    Message.error(
        error?.message ?? String(error),
    );
  }
}
</script>

<template>
  <section class="workspace-view">
    <header class="workspace-view__topbar">
      <div class="workspace-view__heading">
        <div class="workspace-view__eyebrow">WORKSPACE</div>

        <div class="workspace-view__title-row">
          <span class="workspace-view__picker-label">Agent / 项目</span>

          <a-select
              class="workspace-view__agent-select"
              :model-value="workspaceStore.agentID"
              :loading="agentStore.loading"
              placeholder="选择工作区"
              @change="switchWorkspace"
          >
            <a-option
                v-for="agent in agentStore.items"
                :key="agent.id"
                :value="agent.id"
            >
              {{ agent.name }}
            </a-option>
          </a-select>

          <span
              v-if="workspaceStore.overview"
              class="workspace-view__mode"
          >
            {{ modeLabel }}
          </span>
        </div>

        <div
            v-if="workspaceStore.overview"
            class="workspace-view__path"
            :title="workspaceStore.overview.rootDir"
        >
          {{ workspaceStore.overview.rootDir }}
        </div>
      </div>

      <div
          v-if="workspaceStore.overview"
          class="workspace-view__stats"
      >
        <div>
          <strong>{{ workspaceStore.overview.fileCount }}</strong>
          <span>文件</span>
        </div>

        <div>
          <strong>{{ workspaceStore.overview.directoryCount }}</strong>
          <span>目录</span>
        </div>

        <div>
          <strong>{{ formatBytes(workspaceStore.overview.totalBytes) }}</strong>
          <span>统计大小</span>
        </div>

        <button
            type="button"
            class="workspace-view__refresh"
            :disabled="refreshing"
            @click="refreshAll(true)"
        >
          {{ refreshing ? "刷新中…" : "刷新" }}
        </button>
      </div>
    </header>

    <DeskNotes :agent-id="workspaceStore.agentID" @open-session="emit('open-session', $event)" />

    <section v-if="workspaceStore.agentID" class="workspace-view__document-search">
      <div class="workspace-view__document-query">
        <a-input v-model="documentQuery" placeholder="搜索工作区 PDF / Office 文档正文" @press-enter="searchDocuments" />
        <a-button :loading="documentSearching" :disabled="!documentQuery.trim()" @click="searchDocuments">检索资料</a-button>
      </div>
      <div v-if="documentSearched" class="workspace-view__document-results">
        <span v-if="!documentResults.length">没有找到匹配的文档内容</span>
        <button v-for="result in documentResults" :key="`${result.path}:${result.line}`" type="button" @click="openDocumentResult(result)">
          <strong>{{ result.path }}:{{ result.line }}</strong>
          <span>{{ result.snippet }}</span>
        </button>
      </div>
    </section>

    <div
        v-if="agentStore.items.length === 0"
        class="workspace-view__empty"
    >
      <strong>还没有可浏览的 Agent</strong>
      <span>先创建一个 Agent 并为它配置工作区，Humbert 会在这里展示当前文件。</span>
    </div>

    <div
        v-else-if="workspaceStore.error && !workspaceStore.overview"
        class="workspace-view__empty"
    >
      <strong>工作区暂时不可用</strong>
      <span>{{ workspaceStore.error }}</span>
      <a-button @click="loadWorkspace(workspaceStore.agentID)">重试</a-button>
    </div>

    <template v-else>
      <div
          v-if="workspaceStore.overview?.statsTruncated"
          class="workspace-view__notice"
      >
        顶部统计达到 5000 个文件的保护上限；左侧文件树仍可继续按目录浏览，不影响真实文件。
      </div>

      <div
          class="workspace-view__content"
          :style="contentStyle"
      >
        <button
            v-if="leftCollapsed"
            type="button"
            class="workspace-view__rail workspace-view__rail--left"
            aria-label="展开文件面板"
            title="展开文件面板"
            @click="setLeftCollapsed(false)"
        >
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="m9 5 7 7-7 7" />
          </svg>
          <span>文件</span>
        </button>

        <WorkspaceFileTree
            v-else
            :directories="workspaceStore.directories"
            :expanded-paths="workspaceStore.expandedPaths"
            :selected-path="workspaceStore.selectedPath"
            :loading-directories="workspaceStore.loadingDirectories"
            @collapse="setLeftCollapsed(true)"
            @toggle="toggleDirectory"
            @select="selectEntry"
        />

        <WorkspacePreview
            :selected-entry="workspaceStore.selectedEntry"
            :preview="workspaceStore.preview"
            :loading="workspaceStore.loadingPreview"
        />
      </div>
    </template>
  </section>
</template>

<style scoped>
.workspace-view {
  display: flex;
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
  overflow: hidden;
  background: var(--h-bg);
}

.workspace-view__document-search { flex: 0 0 auto; padding: 10px 24px; border-bottom: 1px solid var(--h-border); }
.workspace-view__document-query { display: flex; gap: 8px; }
.workspace-view__document-query :deep(.arco-input-wrapper) { flex: 1; }
.workspace-view__document-results { display: flex; gap: 6px; overflow: auto; margin-top: 8px; }
.workspace-view__document-results > button { display: grid; flex: 0 0 250px; gap: 4px; padding: 7px; border: 1px solid var(--h-border); border-radius: 6px; background: var(--h-bg); color: var(--h-text-secondary); cursor: pointer; text-align: left; }
.workspace-view__document-results > button strong { overflow: hidden; color: var(--h-text); font-size: 10px; text-overflow: ellipsis; white-space: nowrap; }
.workspace-view__document-results > button span { overflow: hidden; font-size: 10px; text-overflow: ellipsis; white-space: nowrap; }

.workspace-view__topbar {
  display: flex;
  min-height: 104px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  padding: 18px 24px;
  border-bottom: 1px solid var(--h-border);
  background: var(--h-bg);
}

.workspace-view__heading {
  min-width: 0;
}

.workspace-view__eyebrow {
  margin-bottom: 5px;
  color: var(--h-text-muted);
  font-family: var(--h-ui);
  font-size: 10px;
  font-weight: 500;
  letter-spacing: 0.1em;
  text-transform: uppercase;
}

.workspace-view__title-row {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 9px;
}

.workspace-view__picker-label {
  flex: 0 0 auto;
  color: var(--h-text-muted);
  font-family: var(--h-ui);
  font-size: 10px;
}

/*
 * Agent 选择器视觉上承担原来大标题的位置，因此弱化 Arco 默认输入框感。
 * 业务上仍使用 a-select，保证键盘操作和弹层行为继续由成熟组件负责。
 */
.workspace-view__agent-select {
  width: clamp(150px, 23vw, 280px);
}

.workspace-view__agent-select :deep(.arco-select-view) {
  min-height: 36px;
  padding: 0 30px 0 0;
  border: 0;
  border-radius: 0;
  background: transparent;
  color: var(--h-text);
  box-shadow: none;
  font-family: var(--h-serif);
  font-size: 24px;
  font-weight: 500;
  letter-spacing: -0.015em;
}

.workspace-view__agent-select :deep(.arco-select-view:hover),
.workspace-view__agent-select :deep(.arco-select-view-focus) {
  background: transparent;
  box-shadow: none;
}

.workspace-view__mode {
  flex: 0 0 auto;
  padding: 3px 8px;
  border: 1px solid var(--h-border);
  border-radius: 999px;
  color: var(--h-text-muted);
  font-family: var(--h-ui);
  font-size: 10px;
}

.workspace-view__path {
  max-width: min(760px, 56vw);
  margin-top: 6px;
  overflow: hidden;
  color: var(--h-text-muted);
  font-family: var(--h-mono);
  font-size: 11px;
  line-height: 1.5;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.workspace-view__stats {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 18px;
}

.workspace-view__stats > div {
  display: grid;
  gap: 2px;
  text-align: right;
}

.workspace-view__stats strong {
  color: var(--h-text);
  font-size: 13px;
  font-weight: 500;
}

.workspace-view__stats span {
  color: var(--h-text-muted);
  font-family: var(--h-ui);
  font-size: 9px;
}

.workspace-view__refresh {
  height: 32px;
  padding: 0 12px;
  border: 1px solid var(--h-border-strong);
  border-radius: var(--h-radius-sm);
  background: transparent;
  color: var(--h-text-secondary);
  cursor: pointer;
  font-family: var(--h-ui);
  font-size: 11px;
  transition:
      border-color var(--h-transition),
      background-color var(--h-transition),
      color var(--h-transition);
}

.workspace-view__refresh:hover:not(:disabled) {
  border-color: var(--h-accent-border);
  background: var(--h-accent-soft);
  color: var(--h-accent);
}

.workspace-view__refresh:disabled {
  cursor: default;
  opacity: 0.55;
}

.workspace-view__notice {
  flex: 0 0 auto;
  padding: 7px 24px;
  border-bottom: 1px solid var(--h-border);
  background: var(--h-warning-soft);
  color: var(--h-warning);
  font-family: var(--h-ui);
  font-size: 10px;
  line-height: 1.5;
}

.workspace-view__content {
  display: grid;
  min-width: 0;
  min-height: 0;
  flex: 1 1 auto;
  overflow: hidden;
  transition: grid-template-columns 150ms ease;
}

.workspace-view__rail {
  display: flex;
  min-width: 0;
  min-height: 0;
  align-items: center;
  flex-direction: column;
  gap: 10px;
  padding: 13px 0;
  border: 0;
  background: var(--h-surface);
  color: var(--h-text-muted);
  cursor: pointer;
  font-family: var(--h-ui);
  transition:
      background-color var(--h-transition),
      color var(--h-transition);
}

.workspace-view__rail:hover {
  background: var(--h-surface-hover);
  color: var(--h-accent);
}

.workspace-view__rail--left {
  border-right: 1px solid var(--h-border);
}

.workspace-view__rail svg {
  width: 16px;
  height: 16px;
  fill: none;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.7;
}

.workspace-view__rail span {
  writing-mode: vertical-rl;
  font-size: 11px;
  letter-spacing: 0.18em;
}

.workspace-view__empty {
  display: grid;
  min-height: 0;
  flex: 1 1 auto;
  place-items: center;
  align-content: center;
  gap: 9px;
  padding: 40px;
  color: var(--h-text-muted);
  text-align: center;
}

.workspace-view__empty strong {
  color: var(--h-text);
  font-size: 15px;
  font-weight: 500;
}

.workspace-view__empty span {
  max-width: 520px;
  font-size: 11px;
  line-height: 1.7;
}

@media (max-width: 1050px) {
  .workspace-view__topbar {
    padding-right: 18px;
    padding-left: 18px;
  }

  .workspace-view__stats > div:nth-child(2) {
    display: none;
  }
}
</style>
