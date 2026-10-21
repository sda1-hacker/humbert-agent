<script setup>
import { computed, onMounted, ref, watch } from "vue";
import { IconCheck, IconDown, IconLeft } from "@arco-design/web-vue/es/icon";
import { useModelStore } from "../../stores/models.js";
import { useAgentStore } from "../../stores/agents.js";
import { listBuiltinTools } from "../../api/agents.js";
import { defaultBuiltinTools } from "../../utils/defaultBuiltinTools.js";

import ModelCatalog from "../settings/models/ModelCatalog.vue";

const emit = defineEmits(["open-settings", "complete"]);
const models = useModelStore();
const agents = useAgentStore();
const step = ref(models.enabledModels.length ? 1 : 0);
const busy = ref(false);
const error = ref("");
const diagnostic = ref(null);
const modelID = ref(models.enabledModels[0]?.id || "");
const agentName = ref("Humbert");
const builtinTools = ref([]);
const enableTools = ref(true);
const steps = ["连接模型", "验证连接", "创建助手"];
const selectedModel = computed(() => models.modelByID(modelID.value));
const needsAgent = computed(() => !agents.items.some((agent) => agent.modelID === modelID.value));

onMounted(async () => {
  try {
    const items = await listBuiltinTools();
    builtinTools.value = Array.isArray(items) ? items : [];
  } catch {
    builtinTools.value = [];
  }
});

watch(() => models.revision, () => { diagnostic.value = null; });
function advance(next) { error.value = ""; step.value = next; }
function explain(errorValue) { error.value = errorValue?.message || String(errorValue); }
// 首次使用与设置页共用同一份连接、预设和模型表单，不再维护独立默认值。
function chooseModel(model) { modelID.value = model.id; diagnostic.value = null; advance(1); }

async function diagnose() {
  if (!modelID.value) { error.value = "请先选择模型。"; return; }
  busy.value = true; error.value = ""; diagnostic.value = null;
  try { diagnostic.value = await models.diagnoseModel(modelID.value); }
  catch (cause) { explain(cause); }
  finally { busy.value = false; }
}

async function finish() {
  if (!needsAgent.value) { emit("complete"); return; }
  if (!agentName.value.trim()) { error.value = "请输入助手名称。"; return; }
  busy.value = true; error.value = "";
  try {
    await agents.create({
      name: agentName.value.trim(), modelID: modelID.value,
      workspaceMode: "managed", workspacePath: "", instruction: "",
      avatar: "", subagentEnabled: false,
      modelRoles: { utilityModelID: "" }, enabledSkills: [],
      builtinToolsConfigured: true,
      enabledBuiltinTools: enableTools.value && diagnostic.value?.toolsSupported ? defaultBuiltinTools(builtinTools.value) : [],
      sandbox: { profile: "", additionalWritePaths: [], networkMode: "", nativeMode: "" },
    });
    emit("complete");
  } catch (cause) { explain(cause); }
  finally { busy.value = false; }
}
</script>

<template>
  <main class="guide">
    <section class="guide-card">
      <header class="guide-header">
        <span class="eyebrow">HUMBERT · 首次使用</span>
        <h1>配置你的 Agent 助手</h1>
        <p>连接模型、验证可用性，再创建一个带独立工作区的助手。</p>
      </header>

      <nav class="steps" aria-label="配置步骤">
        <ol>
          <li v-for="(label, index) in steps" :key="label"
              :class="{ active: step === index, done: step > index }"
              :aria-current="step === index ? 'step' : undefined">
            <span class="step-number">
              <IconCheck v-if="step > index" aria-hidden="true" />
              <template v-else>{{ index + 1 }}</template>
            </span>
            <span class="step-label">{{ $t(label) }}</span>
          </li>
        </ol>
      </nav>

      <div v-if="step === 0" class="body guide-models">
        <ModelCatalog selectable @selected="chooseModel" @open-settings="emit('open-settings', $event)" />
      </div>

      <div v-else-if="step === 1" class="body" :aria-busy="busy">
        <h2>连接与能力诊断</h2>
        <p class="step-description">对 {{ selectedModel?.displayName || $t('所选模型') }} 发送一条短消息，可能产生少量 Token 费用。</p>
        <a-button class="primary-action" type="primary" :loading="busy" :disabled="busy || !modelID" @click="diagnose">{{ $t(busy ? '正在连接…' : diagnostic ? '重新诊断' : '开始诊断') }}</a-button>
        <div v-if="diagnostic" class="diagnostic" :class="{ failure: !diagnostic.success }" role="status">
          <strong>{{ $t(diagnostic.summary) }}</strong><span v-if="diagnostic.durationMS"> · {{ diagnostic.durationMS }} ms</span>
          <p>{{ $t(diagnostic.action) }}</p>
          <p v-if="diagnostic.success">工具调用：{{ $t(diagnostic.toolsSupported ? '配置显示支持，尚未实测' : '当前配置未启用') }}</p>
        </div>
        <a-button v-if="diagnostic?.success" type="primary" :disabled="busy" @click="advance(2)">继续创建助手</a-button>
        <a-button type="outline" :disabled="busy" @click="emit('open-settings', 'models')">打开相关设置</a-button>
      </div>

      <div v-else class="body" :aria-busy="busy">
        <h2>{{ $t(needsAgent ? '创建默认助手' : '准备就绪') }}</h2>
        <p class="step-description">{{ $t(needsAgent ? '助手会使用所选模型和独立管理的工作区。创建后可在 Agent 设置中启用工具、Skills 和安全权限。' : '已有助手，可以开始对话。') }}</p>
        <label v-if="needsAgent" class="field">助手名称
          <a-input v-model="agentName" :disabled="busy" :max-length="100" autocomplete="off" />
        </label>
        <details v-if="needsAgent && diagnostic?.toolsSupported && builtinTools.length" class="advanced">
          <summary><IconDown aria-hidden="true" />内置工具：默认启用常用能力</summary>
          <a-checkbox v-model="enableTools" class="tool-choice" :disabled="busy">启用常用工具（文件、网页搜索、浏览器和提醒；浏览器需要安装 Chrome）</a-checkbox>
        </details>
        <a-button class="primary-action" type="primary" :loading="busy" :disabled="busy" @click="finish">{{ $t(busy ? '正在创建…' : needsAgent ? '创建并开始对话' : '开始对话') }}</a-button>
      </div>

      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <footer>
        <a-button v-if="step > 0" type="text" :disabled="busy" @click="advance(step - 1)">
          <template #icon><IconLeft /></template>上一步
        </a-button>
        <span>配置可以随时在设置中修改</span>
      </footer>
    </section>
  </main>
