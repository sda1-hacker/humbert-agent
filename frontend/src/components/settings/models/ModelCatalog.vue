<script setup>
import {
  computed,
  reactive,
  ref,
  watch,
} from "vue";

import { Message } from "../../../utils/uiMessage.js";
import { t } from "../../../i18n/index.js";

import {
  confirmAction,
} from "../../../utils/confirm.js";

import {
  IconDelete,
  IconEdit,
  IconPlus,
} from "@arco-design/web-vue/es/icon";

import {
  useModelStore,
} from "../../../stores/models.js";

import SectionCard from "../../ui/SectionCard.vue";
import EmptyState from "../../ui/EmptyState.vue";
import ModelCapabilityBadges from "../../models/ModelCapabilityBadges.vue";

const store =
    useModelStore();

const saving =
    ref(false);

const testingID =
    ref("");

const diagnostics = ref({});

const capabilityFields = [
  {key: "tools", label: "Tool Calling", help: "模型是否支持函数/工具调用。Agent 暴露 Tool 时必须开启。"},
  {key: "vision", label: "Vision", help: "模型是否支持图片输入。"},
  {key: "files", label: "Files", help: "Provider 的原生文件输入能力元数据；当前 Humbert 附件入口尚不接收 PDF/Office。"},
  {key: "reasoning", label: "Reasoning", help: "标记模型具备原生推理能力，供后续路由与 UI 使用。"},
  {key: "json", label: "JSON", help: "模型是否支持可靠的结构化/JSON 输出模式。"},
  {key: "audio", label: "Audio", help: "模型的音频能力元数据；当前 Humbert 尚未实现音频附件链路。"},
];

function defaultCapabilityConfig() {
  return {tools: "auto", vision: "auto", files: "auto", reasoning: "auto", json: "auto", audio: "auto"};
}

const form =
    reactive({
      id: "",

      providerID: "",

      modelName: "",

      displayName: "",

      timeoutMS: 60000,

      contextWindow: 131072,

      maxOutputTokens: 8192,

      capabilityConfig: defaultCapabilityConfig(),

      enabled: true,
    });

const editing =
    computed(() => (
        form.id !== ""
    ));

/**
 * 将编辑器恢复为“新建模型”状态。
 *
 * 新建时优先选择第一个已经存在的 Provider，
 * 让用户无需重复选择最常用的默认项。
 */
function reset() {
  Object.assign(
      form,
      {
        id: "",

        providerID:
            store.providers[0]
                ?.id ?? "",

        modelName: "",

        displayName: "",

        timeoutMS: 60000,

        contextWindow: 131072,

        maxOutputTokens: 8192,

        capabilityConfig: defaultCapabilityConfig(),

        enabled: true,
      },
  );
}

/**
 * 将指定模型载入右侧编辑器。
 *
 * 表单只保存可编辑副本，真正的数据仍由 ModelStore 管理。
 */
function edit(model) {
  Object.assign(
      form,
      {
        id:
        model.id,

        providerID:
        model.providerID,

        modelName:
        model.modelName,

        displayName:
        model.displayName,

        timeoutMS:
        model.timeoutMS,

        contextWindow:
        model.contextWindow,

        maxOutputTokens:
        model.maxOutputTokens,

        capabilityConfig: {
          ...defaultCapabilityConfig(),
          ...(model.capabilityConfig ?? {}),
        },

        enabled:
        model.enabled,
      },
  );
}

/**
 * 创建或更新模型。
 *
 * 所有后端错误都在此处转为用户可见消息，
 * 不吞掉错误，也不在组件中维护第二份持久化状态。
 */
async function save() {
  if (!form.providerID) {
    Message.warning(
        "请先创建并选择供应商",
    );

    return;
  }

  if (
      !form.modelName.trim()
  ) {
    Message.warning(
        "请输入模型标识",
    );

    return;
  }

  const contextWindow =
      Number(form.contextWindow);
  const maxOutputTokens =
      Number(form.maxOutputTokens);

  if (
      !Number.isInteger(contextWindow) ||
      contextWindow < 4096 ||
      contextWindow > 2097152
  ) {
    Message.warning(
        "Context Window 必须是 4096 到 2097152 之间的整数",
    );
    return;
  }

  if (
      !Number.isInteger(maxOutputTokens) ||
      maxOutputTokens < 256 ||
      maxOutputTokens >= contextWindow
  ) {
    Message.warning(
        "最大输出 Token 必须至少为 256，且小于 Context Window",
    );
    return;
  }

  saving.value = true;

  try {
    const request = {
      providerID:
      form.providerID,

      modelName:
          form.modelName.trim(),

      displayName:
          form.displayName.trim(),

      timeoutMS:
          Number(form.timeoutMS),

      contextWindow:
          Number(form.contextWindow),

      maxOutputTokens:
          Number(form.maxOutputTokens),

      capabilityConfig: {...form.capabilityConfig},

      enabled:
          Boolean(form.enabled),
    };

    if (editing.value) {
      await store.updateModel(
          form.id,
          request,
      );

      Message.success(
          "模型已更新",
      );
    } else {
      await store.createModel(
          request,
      );

      Message.success(
          "模型已创建",
      );
    }

    reset();
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  } finally {
    saving.value = false;
  }
}

