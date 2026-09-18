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

import WorkspaceArtifacts
  from "./WorkspaceArtifacts.vue";
import WorkspaceFileTree
  from "./WorkspaceFileTree.vue";
import WorkspacePreview
  from "./WorkspacePreview.vue";

import {
  useAgentStore,
} from "../../stores/agents.js";
import {
  useWorkspaceStore,
} from "../../stores/workspace.js";
import {
  formatBytes,
} from "../../utils/workspace.js";

const emit = defineEmits([
  "open-session",
]);

const agentStore = useAgentStore();
const workspaceStore = useWorkspaceStore();

const refreshing = ref(false);

/**
 * 文件树与产物面板属于纯界面状态，不应该写入 Agent 配置或工作区配置。
 *
 * 使用 localStorage 的原因：
 * 1. 用户折叠后切换到聊天再回来，布局仍保持原样；
 * 2. 不会污染后端领域模型；
 * 3. localStorage 不可用时只退化为默认展开，不影响工作区核心功能。
 */
const panelStateStorageKey =
    "humbert.workspace.panels.v1";

const leftCollapsed = ref(false);
const rightCollapsed = ref(false);

let revisionRefreshTimer = null;

const selectedAgent = computed(() => (
    agentStore.items.find(
        (item) => item.id === agentStore.selectedID,
    ) ?? null
));

const modeLabel = computed(() => (
    workspaceStore.overview?.mode === "custom"
        ? "自定义工作区"
        : "Humbert 管理工作区"
));

/**
 * 三栏布局由顶层统一控制。
 *
 * 折叠后保留 42px 的窄栏，而不是彻底把面板从 DOM 里隐藏：
 * 用户始终能看到“文件 / 产物”入口，也不需要额外寻找恢复按钮。
 */
const contentStyle = computed(() => ({
  gridTemplateColumns: [
    leftCollapsed.value
        ? "42px"
        : "minmax(220px, 268px)",
    "minmax(320px, 1fr)",
    rightCollapsed.value
        ? "42px"
        : "minmax(258px, 318px)",
  ].join(" "),
}));

/**
 * Agent 是应用级一级选择。
 *
 * 切换 Agent 后必须完整重载 Workspace，不能继续复用上一个 Agent 的相对路径；
 * 否则两个工作区里同名文件可能被错误地当成同一个文件预览。
 */
watch(
    () => agentStore.selectedID,
    async (agentID) => {
      try {
        await workspaceStore.load(agentID);
      } catch (error) {
        Message.error(
            error?.message ?? String(error),
        );
      }
    },
    {immediate: true},
);

/**
 * Runtime / 主动助手只负责告诉 Store“底层工作区可能变化”。
 *
 * Workspace 页面真正挂载时才执行刷新，并用 250ms 合并连续事件，避免一次 Agent
 * Turn 内多个 write/edit 事件触发多次目录 IPC。
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

  if (
      agentStore.selectedID &&
      workspaceStore.agentID !== agentStore.selectedID
  ) {
    void workspaceStore.load(
        agentStore.selectedID,
    );
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
    rightCollapsed.value =
        Boolean(state?.rightCollapsed);
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
          rightCollapsed: rightCollapsed.value,
        }),
    );
  } catch {
    // localStorage 仅保存界面偏好，失败时不阻塞任何工作区功能。
  }
}

function setLeftCollapsed(value) {
  leftCollapsed.value = Boolean(value);
  persistPanelState();
}

function setRightCollapsed(value) {
  rightCollapsed.value = Boolean(value);
  persistPanelState();
}

async function refreshAll(showMessage = true) {
  if (
      !agentStore.selectedID ||
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

async function openPath(path) {
  try {
    await workspaceStore.openPath(path);
  } catch (error) {
    Message.error(
        error?.message ?? String(error),
    );
  }
}

function openSourceSession(payload) {
  emit("open-session", {
    agentID: agentStore.selectedID,
    sessionID: payload?.sessionID ?? "",
  });
}
</script>

<template>
  <section class="workspace-view">
    <header class="workspace-view__topbar">
      <div class="workspace-view__heading">
        <div class="workspace-view__eyebrow">WORKSPACE &amp; ARTIFACTS</div>

        <div class="workspace-view__title-row">
          <h1>{{ selectedAgent?.name || "工作区" }}</h1>

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

    <div
        v-if="!selectedAgent"
        class="workspace-view__empty"
    >
      <strong>还没有可浏览的 Agent</strong>
      <span>先创建或选择一个 Agent，Humbert 会在这里展示它当前绑定的工作区。</span>
    </div>

    <div
        v-else-if="workspaceStore.error && !workspaceStore.overview"
        class="workspace-view__empty"
    >
      <strong>工作区暂时不可用</strong>
      <span>{{ workspaceStore.error }}</span>
      <a-button @click="workspaceStore.load(agentStore.selectedID)">重试</a-button>
    </div>

    <template v-else>
      <div
          v-if="workspaceStore.overview?.statsTruncated"
          class="workspace-view__notice"
      >
        总览统计达到 5000 个文件的保护上限；文件树仍可继续按目录浏览，不影响真实文件。
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
            <path d="m9 5 7 7-7 7"/>
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

        <button
            v-if="rightCollapsed"
            type="button"
            class="workspace-view__rail workspace-view__rail--right"
            aria-label="展开产物面板"
            title="展开产物面板"
            @click="setRightCollapsed(false)"
        >
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="m15 5-7 7 7 7"/>
          </svg>
          <span>产物</span>
        </button>

        <WorkspaceArtifacts
            v-else
            :artifacts="workspaceStore.artifacts"
            :recent-files="workspaceStore.overview?.recentFiles || []"
            :loading="workspaceStore.loadingArtifacts"
            @collapse="setRightCollapsed(true)"
            @open-path="openPath"
            @open-session="openSourceSession"
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

/*
 * 顶部信息区使用 Humbert 自己的暖纸张色、墨蓝强调色和 Border Token。
 * 不再使用 Arco 的 color-bg / color-text Token，避免工作区和其它一级页面出现两套色系。
 */
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
  align-items: center;
  gap: 10px;
}

.workspace-view__title-row h1 {
  margin: 0;
  color: var(--h-text);
  font-size: 25px;
  font-weight: 500;
  letter-spacing: -0.015em;
  line-height: 1.2;
}

.workspace-view__mode {
  padding: 3px 8px;
  border: 1px solid var(--h-border);
  border-radius: 999px;
  color: var(--h-text-muted);
  font-family: var(--h-ui);
  font-size: 10px;
}

.workspace-view__path {
  max-width: min(720px, 52vw);
  margin-top: 7px;
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
  transition: border-color var(--h-transition),
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
  transition: background-color var(--h-transition),
  color var(--h-transition);
}

.workspace-view__rail:hover {
  background: var(--h-surface-hover);
  color: var(--h-accent);
}

.workspace-view__rail--left {
  border-right: 1px solid var(--h-border);
}

.workspace-view__rail--right {
  border-left: 1px solid var(--h-border);
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
