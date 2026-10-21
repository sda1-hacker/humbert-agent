<script setup>
import { computed, ref, watch } from "vue";
import { IconFolder } from "@arco-design/web-vue/es/icon";
const props = defineProps({
  detail: { type: Object, required: true },
  selectedPath: String,
});
const emit = defineEmits(["select"]);
const files = computed(() =>
  Array.isArray(props.detail?.files) ? props.detail.files : [],
);
const expandedFolders = ref(new Set());
const allRows = computed(() => {
  const folders = new Set();

  const rows = [];

  for (const file of files.value) {
    const path = typeof file?.path === "string" ? file.path : "";

    if (!path) {
      continue;
    }

    const parts = path.split("/");

    for (let index = 1; index < parts.length; index += 1) {
      folders.add(parts.slice(0, index).join("/"));
    }
  }

  for (const folder of folders) {
    rows.push({
      path: folder,
      name: folder.split("/").slice(-1)[0],
      folder: true,
      level: folder.split("/").length - 1,

      text: false,
      sizeBytes: 0,
    });
  }

  for (const file of files.value) {
    const path = typeof file?.path === "string" ? file.path : "";

    if (!path) {
      continue;
    }

    rows.push({
      path,
      name: path.split("/").slice(-1)[0],
      folder: false,
      level: path.split("/").length - 1,
      text: Boolean(file?.text),
      sizeBytes: Number(file?.sizeBytes) || 0,
    });
  }

  rows.sort((left, right) => {
    if (left.path === "SKILL.md") {
      return -1;
    }

    if (right.path === "SKILL.md") {
      return 1;
    }

    return left.path.localeCompare(right.path);
  });

  return rows;
});
const treeRows = computed(() => {
  return allRows.value.filter((row) => {
    const parts = row.path.split("/");

    if (parts.length <= 1) {
      return true;
    }

    for (let index = 1; index < parts.length; index += 1) {
      const parent = parts.slice(0, index).join("/");

      if (!expandedFolders.value.has(parent)) {
        return false;
      }
    }

    return true;
  });
});
function toggleFolder(path) {
  const next = new Set(expandedFolders.value);

  if (next.has(path)) {
    next.delete(path);
  } else {
    next.add(path);
  }

  expandedFolders.value = next;
}
function formatBytes(value) {
  const bytes = Number(value) || 0;

  if (bytes < 1024) {
    return `${bytes} B`;
  }

  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KiB`;
  }

  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`;
}
watch(
  allRows,
  (rows) => {
    expandedFolders.value = new Set(
      rows.filter((row) => row.folder).map((row) => row.path),
    );
  },
  { immediate: true },
);
</script>
<template>
  <aside class="skill-files">
    <div class="skill-files__header">
      <div>
        <strong>Package 文件</strong>
        <span>
          {{ detail.fileCount }} 个文件 · {{ formatBytes(detail.sizeBytes) }}
        </span>
      </div>

      <IconFolder />
    </div>

    <div class="skill-files__tree">
      <button
        v-for="row in treeRows"
        :key="`${row.folder ? 'folder' : 'file'}:${row.path}`"
        type="button"
        class="skill-files__row"
        :class="{
          'skill-files__row--selected':
            !row.folder && selectedPath === row.path,
          'skill-files__row--binary': !row.folder && !row.text,
        }"
        :style="{
          paddingLeft: `${12 + row.level * 18}px`,
        }"
        @click="row.folder ? toggleFolder(row.path) : emit('select', row.path)"
      >
        <span class="skill-files__marker">
          <template v-if="row.folder">
            {{ expandedFolders.has(row.path) ? "▾" : "▸" }}
          </template>
          <template v-else>
            {{ row.text ? "·" : "◇" }}
          </template>
        </span>

        <span class="skill-files__name">
          {{ row.name }}
        </span>

        <span v-if="!row.folder" class="skill-files__size">
          {{ formatBytes(row.sizeBytes) }}
        </span>
      </button>
    </div>

    <div class="skill-files__path">
      <span>安装目录</span>
      <code>{{ detail.rootDir }}</code>
    </div>
  </aside>
</template>
<style scoped>
.skill-files {
  display: flex;
  min-width: 0;
  flex-direction: column;
  border-right: 1px solid var(--h-border);
  background: var(--h-bg);
}

.skill-files__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
  padding: 14px;
  border-bottom: 1px solid var(--h-border);
}

.skill-files__header > div {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.skill-files__header strong {
  color: var(--h-text);
  font-size: 11px;
}

.skill-files__header span {
  color: var(--h-text-muted);
  font-size: 10px;
}

.skill-files__tree {
  min-height: 0;
  flex: 1;
  overflow: auto;
  padding: 6px 0;
}

.skill-files__row {
  display: flex;
  width: 100%;
  min-width: 0;
  align-items: center;
  gap: 6px;
  padding-top: 7px;
  padding-right: 10px;
  padding-bottom: 7px;
  border: 0;
  background: transparent;
  color: var(--h-text-secondary);
  cursor: pointer;
  font: inherit;
  text-align: left;
}

.skill-files__row:hover {
  background: var(--h-surface-hover);
}

.skill-files__row--selected {
  background: var(--h-surface-active);
  color: var(--h-text);
}

.skill-files__row--binary {
  color: var(--h-text-muted);
}

.skill-files__marker {
  width: 12px;
  flex: 0 0 12px;
  color: var(--h-text-muted);
  text-align: center;
}

.skill-files__name {
  min-width: 0;
  flex: 1;
  overflow: hidden;
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.skill-files__size {
  flex: 0 0 auto;
  color: var(--h-text-muted);
  font-size: 8px;
}

.skill-files__path {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 12px;
  border-top: 1px solid var(--h-border);
  color: var(--h-text-muted);
  font-size: 8px;
}

.skill-files__path code {
  overflow: hidden;
  color: var(--h-text-secondary);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 980px) {
  .skill-files {
    min-height: 260px;
    border-right: 0;
    border-bottom: 1px solid var(--h-border);
  }
}
</style>
