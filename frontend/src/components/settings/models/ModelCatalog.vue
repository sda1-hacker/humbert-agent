<script setup>
import { computed, onMounted, onUnmounted, reactive, ref, watch } from "vue";
import { IconDelete, IconEdit, IconPlus, IconSearch, IconRefresh } from "@arco-design/web-vue/es/icon";
import { useModelStore } from "../../../stores/models.js";
import { providerPresets, presetForProvider, formatTokens } from "../../../utils/modelCatalog.js";
import { confirmAction } from "../../../utils/confirm.js";
import { Message } from "../../../utils/uiMessage.js";
import { t } from "../../../i18n/index.js";
import ModelCapabilityBadges from "../../models/ModelCapabilityBadges.vue";
import StatusPill from "../../ui/StatusPill.vue";
import EmptyState from "../../ui/EmptyState.vue";
import ModelEditor from "./ModelEditor.vue";

const store = useModelStore();
const selectedKey = ref("");
const query = ref("");
const error = ref("");
const loading = ref(false);
const saving = ref(false);
const removing = ref(false);
const testingID = ref("");
const diagnostics = ref({});
const editorOpen = ref(false);
const editingModel = ref(null);
const editingPreset = ref(null);
const form = reactive({ name: "", type: "openai_compatible", baseURL: "", apiKey: "", clearAPIKey: false });
const initialConnection = ref("");
let alive = true;

const selectedProvider = computed(() => store.providers.find((provider) => `provider:${provider.id}` === selectedKey.value));
const selectedPreset = computed(() => selectedKey.value.startsWith("preset:")
  ? providerPresets.find((preset) => `preset:${preset.id}` === selectedKey.value)
  : presetForProvider(selectedProvider.value));
const connectionDirty = computed(() => JSON.stringify(form) !== initialConnection.value);
const providerModels = computed(() => store.models.filter((model) => model.providerID === selectedProvider.value?.id));
const working = computed(() => saving.value || removing.value || loading.value);
const unavailable = computed(() => !selectedProvider.value || connectionDirty.value || working.value);
const title = computed(() => selectedProvider.value?.name || selectedPreset.value?.name || t("自定义供应商"));
const protocolNames = { openai: "OpenAI", openai_compatible: "OpenAI Compatible", ollama: "Ollama" };
const configured = computed(() => store.providers.filter((provider) => matches(provider.name, provider.baseURL)));
const available = computed(() => providerPresets.filter((preset) => !store.providers.some((provider) => presetForProvider(provider)?.id === preset.id) && matches(preset.name, preset.baseURL)));

function matches(...values) { return values.join(" ").toLowerCase().includes(query.value.trim().toLowerCase()); }
function modelCount(id) { return store.models.filter((model) => model.providerID === id).length; }
function initial(name) { return [...name][0]?.toUpperCase() || "?"; }
function restoreConnection() {
  const source = selectedProvider.value || selectedPreset.value;
  Object.assign(form, { name: source?.name || "", type: source?.type || "openai_compatible", baseURL: source?.baseURL || "", apiKey: "", clearAPIKey: false });
  initialConnection.value = JSON.stringify(form);
}
function applySelection(key) {
  selectedKey.value = key;
  editorOpen.value = false;
  restoreConnection();
}
async function select(key) {
  if (working.value || key === selectedKey.value) return;
  if (connectionDirty.value && !(await confirmAction({ title: t("连接配置尚未保存"), message: t("切换供应商会放弃当前未保存的连接修改。"), confirmText: t("放弃修改") }))) return;
  applySelection(key);
}
watch(() => store.providers.map((provider) => provider.id).join(","), () => {
  if (!selectedKey.value || selectedKey.value.startsWith("provider:") && !selectedProvider.value) {
    applySelection(store.providers[0] ? `provider:${store.providers[0].id}` : `preset:${providerPresets[0].id}`);
  }
}, { immediate: true });