</template>

<style scoped>
.guide {
  height: 100%; overflow: auto; display: flex; padding: 32px 24px; background: var(--h-bg);
}
.guide-card {
  width: min(960px, 100%); min-width: 0; margin: auto; padding: 32px;
  border: 1px solid var(--h-border); border-radius: 12px; background: var(--h-surface);
}
.eyebrow { font-size: 12px; letter-spacing: .12em; color: var(--h-text-muted); font-weight: 500; }
h1 { font-size: 26px; font-weight: 500; line-height: 1.35; margin: 12px 0 10px; }
h2 { font-size: 18px; font-weight: 500; line-height: 1.4; margin: 0; }
p { color: var(--h-text-muted); line-height: 1.65; margin: 0; }
.guide-header p { font-size: 13px; }
.steps { margin: 28px 0; padding-bottom: 24px; border-bottom: 1px solid var(--h-border); }
.steps ol { display: flex; align-items: center; gap: 12px; list-style: none; padding: 0; margin: 0; }
.steps li { display: flex; align-items: center; gap: 8px; min-width: 0; color: var(--h-text-muted); font-size: 12px; }
.steps li:not(:last-child) { flex: 1; }
.steps li:not(:last-child)::after { content: ""; flex: 1; min-width: 8px; height: 1px; margin-left: 4px; background: var(--h-border); }
.step-number {
  width: 26px; height: 26px; flex: 0 0 26px; display: grid; place-items: center;
  border: 1px solid var(--h-border); border-radius: 50%; background: var(--h-bg); font-weight: 500;
}
.step-label { line-height: 1.4; }
.steps .active { color: var(--h-accent); font-weight: 500; }
.steps .active .step-number { border-color: var(--h-accent); background: var(--h-accent); color: var(--h-surface); }
.steps .done .step-number { border-color: var(--h-accent-border); background: var(--h-accent-soft); color: var(--h-accent); }
.steps .done::after { background: var(--h-accent-border) !important; }
.body { display: grid; gap: 16px; }
.guide-models { min-height: 420px; height: min(560px, 65vh); }
@media (max-width: 900px) { .guide-models { height: auto; } }
.step-description { font-size: 13px; margin: -8px 0 2px; }
.field { display: grid; gap: 7px; min-width: 0; color: var(--h-text-secondary); font-size: 13px; font-weight: 500; }
.field :deep(.arco-input-wrapper),
.field :deep(.arco-select-view-single),
.field :deep(.arco-input-number) { width: 100%; min-height: 38px; font-weight: 400; }
.field :deep(.arco-select-view-single) { height: 38px; }
.field :deep(.arco-input-number .arco-input-wrapper) { min-height: 36px; }
.body > .arco-btn { justify-self: start; min-height: 36px; }
.primary-action { margin-top: 4px; }
.diagnostic { border: 1px solid var(--h-success); border-radius: var(--h-radius-md); padding: 14px 16px; background: var(--h-success-soft); }
.diagnostic.failure { border-color: var(--h-danger-border); background: var(--h-danger-soft); }
.diagnostic p { font-size: 13px; margin-top: 6px; }
.error { color: var(--h-danger); font-size: 13px; margin-top: 18px; }
.advanced { border: 1px solid var(--h-border); border-radius: var(--h-radius-md); padding: 12px 14px; }
.advanced summary { display: flex; align-items: center; gap: 8px; cursor: pointer; list-style: none; font-size: 13px; color: var(--h-text-secondary); }
.advanced summary::-webkit-details-marker { display: none; }
.advanced summary :deep(.arco-icon) { flex-shrink: 0; transform: rotate(-90deg); transition: transform 120ms ease; color: var(--h-text-muted); }
.advanced[open] summary :deep(.arco-icon) { transform: rotate(0); }
.advanced summary:focus-visible { outline: 2px solid var(--h-accent-border); outline-offset: 4px; border-radius: 2px; }
.advanced p { margin: 12px 0; font-size: 12px; }
.tool-choice { margin-top: 14px; align-items: flex-start; }
.tool-choice :deep(.arco-checkbox-label) { white-space: normal; line-height: 1.6; font-size: 13px; }
footer {
  display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-top: 28px; padding-top: 20px;
  border-top: 1px solid var(--h-border); color: var(--h-text-muted); font-size: 12px;
}
footer > span { margin-left: auto; text-align: right; }
@media (max-width: 600px) {
  .guide { padding: 16px 12px; }
  .guide-card { padding: 24px 20px; }
  h1 { font-size: 23px; }
  .steps ol { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 14px; }
  .steps li::after { display: none; }
  footer { gap: 8px; }
}
</style>
