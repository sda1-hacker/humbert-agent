<script setup>
import { onUnmounted, ref, watch } from "vue";
import { Message } from "../../utils/uiMessage.js";
import { searchWorkspaceDocuments } from "../../api/workspace.js";
import { pollIndexedSearch } from "../../utils/searchPolling.js";
import { useAgentStore } from "../../stores/agents.js";
import { useWorkspaceStore } from "../../stores/workspace.js";
import { useContextPanelStore } from "../../stores/contextPanel.js";
import WorkspaceFileTree from "./WorkspaceFileTree.vue";
import WorkspacePreview from "./WorkspacePreview.vue";

const agents = useAgentStore();
const workspace = useWorkspaceStore();
const panel = useContextPanelStore();
const fileBody = ref(null);
const searchOpen = ref(false);
const documentQuery = ref("");
const documentResults = ref([]);
const documentSearching = ref(false);
const documentSearched = ref(false);
let documentSearchSequence = 0;
let fileDragging = false;
let refreshTimer = null;

function report(error) { Message.error(error?.message || String(error)); }
function clampTreeWidth(value) {
  const available = fileBody.value?.clientWidth || panel.width;
  panel.setTreeWidth(Math.min(Math.max(125, available - 200), value));
}
function beginTreeResize(event) {
  if (event.button !== 0) return;
  fileDragging = true;
  event.currentTarget.setPointerCapture(event.pointerId);
}
function moveTreeResize(event) {
  if (!fileDragging || !fileBody.value) return;
  clampTreeWidth(event.clientX - fileBody.value.getBoundingClientRect().left);
}
function endTreeResize() { fileDragging = false; }
function keyTreeResize(event) {
  if (!["ArrowLeft", "ArrowRight"].includes(event.key)) return;
  event.preventDefault();
  clampTreeWidth(panel.treeWidth + (event.key === "ArrowRight" ? 20 : -20));
}
async function searchDocuments() {
  const query = documentQuery.value.trim();
  const agentID = agents.selectedID;
  if (!query || !agentID) return;
  const sequence = ++documentSearchSequence;
  documentSearching.value = true;
  try {
    await pollIndexedSearch(
        () => searchWorkspaceDocuments(agentID, query),
        () => sequence === documentSearchSequence,
        (results) => { documentResults.value = results; documentSearched.value = true; },
    );
  } catch (error) {
    if (sequence === documentSearchSequence) report(error);
  } finally {
    if (sequence === documentSearchSequence) documentSearching.value = false;
  }
}
async function openDocumentResult(result) {
  try { await workspace.openPath(result.path); }
  catch (error) { report(error); }
}
watch(() => agents.selectedID, async (agentID) => {
  documentSearchSequence += 1;
  documentResults.value = [];
  documentSearched.value = false;
  documentSearching.value = false;
  if (!agentID) return;
  if (workspace.agentID === agentID && workspace.overview) return;
  try { await workspace.load(agentID); } catch (error) { report(error); }
}, { immediate: true });
watch(() => workspace.revision, () => {
  if (workspace.agentID !== agents.selectedID) return;
  if (refreshTimer) clearTimeout(refreshTimer);
  refreshTimer = setTimeout(() => {
    refreshTimer = null;
    void workspace.refresh().catch(report);
  }, 250);
});
onUnmounted(() => {
  if (refreshTimer) clearTimeout(refreshTimer);
  documentSearchSequence += 1;
});
async function toggleDirectory(path) { try { await workspace.toggleDirectory(path); } catch (error) { report(error); } }
async function selectEntry(entry) { try { await workspace.selectEntry(entry); } catch (error) { report(error); } }
</script>

<template>
  <aside class="context-panel" aria-label="当前 Agent 工作区文件">
    <div class="context-panel__toolbar">
      <span :title="workspace.overview?.rootDir">{{ workspace.agentID === agents.selectedID ? (workspace.overview?.rootDir || '正在读取工作区…') : '正在切换工作区…' }}</span>
      <button type="button" :aria-pressed="searchOpen" :title="searchOpen ? '收起文档搜索' : '搜索文档正文'" :aria-label="searchOpen ? '收起文档搜索' : '搜索文档正文'" @click="searchOpen = !searchOpen">
        <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5"/><path d="m15.5 15.5 5 5"/></svg>
      </button>
    </div>

    <form v-if="searchOpen" class="context-panel__search" @submit.prevent="searchDocuments">
      <div class="context-panel__search-row">
        <input v-model="documentQuery" type="search" placeholder="搜索 PDF / Office 文档正文" aria-label="搜索工作区文档正文" />
        <button type="submit" :disabled="!documentQuery.trim()">{{ documentSearching ? '重新搜索' : '搜索' }}</button>
      </div>
      <div v-if="documentSearched" class="context-panel__results" role="region" aria-label="文档搜索结果">
        <span v-if="!documentResults.length">{{ documentSearching ? '正在建立文档索引…' : '没有找到匹配的文档内容' }}</span>
        <button v-for="result in documentResults" :key="`${result.path}:${result.line}`" type="button" @click="openDocumentResult(result)">
          <strong>{{ result.path }}:{{ result.line }}</strong>
          <span>{{ result.snippet }}</span>
        </button>
      </div>
    </form>

    <div v-if="!agents.selectedID" class="context-panel__empty">选择一个 Agent 后，这里会显示它的工作区。</div>
    <div v-else-if="workspace.error && !workspace.overview" class="context-panel__empty">{{ workspace.error }}</div>
    <div v-else ref="fileBody" class="context-panel__file-body" :style="{ gridTemplateColumns: `${panel.treeWidth}px 5px minmax(0, 1fr)` }">
      <WorkspaceFileTree
        :directories="workspace.agentID === agents.selectedID ? workspace.directories : {}"
        :expanded-paths="workspace.expandedPaths"
        :selected-path="workspace.selectedPath"
        :loading-directories="workspace.loadingDirectories"
        @toggle="toggleDirectory"
        @select="selectEntry"
      />
      <div class="context-panel__file-divider" role="separator" tabindex="0" aria-label="调整文件目录宽度" aria-orientation="vertical" :aria-valuenow="panel.treeWidth" aria-valuemin="125" aria-valuemax="400" @pointerdown="beginTreeResize" @pointermove="moveTreeResize" @pointerup="endTreeResize" @pointercancel="endTreeResize" @keydown="keyTreeResize"></div>
      <WorkspacePreview
        :selected-entry="workspace.agentID === agents.selectedID ? workspace.selectedEntry : null"
        :preview="workspace.agentID === agents.selectedID ? workspace.preview : null"
        :loading="workspace.loadingPreview"
      />
    </div>
  </aside>