/**
 * 使用后端真实 Provider 配置测试模型连通性。
 */
async function test(model) {
  testingID.value =
      model.id;
  diagnostics.value = { ...diagnostics.value, [model.id]: null };

  try {
    const result =
        await store.diagnoseModel(
            model.id,
        );
    diagnostics.value = { ...diagnostics.value, [model.id]: result };
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  } finally {
    testingID.value = "";
  }
}

/**
 * 删除模型前使用 Humbert 应用内确认框二次确认。
 *
 * 后端仍会检查 Agent 对 Model 的引用关系，
 * 因此即使前端确认删除，也不会绕过领域完整性约束。
 */
async function remove(model) {
  const confirmed =
      await confirmAction({
        title: "删除模型",

        message:
            `确定删除模型「${model.displayName}」吗？历史会话记录会保留；如果仍有 Agent 在 Chat、Utility、Memory 角色中使用，或被多媒体设置选中，需要先切换相关配置。`,

        confirmText: "删除",
        danger: true,
      });

  if (!confirmed) {
    return;
  }

  try {
    await store.deleteModel(
        model.id,
    );

    if (
        form.id === model.id
    ) {
      reset();
    }

    Message.success(
        "模型已删除",
    );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  }
}

/**
 * 第一个 Provider 创建完成以后，
 * 新建 Model Form 自动选择它。
 */
watch(
    () =>
        store.providers
            .map(
                (provider) =>
                    provider.id,
            )
            .join(","),

    () => {
      if (
          !form.id &&
          !form.providerID &&
          store.providers.length > 0
      ) {
        form.providerID =
            store.providers[0].id;
      }
    },

    {
      immediate: true,
    },
);
</script>

