<script setup>
import { computed, reactive, ref, watch } from "vue";
import { Browser } from "@wailsio/runtime";
import { useModelStore } from "../../../stores/models.js";
import { capabilityFields, formatTokens, modelDraft, presetForModel, reasoningReplayFor, reasoningReplayOptions } from "../../../utils/modelCatalog.js";
import { Message } from "../../../utils/uiMessage.js";
import { t } from "../../../i18n/index.js";
import { useAgentStore } from "../../../stores/agents.js";
import { useSettingsGuard } from "../../../composables/useSettingsGuard.js";

const props = defineProps({ visible: Boolean, providerId: { type: String, default: "" }, model: { type: Object, default: null }, preset: { type: Object, default: null } });
const emit = defineEmits(["update:visible", "saved", "open-settings", "open-agent"]);
const store = useModelStore();
const agents = useAgentStore();
const form = reactive(modelDraft(""));
const saving = ref(false);
const advanced = ref(false);
const provider = computed(() => store.providers.find((item) => item.id === form.providerID));
const reference = computed(() => presetForModel(provider.value, form.modelName));
const replayLabel = computed(() => reasoningReplayOptions.find(option => option.value === reasoningReplayFor(provider.value?.type, form.modelName, form.reasoningReplay))?.label || "不回传推理");
const baseline = ref("");
const mayLeave = useSettingsGuard(() => props.visible && JSON.stringify(form) !== baseline.value, save, () => saving.value);
const usages = computed(() => {
  if (!form.id) return [];
  const result = [];
  for (const agent of agents.items) {
    const roles = [];
    if (agent.modelID === form.id) roles.push(t("对话模型"));
    if (agent.modelRoles?.utilityModelID === form.id) roles.push(t("辅助模型"));
    if (roles.length) result.push({ key: agent.id, label: `${agent.name} · ${roles.join(" / ")}`, event: "open-agent", target: agent.id });
  }
  if (store.multimedia.imageModelID === form.id) result.push({ key: "vision", label: t("应用视觉辅助模型"), event: "open-settings", target: "multimedia" });
  return result;
});
async function openUsage(usage) {
  if (!(await mayLeave())) return;
  emit("update:visible", false);
  emit(usage.event, usage.target);
}
// Arco 的 beforeCancel 只支持同步布尔值；异步确认结束后由受控 visible 手动关闭。
function beforeCancel() {
  if (!saving.value) void mayLeave().then(allowed => { if (allowed) emit("update:visible", false); });
  return false;
}

watch(() => props.visible, (visible) => {
  if (!visible) return;
  Object.assign(form, modelDraft(props.providerId, props.model, props.preset));
  baseline.value = JSON.stringify(form);
  advanced.value = false;
}, { immediate: true });

async function save() {
  if (saving.value) return false;
  if (!provider.value || !form.modelName.trim()) { Message.warning(t("请选择供应商并填写模型标识")); return false; }
  if (!Number.isInteger(form.contextWindow) || form.contextWindow < 4096 || form.contextWindow > 2097152) {
    Message.warning(t("Context Window 必须是 4096 到 2097152 之间的整数")); return false;
  }
  if (!Number.isInteger(form.maxOutputTokens) || form.maxOutputTokens < 256 || form.maxOutputTokens >= form.contextWindow) {
    Message.warning(t("最大输出 Token 必须至少为 256，且小于 Context Window")); return false;
  }
  if (!Number.isInteger(form.timeoutMS) || form.timeoutMS < 1000 || form.timeoutMS > 300000) {
    Message.warning(t("请求超时必须在 1,000–300,000 毫秒之间")); return false;
  }
  saving.value = true;
  try {
    // 仍走唯一的 ModelStore 和原有后端校验，不另建一份模型配置。
    const { id, ...request } = { ...form, modelName: form.modelName.trim(), displayName: form.displayName.trim(), capabilityConfig: { ...form.capabilityConfig } };
    const result = id ? await store.updateModel(id, request) : await store.createModel(request);
    Message.success(t(id ? "模型已更新" : "模型已创建"));
    emit("saved", result);
    return true;
  } catch (error) { Message.error(error?.message || String(error)); return false; }
  finally { saving.value = false; }
}

async function openReference() {
  if (!reference.value?.source) return;
  try { await Browser.OpenURL(reference.value.source); }
  catch (error) { Message.error(error?.message || String(error)); }
}
</script>

