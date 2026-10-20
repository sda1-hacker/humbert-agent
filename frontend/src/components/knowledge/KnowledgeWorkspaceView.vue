<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { Events } from "@wailsio/runtime";
import * as api from "../../api/knowledge.js";
import { useAgentStore } from "../../stores/agents.js";
import { confirmAction } from "../../utils/confirm.js";
import { Message } from "../../utils/uiMessage.js";
import AppPageHeader from "../ui/AppPageHeader.vue";
import SourcePreview from "./SourcePreview.vue";
import EmptyState from "../ui/EmptyState.vue";
import StatusPill from "../ui/StatusPill.vue";

const emit = defineEmits(["manage-settings"]);
const agents = useAgentStore();
const state = ref({ settings: {}, collections: [], jobs: [] });
const selected = ref("");
const documents = ref([]);
const hasMore = ref(false);
const loading = ref(false);
const uploading = ref(false);
const error = ref("");
const actionID = ref("");
const editing = ref(false);
const saving = ref(false);
const form = ref({ id: "", name: "", description: "", agent_ids: [] });
const query = ref("");
const mode = ref("hybrid");
const searching = ref(false);
const searchResult = ref(null);
const preview = ref(null);
const previewOpen = ref(false);
const previewLoading = ref(false);
const previewError = ref("");
const chunks = ref([]);
const chunkDocument = ref(null);
const chunkLoading = ref(false);
let alive = true, pageSerial = 0, stateSerial = 0, searchSerial = 0, previewSerial = 0, chunkSerial = 0;
let timer, unsubscribe, searchCall;

const current = computed(() => state.value.collections.find((c) => c.id === selected.value));
const activeJobs = computed(() => state.value.jobs.filter((j) => ["queued", "processing"].includes(j.status)));
const rows = computed(() => {
  const jobs = new Map();
  for (const j of state.value.jobs) if (j.collection_id === selected.value) jobs.set(j.document_id, j);
  const result = documents.value.map((d) => {
    const j = jobs.get(d.id);
    jobs.delete(d.id);
    return { ...d, ...documentNotices(j?.warnings), status: j && ["queued", "processing"].includes(j.status) ? j.status : d.status };
  });
  for (const j of jobs.values()) if (j.status !== "completed") result.unshift({ id: j.document_id, name: j.name, status: j.status, error: j.error, stage: "", revision: 0, ...documentNotices(j.warnings) });
  return result;
});
const statusNames = { queued: "排队中", processing: "处理中", completed: "已就绪", failed: "失败", cancelled: "已取消", interrupted: "已中断" };
const stages = { source: "保存原件", parse: "解析文档", chunk: "结构切分", images: "识别图片", index: "构建索引", embedding: "生成向量", ready: "准备发布", publish: "发布版本", completed: "完成" };
function message(e) { return e?.message || String(e); }
function agentName(id) { return agents.items.find((a) => a.id === id)?.name || id; }
function size(bytes) { return bytes ? `${(bytes / 1024).toFixed(1)} KB` : ""; }
function statusTone(status) { return ({ completed: "success", failed: "danger", processing: "info", queued: "info", interrupted: "warning" })[status] || "neutral"; }

// 兼顾旧任务里逐张保存的提示，不要求用户为了界面显示重新导入文档。
function documentNotices(values = []) {
  let savedImages = 0;
  const warnings = [];
  for (const text of new Set(values || [])) {
    if (/^图片\s*\d+\s*已保存，未开启图片识别。?$/.test(text)) savedImages++;
    else {
      const summary = text.match(/^已保存\s*(\d+)\s*张图片，未开启图片识别。?$/);
      if (summary) savedImages += Number(summary[1]);
      else warnings.push(text);
    }
  }
  return { imageNotice: savedImages ? `已保存 ${savedImages} 张图片，未开启图片识别` : "", warnings };
}

