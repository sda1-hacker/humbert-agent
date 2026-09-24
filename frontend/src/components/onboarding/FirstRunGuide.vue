<script setup>
import { computed, onMounted, reactive, ref, watch } from "vue";
import { useModelStore } from "../../stores/models.js";
import { useAgentStore } from "../../stores/agents.js";
import { listBuiltinTools } from "../../api/agents.js";
import { defaultBuiltinTools } from "../../utils/defaultBuiltinTools.js";

const emit = defineEmits(["open-settings", "complete"]);
const models = useModelStore();
const agents = useAgentStore();
const step = ref(models.providers.length ? (models.enabledModels.length ? 2 : 1) : 0);
const busy = ref(false);
const error = ref("");
const diagnostic = ref(null);
const providerID = ref(models.providers[0]?.id || "");
const modelID = ref(models.enabledModels[0]?.id || "");
const agentName = ref("Humbert");
const builtinTools = ref([]);
const enableTools = ref(true);
const provider = reactive({ name: "", type: "openai", baseURL: "", apiKey: "" });
const model = reactive({ name: "", contextWindow: 131072, maxOutputTokens: 8192 });
const steps = ["连接供应商", "选择模型", "连接诊断", "创建助手"];
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

watch(() => models.providers, (items) => {
  if (!items.some((item) => item.id === providerID.value)) providerID.value = items[0]?.id || "";
});
watch(() => models.enabledModels, (items) => {
  if (!items.some((item) => item.id === modelID.value)) modelID.value = items[0]?.id || "";
});
watch(modelID, () => { diagnostic.value = null; });

function advance(next) { error.value = ""; step.value = next; }
function explain(errorValue) { error.value = errorValue?.message || String(errorValue); }

async function createProvider() {
  if (!provider.name.trim()) { error.value = "请输入供应商名称。"; return; }
  if (provider.type === "openai" && !provider.apiKey.trim()) { error.value = "OpenAI 供应商需要 API Key。"; return; }
  if (provider.type === "openai_compatible" && !provider.baseURL.trim()) { error.value = "请输入兼容接口的 Base URL。"; return; }
  busy.value = true; error.value = "";
  try {
    const created = await models.createProvider({
      name: provider.name.trim(), type: provider.type,
      baseURL: provider.baseURL.trim(), apiKey: provider.apiKey.trim(),
    });
    provider.apiKey = "";
    providerID.value = created.id;
    advance(1);
  } catch (cause) { explain(cause); }
  finally { busy.value = false; }
}