<template>
  <a-modal :visible="visible" :title="t(form.id ? '编辑模型' : '添加模型')" :width="660" :modal-style="{maxWidth:'calc(100vw - 32px)',maxHeight:'90vh',display:'inline-flex',flexDirection:'column'}" :body-style="{minHeight:0,overflowY:'auto'}" :mask-closable="false" :esc-to-close="!saving" :ok-loading="saving" :on-before-ok="save" :on-before-cancel="beforeCancel" :ok-text="t('保存模型')" :cancel-text="t('取消')" @update:visible="emit('update:visible', $event)">
    <a-form :model="form" layout="vertical" :disabled="saving" class="model-editor">
      <p class="model-provider">{{ provider?.name }}</p>
      <div class="editor-grid">
        <a-form-item label="模型标识"><a-input v-model="form.modelName" :max-length="256" placeholder="填写 API 使用的 model name" /></a-form-item>
        <a-form-item label="显示名称"><a-input v-model="form.displayName" :max-length="128" placeholder="留空时使用模型标识" /></a-form-item>
        <a-form-item label="上下文窗口"><a-input-number v-model="form.contextWindow" :min="4096" :max="2097152" :step="1024" /></a-form-item>
        <a-form-item label="输出预算"><a-input-number v-model="form.maxOutputTokens" :min="256" :max="Math.max(256, Number(form.contextWindow) - 1)" :step="1024" /></a-form-item>
      </div>
      <div class="model-budget-note">
        <span v-if="reference">{{ t('参考窗口 {context}，服务最大输出 {output}。', {context: formatTokens(reference.contextWindow), output: reference.maxOutputTokens ? formatTokens(reference.maxOutputTokens) : t('以部署为准')}) }}</span>
        <span>输出预算是单次回复的上限，也会计入上下文预留。</span>
        <span v-if="provider?.type === 'ollama'">本地模型需先安装；请按 Ollama 实际启动窗口调整参数。</span>
        <a-button v-if="reference" type="text" size="mini" @click="openReference">官方参数</a-button>
      </div>
      <div class="model-enabled"><div><strong>启用模型</strong><span>停用后不会出现在 Agent 默认模型选择中。</span></div><a-switch v-model="form.enabled" aria-label="启用模型" /></div>
      <section v-if="form.id" class="model-usages"><strong>使用位置</strong><a-button v-for="usage in usages" :key="usage.key" type="text" size="small" @click="openUsage(usage)">{{ usage.label }}</a-button><p v-if="!usages.length" class="field-help">当前没有 Agent 或视觉辅助配置引用此模型。</p></section>
      <details class="model-advanced" :open="advanced" @toggle="advanced = $event.target.open">
        <summary>能力与高级参数</summary>
        <div class="advanced-body">
          <p class="field-help">预设能力已自动填入。自定义模型可使用“自动”，也可按服务实际能力覆盖。</p>
          <div class="capability-grid"><a-form-item v-for="field in capabilityFields" :key="field.key" :label="t(field.label)"><a-select v-model="form.capabilityConfig[field.key]" :aria-label="t(field.label)"><a-option value="auto">自动</a-option><a-option value="enabled">支持</a-option><a-option value="disabled">不支持</a-option></a-select></a-form-item></div>
          <p class="field-help">原生文件与音频仅为能力标记，不代表应用已支持对应输入入口。</p>
          <a-form-item label="推理回放">
            <a-select v-model="form.reasoningReplay" aria-label="推理回放"><a-option v-for="option in reasoningReplayOptions" :key="option.value" :value="option.value">{{ t(option.label) }}</a-option></a-select>
            <template #extra>{{ t('当前生效：{policy}', { policy: t(replayLabel) }) }}</template>
          </a-form-item>
          <p class="field-help">控制原生 thinking 是否回传；不控制思考开关，也不改变消息保存。回放范围仍受上下文预算和压缩限制。</p>
          <div class="editor-grid">
            <a-form-item label="请求超时"><a-input-number v-model="form.timeoutMS" :min="1000" :max="300000" :step="1000"><template #suffix>ms</template></a-input-number></a-form-item>
            <a-form-item v-if="form.id" label="所属供应商"><a-select v-model="form.providerID" aria-label="所属供应商"><a-option v-for="item in store.providers" :key="item.id" :value="item.id">{{ item.name }}</a-option></a-select></a-form-item>
          </div>
        </div>
      </details>
    </a-form>
  </a-modal>
</template>

<style scoped>
.model-editor { display: grid; gap: 18px; }
.model-provider { margin: 0; color: var(--h-text-muted); font-size: 12px; }
.editor-grid { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 16px; }
.model-editor :deep(.arco-form-item) { margin-bottom: 0; }
.model-editor :deep(.arco-input-number) { width: 100%; }
.model-budget-note { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 8px; color: var(--h-text-muted); font-size: 12px; line-height: 1.65; }
.model-budget-note > span { flex-basis: 100%; }
.model-budget-note :deep(.arco-btn) { padding: 0; }
.model-enabled { display: flex; justify-content: space-between; align-items: center; gap: 16px; }
.model-enabled > div { display: grid; gap: 4px; }
.model-enabled strong { font-size: 13px; font-weight: 500; }
.model-enabled span, .field-help { color: var(--h-text-muted); font-size: 12px; line-height: 1.65; }
.model-advanced { padding-top: 14px; border-top: 1px solid var(--h-border); }
.model-advanced summary { cursor: pointer; color: var(--h-text-secondary); font-size: 13px; }
.advanced-body { display: grid; gap: 14px; margin-top: 14px; }
.field-help { margin: 0; }
.model-usages { display: grid; justify-items: start; gap: 6px; padding-top: 14px; border-top: 1px solid var(--h-border); }
.model-usages strong { font-size: 13px; font-weight: 500; }
.model-usages :deep(.arco-btn) { max-width: 100%; height: auto; white-space: normal; text-align: left; }
.capability-grid { display: grid; grid-template-columns: repeat(3,minmax(0,1fr)); gap: 12px; }
@media (max-width: 480px) { .editor-grid, .capability-grid { grid-template-columns: minmax(0,1fr); } }
</style>
