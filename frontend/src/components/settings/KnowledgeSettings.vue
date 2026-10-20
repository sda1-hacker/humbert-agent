<script setup>
import { onMounted, ref } from "vue";
import SectionCard from "../ui/SectionCard.vue";
import { knowledgeState, saveKnowledgeSettings, initializeKnowledge } from "../../api/knowledge.js";
import { Message } from "../../utils/uiMessage.js";

const loading = ref(true);
const saving = ref(false);
const initializing = ref(false);
const enabled = ref(false);
const revision = ref(0);
const values = ref(null);
const secrets = ref({});
const hasSecrets = ref({});
const error = ref("");
const advanced = ref("");
const showAdvanced = ref(false);
const logQueries = ref(false);

// 设置页直接编辑后端统一配置投影；不存在第二套默认参数。
function apply(state) {
  const s = state.settings;
  enabled.value = s.enabled;
  revision.value = s.revision;
  values.value = s.values;
  hasSecrets.value = s.has_secrets || {};
  logQueries.value = s.log_queries || false;
  secrets.value = {};
  values.value.rerank_provider ||= { endpoint: "", model: "" };
}
async function load() {
  loading.value = true;
  try { apply(await knowledgeState()); error.value = ""; }
  catch (e) { error.value = e?.message || String(e); }
  finally { loading.value = false; }
}
async function save() {
  if (saving.value) return false;
  saving.value = true; error.value = "";
  try {
    const config = JSON.parse(JSON.stringify(values.value));
    if (config.rerank.disabled && !config.rerank_provider?.endpoint) delete config.rerank_provider;
    apply(await saveKnowledgeSettings({ enabled: enabled.value, revision: revision.value, values: config, secrets: secrets.value, log_queries: logQueries.value }));
    Message.success("知识库配置已保存");
    return true;
  } catch (e) { error.value = e?.message || String(e); return false; }
  finally { saving.value = false; }
}
async function initialize() {
  if (!(await save())) return;
  initializing.value = true;
  try { await initializeKnowledge(); Message.success("数据库、索引和维度检查通过"); }
  catch (e) { error.value = e?.message || String(e); }
  finally { initializing.value = false; }
}
function editAdvanced() {
  advanced.value = JSON.stringify(values.value, null, 2); showAdvanced.value = true;
}
function applyAdvanced() {
  try {
    const next = JSON.parse(advanced.value);
    if (!next || Array.isArray(next) || typeof next !== "object") throw new Error("请输入配置对象");
    for (const key of ['database','postgres','embedding','chunking','loader','ingest','search','hybrid','vector','keyword','rerank']) {
      if (!next[key] || typeof next[key] !== 'object' || Array.isArray(next[key])) throw new Error(`缺少配置对象 ${key}`);
    }
    if (!next.chunking.splitter || !next.loader.parse_options || !next.loader.image_recognition) throw new Error('缺少切分或解析配置');
    next.rerank_provider ||= {endpoint:'',model:''};
    values.value = next; showAdvanced.value = false;
    return true;
  } catch (e) { Message.error(e?.message || String(e)); return false; }
}
onMounted(load);
</script>