<template>
  <div class="model-catalog">
    <SectionCard
        class="model-list"
        title="已配置模型"
        description="Agent 只能选择这里已经创建并启用的模型。"
    >
      <template #actions>
        <a-button size="small" @click="reset">
          <template #icon>
            <IconPlus/>
          </template>
          新建模型
        </a-button>
      </template>

      <EmptyState
          v-if="store.models.length === 0"
          title="还没有模型"
          description="请先创建供应商，再添加第一个模型。"
          compact
      />

      <div v-else class="model-items">
        <template v-for="model in store.models" :key="model.id">
        <article
            class="model-item h-list-row"
            :class="{
              'model-item--editing': form.id === model.id,
              'h-list-row--selected': form.id === model.id,
            }"
        >
          <span
              class="model-state"
              :class="{ 'model-state--enabled': model.enabled }"
              :title="model.enabled ? '已启用' : '已停用'"
          ></span>

          <div class="model-main">
            <div class="model-name">{{ model.displayName }}</div>
            <div class="model-meta">{{ model.providerName }} · {{ model.modelName }}</div>
            <ModelCapabilityBadges class="model-capabilities" :capabilities="model.capabilities" compact/>
          </div>

          <div class="model-actions">
            <a-button type="text" size="mini" :loading="testingID === model.id" @click="test(model)">
              测试
            </a-button>
            <a-button type="text" size="mini" aria-label="编辑模型" title="编辑模型" @click="edit(model)">
              <template #icon>
                <IconEdit/>
              </template>
            </a-button>
            <a-button type="text" size="mini" status="danger" aria-label="删除模型" title="删除模型"
                      @click="remove(model)">
              <template #icon>
                <IconDelete/>
              </template>
            </a-button>
          </div>
        </article>
        <div v-if="diagnostics[model.id]" :key="`${model.id}-diagnostic`" class="model-diagnostic" :class="{ 'model-diagnostic--error': !diagnostics[model.id].success }" role="status">
          <strong>{{ diagnostics[model.id].summary }}</strong>
          <span v-if="diagnostics[model.id].durationMS"> · {{ diagnostics[model.id].durationMS }} ms</span>
          <p>{{ diagnostics[model.id].action }}</p>
        </div>
        </template>
      </div>
    </SectionCard>

    <SectionCard
        class="model-editor"
        :title="editing ? '编辑模型' : '新建模型'"
        :description="editing ? '修改模型连接参数与可用状态。' : '把供应商中的一个模型注册到 Humbert。'"
    >
      <a-form :model="form" layout="vertical">
        <a-form-item label="供应商">
          <a-select v-model="form.providerID" placeholder="选择供应商">
            <a-option v-for="provider in store.providers" :key="provider.id" :value="provider.id">
              {{ provider.name }}
            </a-option>
          </a-select>
        </a-form-item>

        <a-form-item label="模型标识">
          <a-input v-model="form.modelName" placeholder="例如：gpt-5"/>
          <template #extra><span class="field-help">填写供应商 API 实际使用的 Model Name。</span></template>
        </a-form-item>

        <a-form-item label="显示名称">
          <a-input v-model="form.displayName" placeholder="留空时使用模型标识"/>
        </a-form-item>

        <a-form-item label="请求超时">
          <a-input-number v-model="form.timeoutMS" :min="1000" :max="300000" :step="1000" class="w-full">
            <template #suffix>ms</template>
          </a-input-number>
        </a-form-item>

        <a-form-item label="Context Window">
          <a-input-number v-model="form.contextWindow" :min="4096" :max="2097152" :step="4096" class="w-full">
            <template #suffix>tokens</template>
          </a-input-number>
          <template #extra>
            <span class="field-help">填写当前模型或本地部署实际支持的上下文窗口；Humbert 使用该值计算自动压缩阈值。</span>
          </template>
        </a-form-item>

        <a-form-item label="最大输出 Token">
          <a-input-number
              v-model="form.maxOutputTokens"
              :min="256"
              :max="Math.max(256, Number(form.contextWindow) - 1)"
              :step="256"
              class="w-full"
          >
            <template #suffix>tokens</template>
          </a-input-number>
          <template #extra>
            <span
                class="field-help">为模型回复预留的最大输出预算。该值必须小于 Context Window，并参与安全 Reserve 计算。</span>
          </template>
        </a-form-item>

        <div class="capability-editor">
          <div class="capability-editor__header">
            <div>
              <div class="enabled-title">模型能力</div>
              <div class="enabled-description">Auto 会根据 Provider 与模型名称做保守推断；不准确时请显式覆盖。</div>
            </div>
          </div>
          <div class="capability-grid">
            <a-form-item
                v-for="field in capabilityFields"
                :key="field.key"
                :label="field.label"
                class="capability-field"
            >
              <a-select v-model="form.capabilityConfig[field.key]">
                <a-option value="auto">Auto</a-option>
                <a-option value="enabled">支持</a-option>
                <a-option value="disabled">不支持</a-option>
              </a-select>
              <template #extra><span class="field-help">{{ t(field.help) }}</span></template>
            </a-form-item>
          </div>
        </div>

        <div class="enabled-row">
          <div>
            <div class="enabled-title">启用模型</div>
            <div class="enabled-description">停用后不会出现在 Agent 默认模型选择中。</div>
          </div>
          <a-switch v-model="form.enabled"/>
        </div>

        <div class="editor-actions">
          <a-button v-if="editing" :disabled="saving" @click="reset">取消编辑</a-button>
          <a-button type="primary" :loading="saving" :disabled="store.providers.length === 0" @click="save">
            {{ editing ? "保存模型" : "创建模型" }}
          </a-button>
        </div>
      </a-form>
    </SectionCard>
  </div>
</template>

<style scoped>
.model-diagnostic { margin: 0 12px 8px 20px; padding: 10px 12px; border-left: 2px solid var(--h-success); background: var(--h-bg); font-size: 12px; }
.model-diagnostic--error { border-left-color: var(--h-danger); }
.model-diagnostic p { margin: 4px 0 0; color: var(--h-text-muted); }
.model-catalog {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(320px, 380px);
  align-items: start;
  gap: var(--h-section-gap);
}

.model-list {
  overflow: hidden;
}

.model-items {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.model-item {
  min-height: 58px;
}

.model-state {
  width: 7px;
  height: 7px;
  flex: 0 0 7px;
  border-radius: 50%;
  background: var(--h-text-muted);
}

.model-state--enabled {
  background: var(--h-success);
}

.model-main {
  min-width: 0;
  flex: 1;
}

.model-name {
  overflow: hidden;
  color: var(--h-text);
  font-size: 12px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-meta {
  margin-top: 4px;
  overflow: hidden;
  color: var(--h-text-muted);
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-actions {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 2px;
}

.field-help {
  color: var(--h-text-muted);
  font-size: 10px;
}

.model-capabilities {
  margin-top: 6px;
}

.capability-editor {
  margin: 4px 0 18px;
  padding: 12px;
  border: 1px solid var(--h-border);
  border-radius: 9px;
  background: var(--h-surface-soft, var(--h-surface));
}

.capability-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0 12px;
  margin-top: 12px;
}

.capability-field {
  margin-bottom: 12px;
}

.enabled-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  margin: 2px 0 22px;
  padding: 12px 0;
  border-top: 1px solid var(--h-border);
  border-bottom: 1px solid var(--h-border);
}

.enabled-title {
  color: var(--h-text-secondary);
  font-size: 12px;
  font-weight: 600;
}

.enabled-description {
  margin-top: 3px;
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.5;
}

.editor-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
}

@media (max-width: 1040px) {
  .model-catalog {
    grid-template-columns: 1fr;
  }
}
</style>
