<script setup>
import {
  computed,
} from "vue";

import {
  formatBytes,
} from "../../utils/workspace.js";

const props = defineProps({
  directories: {
    type: Object,
    default: () => ({}),
  },
  expandedPaths: {
    type: Array,
    default: () => ["."],
  },
  selectedPath: {
    type: String,
    default: "",
  },
  loadingDirectories: {
    type: Object,
    default: () => ({}),
  },
});

const emit = defineEmits([
  "collapse",
  "toggle",
  "select",
]);

/**
 * 后端目录接口只返回“当前目录的一层内容”。
 *
 * 这里根据已经展开的目录，把缓存中的多层结果投影成一份扁平列表：
 * 目录异步状态只存在 Pinia 一份，不需要递归 Vue 子组件各自维护 loading / cache，
 * Agent 切换或工作区刷新时也不会留下旧目录节点。
 */
const visibleRows = computed(() => {
  const rows = [];
  const expanded = new Set(
      props.expandedPaths,
  );

  function appendDirectory(path, depth) {
    const listing =
        props.directories[path];
    const entries =
        Array.isArray(listing?.entries)
            ? listing.entries
            : [];

    for (const entry of entries) {
      rows.push({
        entry,
        depth,
      });

      if (
          entry.type === "directory" &&
          expanded.has(entry.path)
      ) {
        appendDirectory(
            entry.path,
            depth + 1,
        );
      }
    }
  }

  appendDirectory(".", 0);

  return rows;
});

function handleEntryClick(entry) {
  if (entry.type === "directory") {
    emit("toggle", entry.path);
    return;
  }

  if (entry.type === "file") {
    emit("select", entry);
  }
}
</script>

<template>
  <section class="workspace-tree">
    <header class="workspace-tree__header">
      <div class="workspace-tree__heading">
        <strong>文件</strong>
        <span>按目录懒加载</span>
      </div>

      <button
          type="button"
          class="workspace-tree__collapse"
          aria-label="折叠文件面板"
          title="折叠文件面板"
          @click="emit('collapse')"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="m15 5-7 7 7 7" />
        </svg>
      </button>
    </header>

    <div class="workspace-tree__body">
      <div
          v-if="loadingDirectories['.'] && !directories['.']"
          class="workspace-tree__empty"
      >
        正在读取工作区…
      </div>

      <div
          v-else-if="visibleRows.length === 0"
          class="workspace-tree__empty"
      >
        当前工作区还是空的
      </div>

      <button
          v-for="row in visibleRows"
          :key="row.entry.path"
          type="button"
          class="workspace-tree__row"
          :class="{
            'workspace-tree__row--selected': selectedPath === row.entry.path,
            'workspace-tree__row--unsupported': ['symlink', 'other'].includes(row.entry.type),
          }"
          :style="{ paddingLeft: `${12 + row.depth * 18}px` }"
          :disabled="['symlink', 'other'].includes(row.entry.type)"
          @click="handleEntryClick(row.entry)"
      >
        <span class="workspace-tree__twisty" aria-hidden="true">
          <template v-if="row.entry.type === 'directory'">
            {{ expandedPaths.includes(row.entry.path) ? "⌄" : "›" }}
          </template>
        </span>

        <span class="workspace-tree__icon" aria-hidden="true">
          {{ row.entry.type === "directory" ? "▰" : row.entry.type === "file" ? "▤" : "◇" }}
        </span>

        <span
            class="workspace-tree__name"
            :title="row.entry.path"
        >
          {{ row.entry.name }}
        </span>

        <span
            v-if="row.entry.type === 'file'"
            class="workspace-tree__size"
        >
          {{ formatBytes(row.entry.size) }}
        </span>

        <span
            v-if="loadingDirectories[row.entry.path]"
            class="workspace-tree__loading"
        >…</span>
      </button>

      <div
          v-if="directories['.']?.truncated"
          class="workspace-tree__notice"
      >
        根目录项目过多，当前仅显示前 {{ directories['.'].entries.length }} 项。
      </div>
    </div>
  </section>
</template>

<style scoped>
.workspace-tree {
  display: grid;
  min-width: 0;
  min-height: 0;
  grid-template-rows: auto minmax(0, 1fr);
  border-right: 1px solid var(--h-border);
  background: var(--h-surface);
}

.workspace-tree__header {
  display: flex;
  min-height: 50px;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 9px 10px 9px 14px;
  border-bottom: 1px solid var(--h-border);
}

.workspace-tree__heading {
  display: flex;
  min-width: 0;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
  flex: 1 1 auto;
}

.workspace-tree__heading strong {
  color: var(--h-text);
  font-size: 13px;
  font-weight: 500;
}

.workspace-tree__heading span {
  color: var(--h-text-muted);
  font-family: var(--h-ui);
  font-size: 10px;
  white-space: nowrap;
}

.workspace-tree__collapse {
  display: grid;
  width: 28px;
  height: 28px;
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

.workspace-tree__collapse:hover {
  background: var(--h-surface-hover);
  color: var(--h-accent);
}

.workspace-tree__collapse svg {
  width: 16px;
  height: 16px;
  fill: none;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.7;
}

.workspace-tree__body {
  min-height: 0;
  overflow: auto;
  padding: 7px 6px 12px;
}

.workspace-tree__row {
  display: grid;
  width: 100%;
  height: 31px;
  grid-template-columns: 14px 18px minmax(0, 1fr) auto auto;
  align-items: center;
  gap: 4px;
  border: 0;
  border-radius: var(--h-radius-sm);
  background: transparent;
  color: var(--h-text-secondary);
  cursor: pointer;
  text-align: left;
  transition:
      background-color var(--h-transition),
      color var(--h-transition);
}

.workspace-tree__row:hover {
  background: var(--h-surface-hover);
}

.workspace-tree__row--selected {
  background: var(--h-accent-soft);
  color: var(--h-accent);
}

.workspace-tree__row--unsupported {
  cursor: default;
  opacity: 0.55;
}

.workspace-tree__twisty {
  width: 14px;
  color: var(--h-text-muted);
  text-align: center;
}

.workspace-tree__icon {
  color: var(--h-text-muted);
  font-size: 12px;
}

.workspace-tree__row--selected .workspace-tree__icon,
.workspace-tree__row--selected .workspace-tree__twisty {
  color: var(--h-accent);
}

.workspace-tree__name {
  overflow: hidden;
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.workspace-tree__size {
  margin-left: 6px;
  color: var(--h-text-muted);
  font-family: var(--h-ui);
  font-size: 9px;
  white-space: nowrap;
}

.workspace-tree__loading {
  color: var(--h-text-muted);
}

.workspace-tree__empty,
.workspace-tree__notice {
  padding: 18px 12px;
  color: var(--h-text-muted);
  font-size: 11px;
  line-height: 1.7;
}

.workspace-tree__notice {
  padding-top: 10px;
}
</style>