</template>

<style scoped>
.context-panel { display: flex; min-width: 0; min-height: 0; height: 100%; flex-direction: column; overflow: hidden; border-left: 1px solid var(--h-border); background: var(--h-surface); color: var(--h-text); }
.context-panel__toolbar { display: flex; min-height: 45px; align-items: center; gap: 8px; padding: 6px 12px 6px 15px; border-bottom: 1px solid var(--h-border); font: 12px var(--h-ui); }
.context-panel__toolbar > span { overflow: hidden; flex: 1; color: var(--h-text-muted); text-overflow: ellipsis; white-space: nowrap; }
.context-panel__toolbar button { display: grid; width: 30px; height: 30px; flex: 0 0 30px; place-items: center; border: 0; border-radius: var(--h-radius-sm); background: transparent; color: var(--h-text-secondary); cursor: pointer; }
.context-panel__toolbar button:hover, .context-panel__toolbar button[aria-pressed="true"] { background: var(--h-accent-soft); color: var(--h-accent); }
.context-panel__toolbar svg { width: 17px; height: 17px; fill: none; stroke: currentColor; stroke-width: 1.7; stroke-linecap: round; }
.context-panel__search { padding: 10px 12px; border-bottom: 1px solid var(--h-border); }
.context-panel__search-row { display: flex; gap: 6px; }
.context-panel__search-row input { min-width: 0; flex: 1; padding: 8px 9px; border: 1px solid var(--h-border-strong); border-radius: var(--h-radius-sm); background: var(--h-bg); color: var(--h-text); font: 12px var(--h-ui); }
.context-panel__search-row button { padding: 0 10px; border: 0; border-radius: var(--h-radius-sm); background: var(--h-accent); color: white; font: 12px var(--h-ui); cursor: pointer; }
.context-panel__search-row button:disabled { opacity: .45; cursor: default; }
.context-panel__results { display: grid; gap: 4px; max-height: 180px; margin-top: 8px; overflow: auto; }
.context-panel__results > span { padding: 7px 2px; color: var(--h-text-muted); font: 12px var(--h-ui); }
.context-panel__results button { display: grid; gap: 2px; min-width: 0; padding: 7px 8px; border: 0; border-radius: var(--h-radius-sm); background: var(--h-bg); color: var(--h-text-secondary); font: 11px/1.45 var(--h-ui); text-align: left; cursor: pointer; }
.context-panel__results button:hover { background: var(--h-accent-soft); }
.context-panel__results button strong, .context-panel__results button span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.context-panel__file-body { display: grid; min-width: 0; min-height: 0; flex: 1; overflow: hidden; }
.context-panel__file-body :deep(.workspace-tree), .context-panel__file-body :deep(.workspace-preview) { min-width: 0; min-height: 0; overflow: auto; }
.context-panel__file-body :deep(.workspace-tree) { grid-template-rows: minmax(0, 1fr); border-right: 0; }
.context-panel__file-body :deep(.workspace-tree__header) { display: none; }
.context-panel__file-body :deep(.workspace-tree__collapse), .context-panel__file-body :deep(.workspace-tree__size) { display: none; }
.context-panel__file-divider { width: 5px; background: var(--h-border); cursor: col-resize; touch-action: none; }
.context-panel__file-divider:hover, .context-panel__file-divider:focus-visible { background: var(--h-accent-border); outline: none; }
.context-panel__file-body :deep(.workspace-preview__text) { white-space: pre-wrap; overflow-wrap: anywhere; }
.context-panel__file-body :deep(.workspace-preview__header) { align-items: start; flex-direction: column; gap: 4px; padding: 8px 10px; }
.context-panel__file-body :deep(.workspace-preview__meta) { flex-wrap: wrap; gap: 3px 8px; white-space: normal; }
.context-panel__empty { padding: 28px 18px; color: var(--h-text-muted); font: 13px/1.6 var(--h-ui); }
</style>