async function reloadState() {
  const serial = ++stateSerial;
  try {
    const result = await api.knowledgeState();
    if (!alive || serial !== stateSerial) return;
    state.value = { ...result, collections: result.collections || [], jobs: result.jobs || [] };
    if (!state.value.collections.some((c) => c.id === selected.value)) selected.value = state.value.collections[0]?.id || "";
  } catch (e) { if (alive && serial === stateSerial) error.value = message(e); }
}
async function loadDocuments(append = false, quiet = false) {
  const id = selected.value, serial = ++pageSerial;
  if (!id || !state.value.settings.enabled) { documents.value = []; loading.value = false; hasMore.value = false; return; }
  const offset = append ? documents.value.length : 0;
  if (!quiet) loading.value = true;
  try {
    const page = await api.listDocuments(id, offset);
    if (!alive || serial !== pageSerial || selected.value !== id) return;
    documents.value = append ? [...documents.value, ...(page.documents || [])] : (page.documents || []);
    hasMore.value = page.has_more;
    error.value = "";
  } catch (e) { if (alive && serial === pageSerial) error.value = message(e); }
  finally { if (alive && serial === pageSerial) loading.value = false; }
}
async function refresh() { await reloadState(); await loadDocuments(); }
function schedulePoll() {
  clearTimeout(timer);
  // 只有存在在途文档时轮询，离开页面立即注销；单次轮询完成后才安排下一次。
  if (!alive || !activeJobs.value.length && !documents.value.some((d) => d.status === "processing")) return;
  timer = setTimeout(async () => { await reloadState(); await loadDocuments(false, true); schedulePoll(); }, 1800);
}
function edit(collection = null) {
  form.value = collection ? JSON.parse(JSON.stringify(collection)) : { id: "", name: "", description: "", agent_ids: [] };
  editing.value = true;
}
async function save() {
  if (saving.value) return false;
  saving.value = true;
  try {
    const oldIDs = new Set(state.value.collections.map((c) => c.id));
    const result = await api.saveCollection(form.value);
    state.value = result; editing.value = false;
    selected.value = form.value.id || result.collections.find((c) => !oldIDs.has(c.id))?.id || selected.value;
    Message.success("知识库及 Agent 关联已保存，下一轮对话生效");
    return true;
  } catch (e) { Message.error(message(e)); return false; }
  finally { saving.value = false; }
}
async function upload() {
  const id = selected.value;
  uploading.value = true;
  try {
    const jobs = await api.uploadDocuments(id);
    if (jobs.length) Message.success(`已加入 ${jobs.length} 个文档的处理队列`);
    else Message.info("未选择新文档，或相同内容已在知识库中");
    await refresh(); schedulePoll();
  } catch (e) { Message.error(message(e)); }
  finally { uploading.value = false; }
}
async function act(action, row) {
  const collection = selected.value;
  if (action === "delete" && !(await confirmAction({ title: "删除知识库文档", message: `删除“${row.name || row.title}”的索引与记录，已有对话保留当时的来源快照。`, confirmText: "删除", danger: true }))) return;
  if (action === "reindex" && !(await confirmAction({ title: "重新索引文档", message: "用当前解析、分块和向量配置重新处理原件，可能调用已配置的模型。处理期间保留上一版检索结果。", confirmText: "重新索引" }))) return;
  actionID.value = row.id;
  try {
    await ({ cancel: api.cancelDocument, retry: api.retryDocument, reindex: api.reindexDocument, delete: api.deleteDocument, download: api.downloadDocument })[action](collection, row.id);
    if (action !== "download") await refresh(); schedulePoll();
  } catch (e) { Message.error(message(e)); }
  finally { actionID.value = ""; }
}
async function showChunks(row, append = false) {
  const id = selected.value, serial = ++chunkSerial;
  const offset = append ? chunks.value.length : 0;
  if (!append) { chunks.value = []; chunkDocument.value = row; }
  chunkLoading.value = true;
  try {
    const result = await api.previewDocument(id, row.id, offset);
    if (alive && serial === chunkSerial && selected.value === id) chunks.value = append ? [...chunks.value, ...result] : result;
  } catch (e) { if (alive && serial === chunkSerial) Message.error(message(e)); }
  finally { if (serial === chunkSerial) chunkLoading.value = false; }
}
async function search() {
  if (searching.value || !query.value.trim() || !selected.value || !state.value.settings.enabled) return;
  const id = selected.value, serial = ++searchSerial;
  searching.value = true; searchResult.value = null;
  try {
    searchCall = api.searchKnowledge(id, query.value.trim(), mode.value);
    const result = await searchCall;
    if (alive && serial === searchSerial && id === selected.value) searchResult.value = result;
  } catch (e) { if (alive && serial === searchSerial) Message.error(message(e)); }
  finally { if (serial === searchSerial) { searching.value = false; searchCall = null; } }
}
function cancelSearch() { searchSerial++; searchCall?.cancel?.(); searchCall = null; searching.value = false; }
async function openSource(source) {
  const serial = ++previewSerial;
  preview.value = source; previewOpen.value = true; previewLoading.value = true; previewError.value = "";
  try { const full = await api.previewKnowledgeSource(source.id); if (alive && serial === previewSerial) preview.value = full; }
  catch (e) { if (alive && serial === previewSerial) previewError.value = message(e); }
  finally { if (serial === previewSerial) previewLoading.value = false; }
}
watch(selected, () => {
  cancelSearch(); previewSerial++; previewOpen.value = false; chunkSerial++; chunks.value = []; chunkDocument.value = null;
  documents.value = []; hasMore.value = false; searchResult.value = null;
  void loadDocuments();
});
watch(() => activeJobs.value.length, schedulePoll);
onMounted(async () => {
  unsubscribe = Events.On("knowledge.changed", () => { void reloadState().then(() => loadDocuments(false, true)); });
  await Promise.allSettled([reloadState(), agents.items.length ? Promise.resolve() : agents.load()]);
  schedulePoll();
});
onUnmounted(() => { alive = false; pageSerial++; stateSerial++; chunkSerial++; previewSerial++; cancelSearch(); clearTimeout(timer); unsubscribe?.(); });
</script>