<template>
  <div class="knowledge-settings">
    <a-alert v-if="error" type="error">{{ error }} <a-button size="mini" @click="load">重新加载</a-button></a-alert>
    <a-spin :loading="loading" style="width: 100%">
      <div v-if="values" class="settings-sections">
        <SectionCard title="知识库能力" description="上传资料并关联 Agent，让回答带有可追溯的来源。">
          <template #actions><a-switch v-model="enabled" aria-label="启用知识库能力" :disabled="saving || initializing" /></template>
          <p class="hint intro-note">在左侧“知识库”管理文档。配置更新从下一轮对话生效，撤销关联会立即阻止查询。</p>
        </SectionCard>
        <SectionCard title="PostgreSQL" description="需要 PostgreSQL 14+、pgvector 和 pg_search。初始化会创建缺失的数据库、表及索引。">
          <a-form :model="values" layout="vertical" :disabled="saving || initializing">
            <div class="form-grid">
              <a-form-item label="主机"><a-input v-model="values.database.host" placeholder="localhost" /></a-form-item>
              <a-form-item label="端口"><a-input-number v-model="values.database.port" :min="1" :max="65535" /></a-form-item>
              <a-form-item label="数据库"><a-input v-model="values.database.name" placeholder="humbert_rag" /></a-form-item>
              <a-form-item label="用户名"><a-input v-model="values.database.user" /></a-form-item>
              <a-form-item label="密码">
                <a-input-password v-model="secrets['database.password']" :placeholder="hasSecrets['database.password'] ? '已保存，留空保持不变' : '输入数据库密码'" autocomplete="new-password" />
                <template #extra><a-button v-if="hasSecrets['database.password']" type="text" size="mini" @click="secrets['database.password'] = ''">清除已存密码</a-button><span v-if="secrets['database.password'] === ''">保存后清除</span></template>
              </a-form-item>
              <a-form-item label="SSL 模式"><a-select v-model="values.database.sslmode" :options="['disable', 'prefer', 'require', 'verify-ca', 'verify-full']" /></a-form-item>
            </div>
          </a-form>
        </SectionCard>
        <SectionCard title="Embedding" description="入库与查询使用同一个模型、维度和输入规则。更换模型后请新建知识库并重新导入；改变维度请使用新的数据库。">
          <a-form :model="values" layout="vertical" :disabled="saving || initializing">
            <div class="form-grid">
              <a-form-item label="协议"><a-select v-model="values.embedding.provider" :options="[{value:'ollama',label:'Ollama 本地模型'},{value:'openai',label:'OpenAI 兼容接口'}]" @change="values.embedding.base_url = ''" /></a-form-item>
              <a-form-item label="Base URL"><a-input v-model="values.embedding.base_url" :placeholder="values.embedding.provider === 'ollama' ? 'http://localhost:11434' : 'https://api.example.com/v1'" /></a-form-item>
              <a-form-item label="模型名称"><a-input v-model="values.embedding.model" placeholder="例如 embeddinggemma-2:740m" /></a-form-item>
              <a-form-item label="向量维度"><a-input-number v-model="values.embedding.dimensions" :min="1" :max="4000" /><template #extra>建表、文档向量与查询向量都使用此值；模型必须支持所选维度。</template></a-form-item>
              <a-form-item label="向量请求超时"><a-input v-model="values.embedding.timeout" placeholder="例如 2m；0s 使用协议默认值" /></a-form-item>
              <a-form-item v-if="values.embedding.provider === 'openai'" label="API Key"><a-input-password v-model="secrets['embedding.api_key']" :placeholder="hasSecrets['embedding.api_key'] ? '已保存，留空保持不变' : '可选，按服务要求填写'" autocomplete="new-password" /></a-form-item>
            </div>
          </a-form>
        </SectionCard>
        <SectionCard title="文档解析与分块" description="原格式文件与解析图片保存在本机应用数据目录。分块参数更改后，需要重新导入文档。">
          <a-form :model="values" layout="vertical" :disabled="saving || initializing">
            <div class="setting-toggle"><div><strong>父子分块</strong><span>子块用于召回，父块补充连续上下文。</span></div><a-switch v-model="values.chunking.parent_child" aria-label="父子分块" /></div>
            <div class="form-grid">
              <a-form-item label="父块字符数"><a-input-number v-model="values.chunking.parent_chunk_size" :min="512" :max="32000" /></a-form-item>
              <a-form-item label="子块字符数"><a-input-number v-model="values.chunking.child_chunk_size" :min="64" :max="4096" /></a-form-item>
              <a-form-item label="切分策略"><a-select v-model="values.chunking.splitter.strategy" :options="[{value:'auto',label:'自动识别'},{value:'heading',label:'标题优先'},{value:'heuristic',label:'结构启发式'},{value:'recursive',label:'递归切分'}]" /></a-form-item>
              <a-form-item label="最大并行导入"><a-input-number v-model="values.ingest.max_concurrent" :min="1" :max="2" /><template #extra>桌面任务队列最多同时处理 2 篇。</template></a-form-item>
            </div>
            <div class="setting-toggle"><div><strong>图片 OCR 与描述</strong><span>调用视觉模型提取图片文字与描述；关闭时仍保存图片。使用在线模型可能产生费用。</span></div><a-switch v-model="values.loader.recognize_images" aria-label="图片 OCR 与描述" /></div>
            <a-form-item v-if="values.loader.recognize_images" label="图片识别来源"><a-select v-model="values.loader.image_recognition.provider" :options="[{value:'agent',label:'应用视觉辅助模型'},{value:'ollama',label:'独立 Ollama 视觉模型'},{value:'openai',label:'独立 OpenAI 兼容视觉模型'}]" /></a-form-item>
            <div v-if="values.loader.recognize_images && values.loader.image_recognition.provider !== 'agent'" class="form-grid">
              <a-form-item label="视觉 Base URL"><a-input v-model="values.loader.image_recognition.base_url" :placeholder="values.loader.image_recognition.provider === 'ollama' ? 'http://localhost:11434' : 'https://api.example.com/v1'" /></a-form-item>
              <a-form-item label="视觉模型"><a-input v-model="values.loader.image_recognition.model" placeholder="填写视觉模型名称" /></a-form-item>
              <a-form-item v-if="values.loader.image_recognition.provider === 'openai'" label="视觉 API Key"><a-input-password v-model="secrets['loader.image_recognition.api_key']" :placeholder="hasSecrets['loader.image_recognition.api_key'] ? '已保存，留空保持不变' : '输入视觉服务密钥'" autocomplete="new-password" /></a-form-item>
            </div>
          </a-form>
        </SectionCard>
        <SectionCard title="检索与精排" description="默认向量 + BM25 双路召回，再用 RRF 融合。精排需要单独配置模型，由你决定是否开启。">
          <a-form :model="values" layout="vertical" :disabled="saving || initializing">
            <div class="form-grid">
              <a-form-item label="最终返回条数"><a-input-number v-model="values.search.final_top_k" :min="1" :max="5" /><template #extra>提供给 Agent 的来源最多 5 条；可再次搜索或展开原文。</template></a-form-item>
              <a-form-item label="单路候选数"><a-input-number v-model="values.hybrid.channel_top_k" :min="5" :max="200" /></a-form-item>
              <a-form-item label="向量相似度下限"><a-input-number v-model="values.vector.score_threshold" :min="0" :max="1" :step="0.05" /></a-form-item>
              <a-form-item label="BM25 分数下限"><a-input-number v-model="values.keyword.score_threshold" :min="0" :step="0.1" /></a-form-item>
              <a-form-item label="检索总超时"><a-input v-model="values.search.timeout" @change="values.hybrid.timeout = $event" placeholder="例如 2m" /><template #extra>包括模型加载、召回与精排；本地模型首次加载较慢时可调大。</template></a-form-item>
            </div>
            <div class="setting-toggle"><div><strong>展开父块</strong><span>命中子块后补充父块原文，提供更完整的上下文。</span></div><a-switch v-model="values.search.expand_parents" aria-label="展开父块" /></div>
            <div class="setting-toggle"><div><strong>Rerank 精排</strong><span>使用独立模型对召回结果重新排序。</span></div><a-switch :model-value="!values.rerank.disabled" aria-label="Rerank 精排" @change="values.rerank.disabled = !$event" /></div>
            <div v-if="!values.rerank.disabled" class="form-grid">
              <a-form-item label="精排完整 URL"><a-input v-model="values.rerank_provider.endpoint" placeholder="https://example.com/v1/rerank" /></a-form-item>
              <a-form-item label="精排模型"><a-input v-model="values.rerank_provider.model" /></a-form-item>
              <a-form-item label="精排 API Key"><a-input-password v-model="secrets['rerank_provider.api_key']" :placeholder="hasSecrets['rerank_provider.api_key'] ? '已保存，留空保持不变' : '可选'" autocomplete="new-password" /></a-form-item>
              <a-form-item label="额外请求头（JSON，可选）"><a-textarea v-model="secrets['rerank_provider.headers']" :placeholder="hasSecrets['rerank_provider.headers'] ? '已保存，不修改则保持原值' : '例如 {&quot;X-API-Key&quot;:&quot;...&quot;}'" :auto-size="{minRows:2,maxRows:4}" /><template #extra>内容单独存入凭据库，不会写入高级参数或回显。</template></a-form-item>
            </div>
          </a-form>
        </SectionCard>
        <SectionCard title="调试与高级设置" description="默认日志仅记录处理结果、耗时和命中数。详细分数与诊断在 Debug 级别记录。">
          <div class="setting-toggle"><div><strong>记录查询原文</strong><span>用于调试评估，默认关闭。开启后查询会写入本机日志，密钥样式仍会脱敏。</span></div><a-switch v-model="logQueries" aria-label="记录查询原文" :disabled="saving || initializing" /></div>
          <div class="advanced-action"><a-button :disabled="saving || initializing" @click="editAdvanced">编辑全部高级参数</a-button><span class="hint">没有标注数据时不计算准确率或召回率。</span></div>
        </SectionCard>
        <div class="settings-actions">
          <a-button type="primary" :loading="saving" :disabled="initializing" @click="save">保存配置</a-button>
          <a-button :loading="initializing" :disabled="!enabled || saving" @click="initialize">保存并初始化数据库</a-button>
        </div>
      </div>
    </a-spin>
    <a-modal v-model:visible="showAdvanced" title="RAG 高级参数" :width="760" :modal-style="{maxWidth:'calc(100vw - 32px)',maxHeight:'90vh',display:'inline-flex',flexDirection:'column'}" :body-style="{minHeight:0,overflowY:'auto'}" :on-before-ok="applyAdvanced" :mask-closable="false">
      <p class="hint">沿用后端统一配置。超时带单位（例如 30s）；密码只在独立密码框中设置；本机存储目录由应用管理。</p>
      <a-textarea v-model="advanced" :auto-size="{minRows:16,maxRows:24}" class="config-editor" />
    </a-modal>
  </div>