async function createModel() {
  if (!providerID.value) { error.value = "请先选择供应商。"; return; }
  if (!model.name.trim()) { error.value = "请输入供应商提供的模型标识。"; return; }
  const contextWindow = Number(model.contextWindow);
  const maxOutputTokens = Number(model.maxOutputTokens);
  if (!Number.isInteger(contextWindow) || contextWindow < 4096 || contextWindow > 2097152 ||
      !Number.isInteger(maxOutputTokens) || maxOutputTokens < 256 || maxOutputTokens >= contextWindow) {
    error.value = "Context Window 需为 4096–2097152，最大输出需为 256 以上且小于 Context Window。";
    return;
  }
  busy.value = true; error.value = "";
  try {
    const created = await models.createModel({
      providerID: providerID.value, modelName: model.name.trim(), displayName: model.name.trim(),
      timeoutMS: 60000, contextWindow, maxOutputTokens, enabled: true,
      capabilityConfig: { tools: "auto", vision: "auto", files: "auto", reasoning: "auto", json: "auto", audio: "auto" },
    });
    modelID.value = created.id;
    advance(2);
  } catch (cause) { explain(cause); }
  finally { busy.value = false; }
}

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
      modelRoles: { utilityModelID: "", memoryModelID: "" }, enabledSkills: [],
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
      <header>
        <span class="eyebrow">HUMBERT · 首次使用</span>
        <h1>配置你的 Agent 助手</h1>
        <p>连接模型、验证可用性，再创建一个带独立工作区的助手。</p>
      </header>

      <nav class="steps" aria-label="配置步骤">
        <span v-for="(label, index) in steps" :key="label" :class="{ active: step === index, done: step > index }">
          <b>{{ index + 1 }}</b>{{ label }}
        </span>
      </nav>

      <div v-if="step === 0" class="body">
        <h2>连接供应商</h2>
        <p>已有供应商可以直接使用，也可以创建新的连接。API Key 仅保存到本机凭据存储。</p>
        <label v-if="models.providers.length">已有供应商
          <select v-model="providerID"><option v-for="item in models.providers" :key="item.id" :value="item.id">{{ item.name }}</option></select>
        </label>
        <button v-if="providerID" class="secondary" :disabled="busy" @click="advance(1)">使用此供应商</button>
        <div v-if="models.providers.length" class="divider">或新建供应商</div>
        <label>名称<input v-model="provider.name" placeholder="例如：我的模型服务" autocomplete="off"></label>
        <label>类型<select v-model="provider.type"><option value="openai">OpenAI</option><option value="openai_compatible">OpenAI 兼容接口</option><option value="ollama">Ollama</option></select></label>
        <label>Base URL
          <input v-model="provider.baseURL" :placeholder="provider.type === 'openai' ? '默认 https://api.openai.com/v1' : provider.type === 'ollama' ? '默认 http://127.0.0.1:11434' : 'https://example.com/v1'" autocomplete="off">
        </label>
        <label v-if="provider.type !== 'ollama'">API Key
          <input v-model="provider.apiKey" type="password" autocomplete="new-password" placeholder="仅用于当前供应商连接">
        </label>
        <button class="primary" :disabled="busy" @click="createProvider">{{ busy ? '正在保存…' : '保存并继续' }}</button>
      </div>

      <div v-else-if="step === 1" class="body">
        <h2>选择对话模型</h2>
        <p>填写服务商实际使用的模型标识。其余参数可以先使用默认值，之后在设置中调整。</p>
        <label v-if="models.enabledModels.length">已有模型
          <select v-model="modelID"><option v-for="item in models.enabledModels" :key="item.id" :value="item.id">{{ item.displayName }} · {{ item.providerName }}</option></select>
        </label>
        <button v-if="modelID" class="secondary" :disabled="busy" @click="advance(2)">使用此模型</button>
        <div v-if="models.enabledModels.length" class="divider">或新建模型</div>
        <label>供应商<select v-model="providerID"><option v-for="item in models.providers" :key="item.id" :value="item.id">{{ item.name }}</option></select></label>
        <label>模型标识<input v-model="model.name" placeholder="填写 API 使用的 model name" autocomplete="off"></label>
        <details class="advanced"><summary>高级选项：上下文与输出预算</summary>
          <p>若服务商提供了具体数值，请在这里填写；过高的数值可能导致请求被模型拒绝。</p>
          <div class="pair"><label>Context Window<input v-model.number="model.contextWindow" type="number" min="4096"></label><label>最大输出 Token<input v-model.number="model.maxOutputTokens" type="number" min="256"></label></div>
        </details>
        <button class="primary" :disabled="busy" @click="createModel">{{ busy ? '正在保存…' : '创建并继续' }}</button>
      </div>

      <div v-else-if="step === 2" class="body">
        <h2>连接与能力诊断</h2>
        <p>对 {{ selectedModel?.displayName || '所选模型' }} 发送一条短消息，可能产生少量 Token 费用。</p>
        <button class="primary" :disabled="busy || !modelID" @click="diagnose">{{ busy ? '正在连接…' : diagnostic ? '重新诊断' : '开始诊断' }}</button>
        <div v-if="diagnostic" class="diagnostic" :class="{ failure: !diagnostic.success }" role="status">
          <strong>{{ diagnostic.summary }}</strong><span v-if="diagnostic.durationMS"> · {{ diagnostic.durationMS }} ms</span>
          <p>{{ diagnostic.action }}</p>
          <p v-if="diagnostic.success">工具调用：{{ diagnostic.toolsSupported ? '配置显示支持，尚未实测' : '当前配置未启用' }}</p>
        </div>
        <button v-if="diagnostic?.success" class="primary" @click="advance(3)">继续创建助手</button>
        <button class="secondary" @click="emit('open-settings', ['credential', 'network', 'endpoint', 'timeout'].includes(diagnostic?.category) ? 'providers' : 'models')">打开相关设置</button>
      </div>

      <div v-else class="body">
        <h2>{{ needsAgent ? '创建默认助手' : '准备就绪' }}</h2>
        <p>{{ needsAgent ? '助手会使用所选模型和独立管理的工作区。创建后可在 Agent 设置中启用工具、Skills 和安全权限。' : '已有助手，可以开始对话。' }}</p>
        <label v-if="needsAgent">助手名称<input v-model="agentName" maxlength="100" autocomplete="off"></label>
        <details v-if="needsAgent && diagnostic?.toolsSupported && builtinTools.length" class="advanced"><summary>内置工具：默认启用常用能力</summary>
          <label class="tool-choice"><input v-model="enableTools" type="checkbox">启用常用工具（文件、网页搜索、浏览器和提醒；浏览器需要安装 Chrome）</label>
        </details>
        <button class="primary" :disabled="busy" @click="finish">{{ busy ? '正在创建…' : needsAgent ? '创建并开始对话' : '开始对话' }}</button>
      </div>

      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <footer><button v-if="step > 0" class="back" :disabled="busy" @click="advance(step - 1)">上一步</button><span>配置可以随时在设置中修改</span></footer>
    </section>
  </main>