async function refresh() {
  if (loading.value || working.value) return;
  const initialLoad = !store.revision;
  const selectionAtStart = selectedKey.value;
  loading.value = true;
  try {
    await store.load();
    if (!alive) return;
    error.value = "";
    if (!connectionDirty.value && selectedKey.value === selectionAtStart) {
      if (initialLoad && store.providers[0]) applySelection(`provider:${store.providers[0].id}`);
      else restoreConnection();
    }
  }
  catch (reason) { if (alive) error.value = reason?.message || String(reason); }
  finally { if (alive) loading.value = false; }
}
onMounted(() => { if (!store.revision) void refresh(); });
onUnmounted(() => { alive = false; form.apiKey = ""; });
// 连接或模型参数更新后，旧诊断不再代表当前配置。
watch(() => store.revision, () => { diagnostics.value = {}; });

function changeProtocol(type) {
  form.baseURL = providerPresets.find((preset) => preset.type === type && type !== "openai_compatible")?.baseURL || "";
}
async function saveProvider() {
  if (working.value) return;
  if (!form.name.trim()) { Message.warning(t("请输入供应商名称")); return; }
  if (form.type === "openai_compatible" && !form.baseURL.trim()) { Message.warning(t("请填写兼容服务的 Base URL")); return; }
  saving.value = true;
  const owner = selectedProvider.value;
  const request = { name: form.name.trim(), type: form.type, baseURL: form.baseURL.trim(), apiKey: form.clearAPIKey ? "" : form.apiKey };
  try {
    // 空密钥默认保持原值，只有显式选择清除或填写新值才更新凭据。
    const result = owner
      ? await store.updateProvider(owner.id, { ...request, updateAPIKey: form.clearAPIKey || form.apiKey.trim() !== "" })
      : await store.createProvider(request);
    if (!alive) return;
    applySelection(`provider:${result.id}`);
    Message.success(t(owner ? "供应商已更新" : "供应商已创建"));
  } catch (reason) { if (alive) Message.error(reason?.message || String(reason)); }
  finally { if (alive) saving.value = false; }
}
async function removeProvider() {
  const provider = selectedProvider.value;
  if (!provider || working.value) return;
  if (!(await confirmAction({ title: t("删除供应商"), message: t("删除供应商“{name}”？请先移除或迁移它的模型；历史会话不受影响。", { name: provider.name }), confirmText: t("删除"), danger: true }))) return;
  removing.value = true;
  try { await store.deleteProvider(provider.id); if (alive) Message.success(t("供应商已删除")); }
  catch (reason) { if (alive) Message.error(reason?.message || String(reason)); }
  finally { if (alive) removing.value = false; }
}
function openModel(model = null, preset = null) {
  if (unavailable.value) return;
  editingModel.value = model;
  editingPreset.value = preset;
  editorOpen.value = true;
}
function chooseModel(id) { openModel(null, selectedPreset.value?.models.find((model) => model.id === id) || null); }
async function removeModel(model) {
  if (working.value) return;
  if (!(await confirmAction({ title: t("删除模型"), message: t("删除模型“{name}”？历史会话会保留；仍被 Agent 或视觉辅助设置使用时，需要先切换相关配置。", { name: model.displayName }), confirmText: t("删除"), danger: true }))) return;
  removing.value = true;
  try { await store.deleteModel(model.id); if (alive) Message.success(t("模型已删除")); }
  catch (reason) { if (alive) Message.error(reason?.message || String(reason)); }
  finally { if (alive) removing.value = false; }
}
async function test(model) {
  if (testingID.value || unavailable.value) return;
  const revision = store.revision;
  testingID.value = model.id;
  diagnostics.value = { ...diagnostics.value, [model.id]: null };
  try {
    const result = await store.diagnoseModel(model.id);
    if (alive && revision === store.revision) diagnostics.value = { ...diagnostics.value, [model.id]: result };
  } catch (reason) { if (alive && revision === store.revision) Message.error(reason?.message || String(reason)); }
  finally { if (alive) testingID.value = ""; }
}
</script>