<template>
  <section class="knowledge-workspace">
    <div class="knowledge-inner">
      <AppPageHeader eyebrow="Agent Knowledge" title="知识库" description="保存原件，建立索引，为 Agent 提供可追溯的资料。">
        <template #actions><a-button @click="emit('manage-settings')">RAG 配置</a-button><a-button type="primary" @click="edit()">新建知识库</a-button></template>
      </AppPageHeader>
      <a-alert v-if="!state.settings.enabled" type="info">先配置数据库和 Embedding，再启用知识库。<a-button type="text" @click="emit('manage-settings')">前往配置</a-button></a-alert>
      <a-alert v-if="error" type="error">{{ error }} <a-button size="mini" @click="refresh">重试</a-button></a-alert>
      <div v-if="state.collections.length" class="knowledge-layout">
        <aside class="knowledge-list" aria-label="知识库列表">
          <span class="collection-label">全部知识库 · {{ state.collections.length }}</span>
          <button v-for="collection in state.collections" :key="collection.id" type="button" class="collection-button" :class="{ selected: selected === collection.id }" @click="selected = collection.id">
            <strong>{{ collection.name }}</strong><span>{{ collection.agent_ids?.length || 0 }} 个 Agent</span>
          </button>
        </aside>
        <main v-if="current" class="knowledge-main">
          <header class="collection-header">
            <div><h2>{{ current.name }}</h2><p v-if="current.description">{{ current.description }}</p><p class="agents-line">关联：{{ current.agent_ids?.length ? current.agent_ids.map(agentName).join('、') : '尚未关联 Agent' }}</p></div>
            <a-button @click="edit(current)">编辑 / 关联 Agent</a-button>
          </header>
          <a-tabs default-active-key="documents">
            <a-tab-pane key="documents" title="文档">
              <div class="document-toolbar"><span>支持 PDF、Office、Markdown、文本及图片</span><div><a-button :loading="loading" @click="loadDocuments()">刷新</a-button><a-button type="primary" :loading="uploading" :disabled="!state.settings.enabled" @click="upload">上传文档</a-button></div></div>
              <a-spin :loading="loading" style="width:100%">
                <EmptyState v-if="!rows.length" title="还没有文档" description="上传后会在后台解析和索引" />
                <article v-for="row in rows" :key="row.id" class="document-row">
                  <div class="document-copy">
                    <button type="button" class="document-title" :disabled="!row.revision" @click="showChunks(row)">{{ row.name || row.title }}</button>
                    <div class="document-meta"><StatusPill :tone="statusTone(row.status)" :label="statusNames[row.status] || row.status" /><span v-if="row.size">{{ size(row.size) }}</span><span v-if="['processing','queued'].includes(row.status)">{{ stages[row.stage] || '等待处理' }}</span><span v-if="row.revision">已发布 v{{ row.revision }}</span></div>
                    <p v-if="row.error" class="document-error">{{ row.error }}</p>
                    <p v-if="row.imageNotice" class="document-notice">{{ row.imageNotice }}</p>
                    <details v-if="row.warnings.length" class="document-warnings"><summary>{{ row.warnings.length }} 条处理提示</summary><ul><li v-for="(warning,i) in row.warnings" :key="i">{{ warning }}</li></ul></details>
                  </div>
                  <div class="document-actions"><a-button v-if="['processing','queued'].includes(row.status)" type="text" :loading="actionID === row.id" @click="act('cancel',row)">取消</a-button><a-button v-if="row.retryable || ['failed','cancelled','interrupted'].includes(row.status)" type="text" :loading="actionID === row.id" @click="act('retry',row)">重试</a-button><a-button v-if="row.revision" type="text" @click="act('download',row)">下载原件</a-button><a-button v-if="row.revision && !['processing','queued'].includes(row.status)" type="text" :disabled="actionID === row.id" @click="act('reindex',row)">重新索引</a-button><a-button type="text" status="danger" :disabled="actionID === row.id" @click="act('delete',row)">删除</a-button></div>
                </article>
                <a-button v-if="hasMore" long :loading="loading" @click="loadDocuments(true)">加载更多文档</a-button>
              </a-spin>
              <p class="knowledge-note">上传相同内容自动跳过，避免重复向量化。失败或取消不影响上一版已发布内容。图片识别是否调用模型，由 RAG 配置决定。</p>
            </a-tab-pane>
            <a-tab-pane key="search" title="检索验证">
              <p class="knowledge-note">只调用检索与配置的 Embedding / Rerank，不调用回答模型。分数用于排序，不代表答案准确率。</p>
              <div class="query-row"><a-input v-model="query" placeholder="输入需要从资料中查找的问题" :max-length="2000" @press-enter="search" /><a-select v-model="mode" :options="[{value:'hybrid',label:'双路融合'},{value:'keyword',label:'BM25'},{value:'semantic',label:'向量'}]" style="width:120px;flex-shrink:0" /><a-button type="primary" :loading="searching" :disabled="!query.trim() || !state.settings.enabled" @click="search">检索</a-button><a-button v-if="searching" @click="cancelSearch">取消</a-button></div>
              <template v-if="searchResult"><p class="knowledge-note">返回 {{ searchResult.sources.length }} 个来源 · 上下文预算 {{ searchResult.diagnostics?.context_tokens || 0 }} · 精排 {{ searchResult.diagnostics?.rerank?.outcome || '未启用' }}<span v-if="searchResult.diagnostics?.retrieval?.degraded"> · 已降级</span></p><EmptyState v-if="!searchResult.sources.length" title="没有符合阈值的资料" description="可换词、降低阈值或检查文档解析结果" /><button v-for="(source,i) in searchResult.sources" :key="source.id" type="button" class="search-hit" @click="openSource(source)"><strong>{{ i + 1 }}. {{ source.title }}</strong><span>{{ source.header }}</span><p>{{ source.excerpt }}</p><small>向量 {{ source.vector_score?.toFixed(4) || '—' }} · BM25 {{ source.keyword_score?.toFixed(4) || '—' }} · 查看来源 ›</small></button><details class="knowledge-note"><summary>检索诊断</summary><pre>{{ JSON.stringify(searchResult.diagnostics,null,2) }}</pre></details></template>
            </a-tab-pane>
          </a-tabs>
        </main>
      </div>
      <EmptyState v-else title="创建第一个知识库" description="上传文档后再关联需要使用它的 Agent" />
    </div>
    <a-modal v-model:visible="editing" :title="form.id ? '编辑知识库' : '新建知识库'" :width="560" :modal-style="{maxWidth:'calc(100vw - 32px)'}" :ok-loading="saving" :on-before-ok="save" :mask-closable="false">
      <a-form :model="form" layout="vertical" :disabled="saving"><a-form-item label="名称"><a-input v-model="form.name" :max-length="100" /></a-form-item><a-form-item label="说明"><a-textarea v-model="form.description" :max-length="800" placeholder="描述资料范围，帮助 Agent 选择知识库" /></a-form-item><a-form-item label="关联 Agent"><a-select v-model="form.agent_ids" multiple allow-search allow-clear placeholder="选择可使用此知识库的 Agent"><a-option v-for="agent in agents.items" :key="agent.id" :value="agent.id">{{ agent.name }}</a-option></a-select><template #extra>在该 Agent 的所有会话中生效；其他 Agent 无权通过知识库工具查询。</template></a-form-item></a-form>
    </a-modal>
    <a-modal :visible="!!chunkDocument" :title="chunkDocument?.name || '文档分块'" :width="760" :modal-style="{maxWidth:'calc(100vw - 32px)',maxHeight:'90vh',display:'inline-flex',flexDirection:'column'}" :body-style="{minHeight:0,overflowY:'auto'}" :footer="false" @cancel="chunkDocument = null; chunkSerial++">
      <a-spin :loading="chunkLoading" style="width:100%"><div class="chunk-list"><p class="knowledge-note">展示已发布子块，可打开查看父块上下文及原文位置。</p><button v-for="(source,i) in chunks" :key="source.id" type="button" class="search-hit" @click="openSource(source)"><strong>分块 {{ i + 1 }} · {{ source.header || source.title }}</strong><p>{{ source.excerpt }}</p></button><a-button v-if="chunks.length && chunks.length % 20 === 0" long @click="showChunks(chunkDocument,true)">更多分块</a-button><EmptyState v-if="!chunks.length && !chunkLoading" title="暂无已发布分块" /></div></a-spin>
    </a-modal>
    <SourcePreview v-model:visible="previewOpen" :source="preview" :loading="previewLoading" :error="previewError" />
  </section>
