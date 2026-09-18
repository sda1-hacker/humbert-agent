<script setup>
import {
  computed,
  ref,
} from "vue";

import {
  artifactOperationGroup,
  artifactOperationLabel,
  formatBytes,
  formatWorkspaceTime,
} from "../../utils/workspace.js";

const props = defineProps({
  artifacts: {
    type: Array,
    default: () => [],
  },
  recentFiles: {
    type: Array,
    default: () => [],
  },
  loading: {
    type: Boolean,
    default: false,
  },
});

const emit = defineEmits([
  "collapse",
  "open-path",
  "open-session",
]);

/**
 * “产物”和“最近修改”必须保持语义分离：
 *
 * - 产物：有 Session + 成功文件工具事务作为可审计来源；
 * - 最近修改：只来自文件系统 mtime，可能是用户、IDE、命令行或 Agent 间接修改。
 *
 * 不能把两者混成一张列表，否则 Humbert 会对自己没有证据的文件变化做错误归因。
 */
const activeTab = ref("artifacts");
const artifactFilter = ref("all");

const filteredArtifacts = computed(() => {
  if (artifactFilter.value === "all") {
    return props.artifacts;
  }

  return props.artifacts.filter(
      (item) => (
          artifactOperationGroup(
              item.operation,
          ) === artifactFilter.value
      ),
  );
});
</script>

<template>
  <section class="workspace-artifacts">
    <header class="workspace-artifacts__header">
      <div class="workspace-artifacts__tabs">
        <button
            type="button"
            :class="{ active: activeTab === 'artifacts' }"
            @click="activeTab = 'artifacts'"
        >产物</button>

        <button
            type="button"
            :class="{ active: activeTab === 'recent' }"
            @click="activeTab = 'recent'"
        >最近修改</button>
      </div>

      <button
          type="button"
          class="workspace-artifacts__collapse"
          aria-label="折叠产物面板"
          title="折叠产物面板"
          @click="emit('collapse')"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="m9 5 7 7-7 7" />
        </svg>
      </button>
    </header>

    <div
        v-if="activeTab === 'artifacts'"
        class="workspace-artifacts__body"
    >
      <div class="workspace-artifacts__filters">
        <button
            v-for="item in [
              ['all', '全部'],
              ['created', '生成'],
              ['modified', '修改'],
              ['deleted', '删除'],
            ]"
            :key="item[0]"
            type="button"
            :class="{ active: artifactFilter === item[0] }"
            @click="artifactFilter = item[0]"
        >
          {{ item[1] }}
        </button>
      </div>

      <div
          v-if="loading"
          class="workspace-artifacts__empty"
      >
        正在整理 Agent 产物…
      </div>

      <div
          v-else-if="filteredArtifacts.length === 0"
          class="workspace-artifacts__empty"
      >
        还没有可验证来源的文件产物。<br />
        Agent 使用文件工具创建或修改内容后会出现在这里。
      </div>

      <article
          v-for="item in filteredArtifacts"
          :key="`${item.path}:${item.entryID}`"
          class="artifact-card"
      >
        <button
            type="button"
            class="artifact-card__main"
            :disabled="!item.available"
            @click="emit('open-path', item.path)"
        >
          <div class="artifact-card__topline">
            <span
                class="artifact-card__operation"
                :data-operation="artifactOperationGroup(item.operation)"
            >
              {{ artifactOperationLabel(item.operation) }}
            </span>

            <span
                v-if="!item.available"
                class="artifact-card__missing"
            >
              当前不存在
            </span>
          </div>

          <strong :title="item.path">{{ item.path }}</strong>

          <span>
            {{ item.available ? formatBytes(item.size) : "文件已移动、删除或不可用" }}
            · {{ formatWorkspaceTime(item.occurredAt) }}
          </span>
        </button>

        <div class="artifact-card__source">
          <span :title="item.toolName">{{ item.toolName }}</span>

          <button
              type="button"
              @click="emit('open-session', { agentID: '', sessionID: item.sessionID })"
          >
            {{ item.sessionTitle || "查看来源会话" }}
          </button>
        </div>
      </article>
    </div>

    <div
        v-else
        class="workspace-artifacts__body"
    >
      <p class="workspace-artifacts__explain">
        按真实文件修改时间排列；这里的变化可能来自 Agent、命令行或你自己的编辑器，不会被自动归因为 Agent 产物。
      </p>

      <div
          v-if="recentFiles.length === 0"
          class="workspace-artifacts__empty"
      >
        暂无最近修改文件
      </div>

      <button
          v-for="item in recentFiles"
          :key="item.path"
          type="button"
          class="recent-file"
          @click="emit('open-path', item.path)"
      >
        <strong :title="item.path">{{ item.path }}</strong>
        <span>{{ formatBytes(item.size) }} · {{ formatWorkspaceTime(item.modifiedAt) }}</span>
      </button>
    </div>
  </section>
</template>

<style scoped>
.workspace-artifacts {
  display: grid;
  min-width: 0;
  min-height: 0;
  grid-template-rows: auto minmax(0, 1fr);
  border-left: 1px solid var(--h-border);
  background: var(--h-surface);
}

