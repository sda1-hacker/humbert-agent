<script setup>
import {
  computed,
  reactive,
  ref,
} from "vue";

import {
  Message,
} from "@arco-design/web-vue";

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

const store =
    useModelStore();

const saving =
    ref(false);

const form =
    reactive({
      id: "",

      name: "",

      type: "openai",

      baseURL: "",

      apiKey: "",

      clearAPIKey: false,
    });

const editing =
    computed(() => (
        form.id !== ""
    ));

/**
 * BaseURL 只提供建议值，不强制写入。
 *
 * 用户始终可以配置官方地址、自建网关、代理或其他兼容服务。
 */
const baseURLPlaceholder =
    computed(() => {
      switch (form.type) {
        case "openai":
          return (
              "https://api.openai.com/v1"
          );

        case "openai_compatible":
          return (
              "https://api.example.com/v1"
          );

        case "ollama":
          return (
              "http://127.0.0.1:11434"
          );

        default:
          return "https://...";
      }
    });

/**
 * 将右侧编辑器恢复到“新建供应商”状态。
 *
 * API Key 永远不会因为 reset 被读取或回显。
 */
function reset() {
  Object.assign(
      form,
      {
        id: "",

        name: "",

        type: "openai",

        baseURL: "",

        apiKey: "",

        clearAPIKey: false,
      },
  );
}

/**
 * 编辑已有供应商。
 *
 * 后端不会返回 API Key，组件也不会尝试恢复旧密钥。
 * 用户留空时表示保留当前 Credential。
 */
function edit(provider) {
  Object.assign(
      form,
      {
        id:
        provider.id,

        name:
        provider.name,

        type:
        provider.type,

        baseURL:
            provider.baseURL ?? "",

        apiKey: "",

        clearAPIKey: false,
      },
  );
}

/**
 * 创建或更新供应商。
 *
 * Credential 是否更新通过 updateAPIKey 显式告诉后端，
 * 避免空字符串意外覆盖已有密钥。
 */