<template>
  <div class="model-catalog">
    <a-alert v-if="error" type="error">{{ error }} <a-button type="text" @click="refresh">重新加载</a-button></a-alert>
    <div class="provider-board" :aria-busy="loading">
      <aside class="provider-sidebar" aria-label="供应商列表">
        <a-input v-model="query" allow-clear placeholder="搜索供应商" aria-label="搜索供应商"><template #prefix><IconSearch /></template></a-input>
        <nav class="provider-navigation" aria-label="选择供应商">
          <template v-if="configured.length">
            <span class="provider-group">已配置</span>
            <button v-for="provider in configured" :key="provider.id" type="button" class="provider-choice" :class="{'is-selected': selectedKey === `provider:${provider.id}`}" :aria-pressed="selectedKey === `provider:${provider.id}`" :disabled="working" @click="select(`provider:${provider.id}`)">
              <span class="provider-mark" aria-hidden="true">{{ initial(provider.name) }}</span><span class="provider-choice-copy"><strong>{{ provider.name }}</strong><small>{{ protocolNames[provider.type] }}</small></span><span class="provider-count">{{ modelCount(provider.id) }}</span>
            </button>
          </template>
          <template v-if="available.length">
            <span class="provider-group">快速添加</span>
            <button v-for="preset in available" :key="preset.id" type="button" class="provider-choice" :class="{'is-selected': selectedKey === `preset:${preset.id}`}" :aria-pressed="selectedKey === `preset:${preset.id}`" :disabled="working" @click="select(`preset:${preset.id}`)">
              <span class="provider-mark" aria-hidden="true">{{ initial(preset.name) }}</span><span class="provider-choice-copy"><strong>{{ preset.name }}</strong><small>{{ preset.type === 'ollama' ? t('本地服务') : t('API 服务') }}</small></span><IconPlus class="provider-add-icon" />
            </button>
          </template>
          <p v-if="!configured.length && !available.length" class="list-empty">没有匹配的供应商</p>
        </nav>
        <div class="provider-sidebar-actions"><a-button long :disabled="working" @click="select('custom')"><template #icon><IconPlus /></template>自定义供应商</a-button></div>
      </aside>
      <main class="provider-detail">
        <header class="provider-heading"><div><div class="provider-heading-title"><h2>{{ title }}</h2><StatusPill :label="t(selectedProvider ? '已配置' : '待配置')" :tone="selectedProvider ? 'info' : 'neutral'" :dot="false" /></div><p v-if="!selectedProvider">{{ selectedPreset ? t('连接供应商后，选择模型即可自动填写参数。') : t('连接兼容服务或自建网关，并添加需要使用的模型。') }}</p></div><div class="provider-heading-actions"><a-button type="text" :loading="loading" :disabled="working" aria-label="刷新配置" title="刷新配置" @click="refresh"><template #icon><IconRefresh /></template></a-button><a-button v-if="selectedProvider" type="text" status="danger" :disabled="working" aria-label="删除供应商" title="删除供应商" @click="removeProvider"><template #icon><IconDelete /></template></a-button></div></header>
        <section class="connection-section" aria-label="连接设置">
          <a-form :model="form" layout="vertical" :disabled="working">
            <template v-if="!selectedPreset"><div class="connection-grid"><a-form-item label="名称"><a-input v-model="form.name" :max-length="100" placeholder="例如：我的模型服务" /></a-form-item><a-form-item label="API 类型"><a-select v-model="form.type" @change="changeProtocol"><a-option value="openai_compatible">OpenAI Compatible</a-option><a-option value="openai">OpenAI</a-option><a-option value="ollama">Ollama</a-option></a-select></a-form-item></div></template>
            <a-form-item v-if="form.type !== 'ollama'" label="API Key"><a-input-password v-model="form.apiKey" :disabled="working || form.clearAPIKey" autocomplete="new-password" :placeholder="selectedProvider?.hasCredential ? t('已保存，留空保留') : t('输入 API Key')" /><template #extra>密钥仅存入本机凭据存储，不会回显。</template></a-form-item>
            <a-form-item label="Base URL"><a-input v-model="form.baseURL" :readonly="form.type === 'openai'" :placeholder="form.type === 'ollama' ? 'http://127.0.0.1:11434' : 'https://example.com/v1'" /><template v-if="form.type === 'openai'" #extra>OpenAI 官方连接使用固定地址；代理或网关请选择兼容接口。</template></a-form-item>
            <details v-if="selectedPreset || selectedProvider?.hasCredential" class="connection-options"><summary>连接选项</summary><div class="connection-options-body"><div v-if="selectedPreset" class="connection-grid"><a-form-item label="名称"><a-input v-model="form.name" :max-length="100" /></a-form-item><a-form-item label="API 类型"><a-select v-model="form.type" @change="changeProtocol"><a-option value="openai_compatible">OpenAI Compatible</a-option><a-option value="openai">OpenAI</a-option><a-option value="ollama">Ollama</a-option></a-select></a-form-item></div><a-checkbox v-if="selectedProvider?.hasCredential" v-model="form.clearAPIKey" @change="form.apiKey = ''">保存时清除已有密钥</a-checkbox></div></details>
            <div v-if="!selectedProvider || connectionDirty" class="connection-actions"><span v-if="connectionDirty" class="unsaved-note">连接有未保存的修改</span><a-button v-if="connectionDirty" :disabled="working" @click="restoreConnection">撤销修改</a-button><a-button type="primary" :loading="saving" :disabled="loading || removing || !form.name.trim()" @click="saveProvider">{{ t(selectedProvider ? '保存连接' : '添加供应商') }}</a-button></div>
          </a-form>
        </section>
        <section class="models-section" aria-label="供应商模型">
          <header class="models-heading"><h3>已添加的模型 <span>{{ providerModels.length }}</span></h3><span class="section-hint">供 Agent 与视觉辅助使用</span></header>
          <p v-if="!selectedProvider" class="model-help">先保存连接，再添加模型。</p>
          <p v-else-if="connectionDirty" class="model-help">请先保存连接修改，再管理或测试模型。</p>
          <div class="add-model-row"><a-select :model-value="''" allow-search :disabled="unavailable" placeholder="选择模型，自动填写参数" aria-label="选择预设模型" @change="chooseModel"><a-option v-for="preset in selectedPreset?.models || []" :key="preset.id" :value="preset.id" :label="`${preset.displayName} ${preset.id}`" :disabled="providerModels.some((model) => model.modelName === preset.id)"><span class="preset-option"><span>{{ preset.displayName }}</span><small>{{ formatTokens(preset.contextWindow) }}</small></span></a-option><a-option value="__custom__">自定义模型</a-option></a-select><a-button :disabled="unavailable" @click="openModel()"><template #icon><IconPlus /></template>自定义模型</a-button></div>
          <div v-if="providerModels.length" class="model-items"><article v-for="model in providerModels" :key="model.id" class="model-item"><div class="model-row"><div class="model-copy"><div class="model-title-line"><button type="button" class="model-title" :disabled="unavailable" @click="openModel(model)">{{ model.displayName }}</button><StatusPill v-if="!model.enabled" label="已停用" :dot="false" /></div><code class="model-id">{{ model.modelName }}</code><div class="model-facts"><span :title="model.contextWindow.toLocaleString()">{{ t('上下文 {size}', {size: formatTokens(model.contextWindow)}) }}</span><span :title="model.maxOutputTokens.toLocaleString()">{{ t('输出 {size}', {size: formatTokens(model.maxOutputTokens)}) }}</span><ModelCapabilityBadges :capabilities="model.capabilities" /></div></div><div class="model-actions"><a-button type="text" size="small" :loading="testingID === model.id" :disabled="unavailable || !!testingID && testingID !== model.id" @click="test(model)">测试</a-button><a-button type="text" size="small" :disabled="unavailable" aria-label="编辑模型" title="编辑模型" @click="openModel(model)"><template #icon><IconEdit /></template></a-button><a-button type="text" size="small" status="danger" :disabled="unavailable || testingID === model.id" aria-label="删除模型" title="删除模型" @click="removeModel(model)"><template #icon><IconDelete /></template></a-button></div></div><div v-if="diagnostics[model.id]" class="model-diagnostic" :class="{'has-error': !diagnostics[model.id].success}" role="status"><strong>{{ t(diagnostics[model.id].summary) }}</strong><span v-if="diagnostics[model.id].durationMS"> · {{ diagnostics[model.id].durationMS }} ms</span><p>{{ t(diagnostics[model.id].action) }}</p></div></article></div>
          <EmptyState v-else-if="selectedProvider" title="还没有模型" description="选择预设模型自动填入参数，或添加自定义模型。" compact />
          <p class="model-footnote">保存配置不会调用模型；点击“测试”会发送少量请求，可能产生费用。</p>
        </section>
      </main>
    </div>
    <ModelEditor v-model:visible="editorOpen" :provider-id="selectedProvider?.id || ''" :model="editingModel" :preset="editingPreset" />
  </div>