</template>

<style scoped>
.knowledge-workspace { height: 100%; overflow: auto; background: var(--h-bg); }
.knowledge-inner { max-width: 1160px; margin: 0 auto; padding: var(--h-page-top) 32px var(--h-page-bottom); display: grid; gap: 24px; }
.knowledge-layout { display: grid; grid-template-columns: 176px minmax(0,1fr); gap: 20px; align-items: start; }
.knowledge-list { display: flex; flex-direction: column; gap: 6px; }
.collection-label { padding: 4px 12px 8px; color: var(--h-text-muted); font-size: 12px; }
.collection-button { display: grid; gap: 4px; padding: 12px; background: transparent; border: 1px solid transparent; border-radius: var(--h-radius-md); text-align: left; cursor: pointer; color: var(--h-text); font: inherit; overflow-wrap: anywhere; }
.collection-button strong { font-size: 13px; font-weight: 500; }
.collection-button span { font-size: 12px; color: var(--h-text-muted); }
.collection-button:hover { background: var(--h-surface-hover); }
.collection-button.selected { border-color: var(--h-border); background: var(--h-surface); color: var(--h-accent); }
.knowledge-main { min-width: 0; padding: var(--h-panel-padding); border-radius: var(--h-radius-lg); background: var(--h-surface); }
.collection-header { display: flex; gap: 16px; justify-content: space-between; align-items: flex-start; margin-bottom: 16px; }
.collection-header h2 { margin: 0; font-size: 16px; font-weight: 500; }
.collection-header p { margin: 6px 0 0; color: var(--h-text-muted); font-size: 12px; line-height: 1.65; }
.agents-line { max-width: 550px; overflow-wrap: anywhere; }
.document-toolbar, .document-toolbar>div { display: flex; align-items: center; gap: 8px; }
.document-toolbar { justify-content: space-between; flex-wrap: wrap; gap: 12px; margin: 4px 0 8px; }
.document-toolbar>span { font-size: 12px; color: var(--h-text-muted); }
.document-row { display: flex; flex-wrap: wrap; align-items: flex-start; justify-content: space-between; gap: 10px 14px; padding: 18px 0; border-bottom: 1px solid var(--h-border); }
.document-copy { flex: 1 1 240px; min-width: 0; }
.document-title { padding: 0; background: none; border: 0; color: var(--h-text); font: inherit; font-weight: 550; text-align: left; overflow-wrap: anywhere; cursor: pointer; }
.document-title:disabled { cursor: default; }
.document-meta { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-top: 8px; font-size: 12px; color: var(--h-text-muted); }
.document-actions { display: flex; flex-wrap: wrap; gap: 2px; justify-content: flex-end; margin-left: auto; }
.document-actions :deep(.arco-btn) { height: 28px; padding: 0 8px; font-size: 12px; }
.document-error { color: var(--h-danger); font-size: 12px; line-height: 1.6; overflow-wrap: anywhere; }
.document-notice { margin: 10px 0 0; color: var(--h-text-muted); font-size: 12px; line-height: 1.6; }
.document-warnings { margin-top: 10px; color: var(--h-warning); font-size: 12px; line-height: 1.7; }
.document-warnings summary { width: fit-content; cursor: pointer; }
.document-warnings ul { max-height: 200px; overflow: auto; margin: 8px 0 0; padding-left: 20px; overflow-wrap: anywhere; }
.knowledge-note { font-size: 12px; line-height: 1.7; color: var(--h-text-muted); margin: 16px 0; }
.knowledge-note pre { white-space: pre-wrap; overflow-wrap: anywhere; }
.query-row { display: flex; align-items: center; gap: 8px; }
.search-hit { display: block; width: 100%; text-align: left; padding: 14px; margin: 12px 0; border: 1px solid var(--h-border); border-radius: var(--h-radius-md); background: var(--h-surface); color: var(--h-text); font: inherit; cursor: pointer; overflow-wrap: anywhere; }
.search-hit strong { font-size: 13px; font-weight: 500; }
.search-hit:hover { border-color: var(--h-accent); }
.search-hit>span, .search-hit small { display: block; margin-top: 8px; color: var(--h-text-muted); font-size: 12px; white-space: pre-wrap; }
.search-hit p { line-height: 1.65; font-size: 13px; white-space: pre-wrap; margin-bottom: 0; max-height: 110px; overflow: hidden; }
@media (max-width: 960px) { .knowledge-layout { grid-template-columns: 150px minmax(0,1fr); gap: 18px; } .knowledge-inner { padding: 24px 20px; } .document-row { flex-direction: column; } .document-actions { max-width: none; } }
@media (max-width: 650px) { .knowledge-layout { grid-template-columns: minmax(0,1fr); } .knowledge-list { flex-direction: row; overflow-x: auto; } .collection-label { display: none; } .collection-button { flex-shrink: 0; min-width: 140px; } .collection-header, .document-toolbar { flex-direction: column; } .query-row { flex-wrap: wrap; } }
</style>