</template>

<style scoped>
.knowledge-settings { display: grid; gap: 20px; }
/* 卡片在 Spin 内部排列，间距应放在实际容器上，避免 template 导致卡片黏连。 */
.settings-sections { display: grid; gap: 16px; }
.form-grid { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 16px 20px; }
.knowledge-settings :deep(.arco-form) { display: grid; gap: 16px; }
.knowledge-settings :deep(.arco-form-item) { margin-bottom: 0; }
.knowledge-settings :deep(.arco-form-item-label-col) { margin-bottom: 6px; }
.knowledge-settings :deep(.arco-form-item-extra) { margin-top: 6px; color: var(--h-text-muted); font-size: 12px; line-height: 1.6; }
.form-grid :deep(.arco-input-number) { width: 100%; }
.hint { color: var(--h-text-muted); font-size: 12px; line-height: 1.7; }
.intro-note { margin: 0; }
.setting-toggle { display: flex; align-items: center; justify-content: space-between; gap: 24px; padding: 0 0 14px; border-bottom: 1px solid var(--h-border); }
.setting-toggle > div { display: grid; gap: 4px; min-width: 0; }
.setting-toggle strong { font-size: 13px; font-weight: 500; }
.setting-toggle span { color: var(--h-text-muted); font-size: 12px; line-height: 1.65; }
.setting-toggle :deep(.arco-switch) { flex-shrink: 0; }
.advanced-action { display: flex; flex-wrap: wrap; align-items: center; gap: 10px 16px; margin-top: 16px; }
.settings-actions { display: flex; justify-content: flex-end; flex-wrap: wrap; gap: 10px; padding: 4px 0 24px; }
.config-editor { font-family: var(--h-mono); }
@media (max-width: 700px) { .form-grid { grid-template-columns: 1fr; } .settings-actions { flex-wrap: wrap; } }
</style>