</template>

<style scoped>
.model-catalog { display: flex; flex-direction: column; gap: 16px; height: 100%; min-width: 0; min-height: 0; }
.provider-board { display: grid; grid-template-columns: 206px minmax(0,1fr); flex: 1; min-height: 360px; overflow: hidden; border-radius: var(--h-radius-lg); background: var(--h-surface); }
.provider-sidebar { display: flex; flex-direction: column; gap: 16px; min-width: 0; min-height: 0; padding: 18px 12px; border-right: 1px solid var(--h-border); }
.provider-navigation { flex: 1; min-height: 0; overflow-y: auto; }
.provider-group { display: block; padding: 12px 10px 8px; color: var(--h-text-muted); font-size: 12px; }
.provider-group:first-child { padding-top: 0; }
.provider-choice { display: flex; align-items: center; gap: 10px; width: 100%; padding: 11px 10px; border: 0; border-radius: var(--h-radius-sm); background: transparent; color: var(--h-text-secondary); text-align: left; font: inherit; cursor: pointer; }
.provider-choice:hover { background: var(--h-surface-hover); }
.provider-choice.is-selected { background: var(--h-accent-soft); color: var(--h-accent); }
.provider-choice:focus-visible, .model-title:focus-visible { outline: 2px solid var(--h-accent-border); outline-offset: 2px; }
.provider-choice:disabled { cursor: default; opacity: .6; }
.provider-mark { display: flex; align-items: center; justify-content: center; flex: 0 0 28px; width: 28px; height: 28px; border-radius: 6px; background: var(--h-bg); color: var(--h-text-muted); font-size: 13px; font-weight: 500; }
.provider-choice-copy { display: grid; flex: 1; min-width: 0; gap: 3px; }
.provider-choice strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; font-weight: 500; }
.provider-choice small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--h-text-muted); font-size: 12px; }
.provider-count, .provider-add-icon { flex-shrink: 0; color: var(--h-text-muted); font-size: 12px; }
.provider-sidebar-actions { display: flex; align-items: center; gap: 2px; }
.provider-sidebar-actions :deep(.arco-btn) { font-size: 12px; }
.provider-sidebar-actions :deep(.arco-btn-long) { flex: 1; }
.list-empty { padding: 8px; color: var(--h-text-muted); font-size: 12px; line-height: 1.6; }
.provider-detail { min-width: 0; min-height: 0; overflow-y: auto; padding: 24px 28px; }
.provider-heading { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; margin-bottom: 18px; }
.provider-heading-title { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; }
.provider-heading-actions { display: flex; flex-shrink: 0; gap: 2px; }
.provider-heading h2 { margin: 0; overflow-wrap: anywhere; font-size: 20px; font-weight: 500; line-height: 1.3; }
.provider-heading p { margin: 8px 0 0; color: var(--h-text-muted); font-size: 12px; line-height: 1.65; }
.connection-section :deep(.arco-form) { display: grid; gap: 12px; }
.connection-section :deep(.arco-form-item) { margin-bottom: 0; }
.connection-section :deep(.arco-form-item-extra) { color: var(--h-text-muted); font-size: 12px; line-height: 1.6; }
.connection-grid { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 16px; }
.connection-options summary { width: fit-content; color: var(--h-text-muted); font-size: 12px; cursor: pointer; }
.connection-options-body { display: grid; gap: 12px; padding-top: 14px; }
.connection-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; align-items: center; gap: 8px; }
.unsaved-note { margin-right: auto; color: var(--h-text-muted); font-size: 12px; }
.models-section { margin-top: 20px; padding-top: 20px; border-top: 1px solid var(--h-border); }
.models-heading { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: baseline; gap: 8px; margin-bottom: 16px; }
.models-heading h3 { margin: 0; font-size: 14px; font-weight: 500; }
.models-heading h3 span { margin-left: 6px; color: var(--h-text-muted); font-size: 12px; }
.section-hint, .model-help, .model-footnote { margin: 8px 0; color: var(--h-text-muted); font-size: 12px; line-height: 1.65; }
.add-model-row { display: flex; gap: 8px; }
.add-model-row :deep(.arco-select) { min-width: 0; flex: 1; }
.add-model-row > :deep(.arco-btn) { flex-shrink: 0; }
.preset-option { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; }
.preset-option small { color: var(--h-text-muted); font-size: 12px; }
.model-items { margin-top: 12px; }
.model-item { padding: 16px 0; }
.model-item + .model-item { border-top: 1px solid var(--h-border); }
.model-row { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
.model-copy { min-width: 0; flex: 1; }
.model-title-line { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.model-title { padding: 0; border: 0; background: none; color: var(--h-text); font: inherit; font-weight: 500; text-align: left; overflow-wrap: anywhere; cursor: pointer; }
.model-title:disabled { cursor: default; }
.model-id { display: block; margin-top: 4px; color: var(--h-text-muted); font: 12px/1.6 var(--h-mono); overflow-wrap: anywhere; }
.model-facts { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 12px; margin-top: 8px; color: var(--h-text-muted); font-size: 12px; }
.model-facts > span { white-space: nowrap; }
.model-facts :deep(.capability-badge) { border: 0; padding: 1px 5px; background: var(--h-bg); font-size: 12px; }
.model-actions { display: flex; gap: 1px; flex-shrink: 0; }
.model-actions :deep(.arco-btn) { height: 28px; padding: 0 6px; font-size: 12px; }
.model-diagnostic { margin-top: 12px; padding: 10px 12px; background: var(--h-bg); border-radius: var(--h-radius-sm); color: var(--h-text-secondary); font-size: 12px; line-height: 1.65; }
.model-diagnostic strong { font-weight: 500; }
.model-diagnostic p { margin: 4px 0 0; color: var(--h-text-muted); }
.model-diagnostic.has-error strong { color: var(--h-danger); }
.model-footnote { margin: 16px 0 0; }
@media (max-width: 1100px) { .provider-board { grid-template-columns: 180px minmax(0,1fr); } .provider-detail { padding: 20px; } .provider-mark { display: none; } .model-row { flex-wrap: wrap; } .model-actions { margin-left: auto; } }
@media (max-width: 900px) { .model-catalog { height: auto; } .provider-board { grid-template-columns: minmax(0,1fr); min-height: 0; overflow: visible; } .provider-sidebar { border-right: 0; border-bottom: 1px solid var(--h-border); gap: 12px; } .provider-navigation { display: flex; gap: 6px; overflow-x: auto; overflow-y: hidden; } .provider-group { display: none; } .provider-choice { width: auto; min-width: 130px; flex-shrink: 0; padding: 9px 10px; } .provider-choice small { display: none; } .provider-sidebar-actions { justify-content: flex-end; } .provider-sidebar-actions :deep(.arco-btn-long) { width: auto; flex: none; } .provider-detail { overflow: visible; padding: 20px 16px; } }
@media (max-width: 480px) { .connection-grid { grid-template-columns: minmax(0,1fr); } .add-model-row { flex-wrap: wrap; } .add-model-row :deep(.arco-select) { flex-basis: 100%; } .model-row { flex-direction: column; } .model-actions { margin-left: 0; } }
</style>