async function save() {
  const name =
      form.name.trim();

  if (!name) {
    Message.warning(
        "请输入供应商名称",
    );

    return;
  }

  saving.value = true;

  try {
    if (editing.value) {
      await store.updateProvider(
          form.id,
          {
            name,

            type:
            form.type,

            baseURL:
                form.baseURL.trim(),

            apiKey:
                form.clearAPIKey
                    ? ""
                    : form.apiKey,

            updateAPIKey:
                form.clearAPIKey ||
                form.apiKey.trim() !== "",
          },
      );

      Message.success(
          "供应商已更新",
      );
    } else {
      await store.createProvider({
        name,

        type:
        form.type,

        baseURL:
            form.baseURL.trim(),

        apiKey:
        form.apiKey,
      });

      Message.success(
          "供应商已创建",
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
 * 删除供应商前进行二次确认。
 *
 * 如果仍有 Model 引用该 Provider，
 * 后端会继续负责最终的完整性检查。
 */
async function remove(provider) {
  const confirmed =
      await confirmAction({
        title:
            "删除供应商",

        message:
            `确定删除供应商「${provider.name}」吗？`,

        confirmText: "删除",
        danger: true,
      });

  if (!confirmed) {
    return;
  }

  try {
    await store.deleteProvider(
        provider.id,
    );

    if (
        form.id === provider.id
    ) {
      reset();
    }

    Message.success(
        "供应商已删除",
    );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  }
}
</script>

<template>
  <div class="provider-settings">
    <SectionCard
        class="provider-list"
        title="已配置供应商"
        description="供应商保存连接信息，API Key 单独进入 Humbert Credential Store。"
    >
      <template #actions>
        <a-button size="small" @click="reset">
          <template #icon>
            <IconPlus/>
          </template>
          新建供应商
        </a-button>
      </template>

      <EmptyState
          v-if="store.providers.length === 0"
          title="还没有供应商"
          description="创建 OpenAI、兼容 API 或 Ollama 连接后，再添加模型。"
          compact
      />

      <div v-else class="provider-items">
        <article
            v-for="provider in store.providers"
            :key="provider.id"
            class="provider-item h-list-row"
            :class="{
              'provider-item--editing': form.id === provider.id,
              'h-list-row--selected': form.id === provider.id,
            }"
        >
          <div class="provider-main">
            <div class="provider-name">{{ provider.name }}</div>
            <div class="provider-meta">
              {{ provider.type }}
              <template v-if="provider.baseURL"> · {{ provider.baseURL }}</template>
            </div>
          </div>
          <div class="provider-actions">
            <a-button type="text" size="mini" aria-label="编辑供应商" title="编辑供应商" @click="edit(provider)">
              <template #icon>
                <IconEdit/>
              </template>
            </a-button>
            <a-button type="text" size="mini" status="danger" aria-label="删除供应商" title="删除供应商"
                      @click="remove(provider)">
              <template #icon>
                <IconDelete/>
              </template>
            </a-button>
          </div>
        </article>
      </div>
    </SectionCard>

    <SectionCard
        class="provider-editor"
        :title="editing ? '编辑供应商' : '新建供应商'"
        :description="editing ? '修改服务地址或更新 Credential。' : '创建模型之前，需要先配置一个供应商。'"
    >
      <a-form :model="form" layout="vertical">
        <a-form-item label="名称">
          <a-input v-model="form.name" placeholder="例如：OpenAI"/>
        </a-form-item>

        <a-form-item label="类型">
          <a-select v-model="form.type">
            <a-option value="openai">OpenAI</a-option>
            <a-option value="openai_compatible">OpenAI Compatible</a-option>
            <a-option value="ollama">Ollama</a-option>
          </a-select>
        </a-form-item>

        <a-form-item label="Base URL">
          <a-input v-model="form.baseURL" :placeholder="baseURLPlaceholder"/>
          <template #extra><span class="field-help">可留空使用默认地址，也可以填写自建网关、代理或兼容服务。</span>
          </template>
        </a-form-item>

        <a-form-item v-if="form.type !== 'ollama'" label="API Key">
          <a-input-password
              v-model="form.apiKey"
              autocomplete="off"
              :placeholder="editing ? '留空表示保持现有密钥' : '输入 API Key'"
          />
          <template #extra><span class="field-help">API Key 不会写入 providers.json，也不会从后端回显。</span></template>
        </a-form-item>

        <div v-if="editing && form.type !== 'ollama'" class="credential-row">
          <div>
            <div class="credential-title">删除已有 API Key</div>
            <div class="credential-description">保存后清除当前供应商关联的 Credential。</div>
          </div>
          <a-checkbox v-model="form.clearAPIKey"/>
        </div>

        <div class="editor-actions">
          <a-button v-if="editing" :disabled="saving" @click="reset">取消编辑</a-button>
          <a-button type="primary" :loading="saving" @click="save">
            {{ editing ? "保存供应商" : "创建供应商" }}
          </a-button>
        </div>
      </a-form>
    </SectionCard>
  </div>
</template>

<style scoped>
.provider-settings {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(320px, 380px);
  align-items: start;
  gap: var(--h-section-gap);
}

.provider-list {
  overflow: hidden;
}

.provider-items {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.provider-item {
  min-height: 58px;
}

.provider-main {
  min-width: 0;
  flex: 1;
}

.provider-name {
  overflow: hidden;
  color: var(--h-text);
  font-size: 12px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.provider-meta {
  margin-top: 4px;
  overflow: hidden;
  color: var(--h-text-muted);
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.provider-actions {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 2px;
}

.field-help {
  color: var(--h-text-muted);
  font-size: 10px;
}

.credential-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  margin: 2px 0 22px;
  padding: 12px 0;
  border-top: 1px solid var(--h-border);
  border-bottom: 1px solid var(--h-border);
}

.credential-title {
  color: var(--h-text-secondary);
  font-size: 12px;
  font-weight: 600;
}

.credential-description {
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
  .provider-settings {
    grid-template-columns: 1fr;
  }
}
</style>