.workspace-artifacts__header {
  display: flex;
  min-height: 50px;
  align-items: stretch;
  justify-content: space-between;
  gap: 4px;
  padding: 0 8px 0 10px;
  border-bottom: 1px solid var(--h-border);
}

.workspace-artifacts__tabs {
  display: flex;
  min-width: 0;
  align-items: stretch;
}

.workspace-artifacts__tabs button {
  height: 50px;
  padding: 0 10px;
  border: 0;
  border-bottom: 2px solid transparent;
  background: transparent;
  color: var(--h-text-muted);
  cursor: pointer;
  font-size: 12px;
  transition:
      border-color var(--h-transition),
      color var(--h-transition);
}

.workspace-artifacts__tabs button.active {
  border-bottom-color: var(--h-accent);
  color: var(--h-accent);
  font-weight: 500;
}

.workspace-artifacts__collapse {
  display: grid;
  width: 28px;
  height: 28px;
  align-self: center;
  flex: 0 0 28px;
  place-items: center;
  padding: 0;
  border: 0;
  border-radius: var(--h-radius-sm);
  background: transparent;
  color: var(--h-text-muted);
  cursor: pointer;
  transition:
      background-color var(--h-transition),
      color var(--h-transition);
}

.workspace-artifacts__collapse:hover {
  background: var(--h-surface-hover);
  color: var(--h-accent);
}

.workspace-artifacts__collapse svg {
  width: 16px;
  height: 16px;
  fill: none;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.7;
}

.workspace-artifacts__body {
  min-height: 0;
  overflow: auto;
  padding: 10px;
}

.workspace-artifacts__filters {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
  margin-bottom: 10px;
}

.workspace-artifacts__filters button {
  padding: 4px 8px;
  border: 1px solid var(--h-border);
  border-radius: 999px;
  background: transparent;
  color: var(--h-text-muted);
  cursor: pointer;
  font-family: var(--h-ui);
  font-size: 10px;
  transition:
      border-color var(--h-transition),
      background-color var(--h-transition),
      color var(--h-transition);
}

.workspace-artifacts__filters button:hover {
  border-color: var(--h-accent-border);
  color: var(--h-accent);
}

.workspace-artifacts__filters button.active {
  border-color: var(--h-accent-border);
  background: var(--h-accent-soft);
  color: var(--h-accent);
}

.workspace-artifacts__empty {
  padding: 28px 12px;
  color: var(--h-text-muted);
  font-size: 11px;
  line-height: 1.7;
  text-align: center;
}

.workspace-artifacts__explain {
  margin: 0 2px 10px;
  padding: 8px 9px;
  border-radius: var(--h-radius-sm);
  background: color-mix(in srgb, var(--h-surface-hover) 72%, transparent);
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.6;
}

.artifact-card {
  margin-bottom: 8px;
  overflow: hidden;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-md);
  background: var(--h-bg);
}

.artifact-card__main {
  display: grid;
  width: 100%;
  gap: 5px;
  padding: 10px;
  border: 0;
  background: transparent;
  cursor: pointer;
  text-align: left;
}

.artifact-card__main:hover:not(:disabled) {
  background: var(--h-surface-hover);
}

.artifact-card__main:disabled {
  cursor: default;
  opacity: 0.72;
}

.artifact-card__main strong,
.recent-file strong {
  overflow: hidden;
  color: var(--h-text);
  font-size: 11px;
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.artifact-card__main > span,
.recent-file span {
  color: var(--h-text-muted);
  font-family: var(--h-ui);
  font-size: 9px;
}

.artifact-card__topline {
  display: flex;
  align-items: center;
  gap: 5px;
}

.artifact-card__operation {
  padding: 2px 5px;
  border-radius: 4px;
  background: var(--h-accent-soft);
  color: var(--h-accent);
  font-family: var(--h-ui);
  font-size: 9px;
}

.artifact-card__operation[data-operation="deleted"] {
  background: var(--h-danger-soft);
  color: var(--h-danger);
}

.artifact-card__operation[data-operation="modified"] {
  background: var(--h-warning-soft);
  color: var(--h-warning);
}

.artifact-card__missing {
  color: var(--h-text-muted);
  font-size: 9px;
}

.artifact-card__source {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  padding: 6px 9px;
  border-top: 1px solid var(--h-border);
  color: var(--h-text-muted);
  font-family: var(--h-ui);
  font-size: 9px;
}

.artifact-card__source span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.artifact-card__source button {
  max-width: 65%;
  overflow: hidden;
  border: 0;
  background: transparent;
  color: var(--h-accent);
  cursor: pointer;
  font-size: 9px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.recent-file {
  display: grid;
  width: 100%;
  gap: 4px;
  margin-bottom: 5px;
  padding: 8px 9px;
  border: 0;
  border-radius: var(--h-radius-sm);
  background: transparent;
  cursor: pointer;
  text-align: left;
}

.recent-file:hover {
  background: var(--h-surface-hover);
}
</style>