</template>

<style scoped>
.guide{height:100%;overflow:auto;display:grid;place-items:center;padding:32px;background:var(--h-bg)}
.guide-card{width:min(760px,100%);padding:36px;border:1px solid var(--h-border);border-radius:18px;background:var(--h-surface);box-shadow:0 14px 40px rgba(0,0,0,.06)}
.eyebrow{font-size:11px;letter-spacing:.15em;color:var(--h-text-muted);font-weight:700}
h1{font-size:28px;margin:10px 0 6px}h2{font-size:19px;margin:0 0 8px}p{color:var(--h-text-muted);line-height:1.55;margin:0 0 18px}
.steps{display:flex;gap:8px;margin:28px 0 30px}.steps span{display:flex;align-items:center;gap:6px;flex:1;color:var(--h-text-muted);font-size:12px}.steps b{width:25px;height:25px;border-radius:50%;display:grid;place-items:center;background:var(--h-bg);border:1px solid var(--h-border)}.steps .active{color:var(--h-text)}.steps .active b,.steps .done b{background:var(--h-accent);color:white;border-color:var(--h-accent)}
.body{display:grid;gap:12px}.body>p{margin-bottom:4px}label{display:grid;gap:6px;color:var(--h-text);font-size:13px;font-weight:600}input,select{width:100%;height:38px;border:1px solid var(--h-border);border-radius:8px;padding:0 10px;background:var(--h-bg);color:var(--h-text);font:inherit}input:focus,select:focus{outline:2px solid var(--h-accent);outline-offset:1px}.pair{display:grid;grid-template-columns:1fr 1fr;gap:12px}.divider{color:var(--h-text-muted);font-size:12px;text-align:center;margin:3px 0}button{justify-self:start;min-height:36px;border-radius:8px;padding:0 15px;cursor:pointer;font:inherit}.primary{background:var(--h-accent);border:1px solid var(--h-accent);color:white}.secondary,.back{background:transparent;border:1px solid var(--h-border);color:var(--h-text)}button:disabled{opacity:.6;cursor:wait}.diagnostic{border:1px solid var(--h-success);border-radius:10px;padding:14px;background:var(--h-bg)}.diagnostic.failure{border-color:var(--h-danger)}.diagnostic p{margin:6px 0 0}.error{color:var(--h-danger);margin:18px 0 0}footer{display:flex;align-items:center;justify-content:space-between;margin-top:28px;color:var(--h-text-muted);font-size:12px}@media(max-width:650px){.guide{padding:12px;place-items:start center}.guide-card{padding:22px}.steps{flex-wrap:wrap}.steps span{min-width:40%}.pair{grid-template-columns:1fr}}
.tool-choice{display:flex;align-items:center;gap:9px;font-weight:400}.tool-choice input{width:auto;height:auto}
.advanced{border:1px solid var(--h-border);border-radius:8px;padding:10px 12px}.advanced summary{cursor:pointer;font-size:13px;color:var(--h-text)}.advanced p{margin:10px 0;font-size:12px}
</style>
