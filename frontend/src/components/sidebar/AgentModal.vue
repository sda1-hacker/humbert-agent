<script setup>
import { computed, onMounted, reactive, ref, watch } from "vue";
import { IconDelete, IconFolder } from "@arco-design/web-vue/es/icon";
import { Message } from "../../utils/uiMessage.js";
import { confirmAction } from "../../utils/confirm.js";
import { createAgentForm, agentFormRequest } from "../../utils/agentForm.js";
import { DEFAULT_AGENT_AVATAR } from "../../utils/avatar.js";
import { getSandboxStatus, listBuiltinTools, selectWorkspaceDirectory } from "../../api/agents.js";
import { useAgentStore } from "../../stores/agents.js";
import { useModelStore } from "../../stores/models.js";
import SkillSelector from "../skills/SkillSelector.vue";
import AgentSecurityEditor from "../agents/AgentSecurityEditor.vue";
import ModelCapabilityBadges from "../models/ModelCapabilityBadges.vue";
import IdentityAvatar from "../ui/IdentityAvatar.vue";

const props = defineProps({ agent: { type: Object, default: null } });
const visible = defineModel("visible", { type: Boolean, default: false });
const emit = defineEmits(["created", "updated", "deleted"]);
const agentStore = useAgentStore();
const modelStore = useModelStore();
const saving = ref(false);
const selectingWorkspace = ref(false);
const avatarInput = ref(null);
const activeTab = ref("basic");
const builtinCatalog = ref([]);
const platformSandboxStatus = ref(null);
const form = reactive(createAgentForm(null));
const creating = computed(() => !form.id);
const modalTitle = computed(() => creating.value ? "新建 Agent" : "Agent 设置");

// 已禁用但仍被 Agent 引用的模型保持可见，用户才能辨认和修复已有配置。
function roleModelOptions(selectedID) {
  return modelStore.models.filter(model => model.enabled || model.id === selectedID);
}
const availableModels = computed(() => roleModelOptions(form.modelID));
const selectedChatModel = computed(() => modelStore.modelByID(form.modelID));
const workspaceDisplay = computed(() => form.workspaceMode === "custom"
  ? form.workspacePath || "尚未选择目录"
  : form.workspaceDisplayPath || "~/.humbert-agent/workspaces/<agent-id>");

/** 每次打开或更换目标 Agent 都创建独立表单。模型、目录与安全状态来自共享 Store/服务。 */
function resetForm(agent) {
  activeTab.value = "basic";
  Object.assign(form, createAgentForm(agent, {
    tools: builtinCatalog.value,
    sandboxStatus: platformSandboxStatus.value,
    defaultModelID: modelStore.enabledModels[0]?.id || "",
  }));
}
watch([() => visible.value, () => props.agent?.id], ([opened]) => {
  if (opened) resetForm(props.agent);
}, { immediate: true });

// 模型目录可能在弹窗之后才到达，只给尚未选择模型的新 Agent 设置默认值。
watch(() => modelStore.enabledModels.map(model => model.id).join(","), () => {
  if (creating.value && !form.modelID) form.modelID = modelStore.enabledModels[0]?.id || "";
});

onMounted(async () => {
  try {
    const [tools, status] = await Promise.all([listBuiltinTools(), getSandboxStatus()]);
    builtinCatalog.value = Array.isArray(tools) ? tools : [];
    platformSandboxStatus.value = status || null;
    if (!visible.value) return;
    // 目录加载结束只补齐展示信息，不重置名称、指令、头像等正在编辑的内容。
    // 用户手动调整工具时会把 builtinToolsConfigured 标成 true，显式空选择也不能被默认值覆盖。
    const defaults = createAgentForm(props.agent, { tools: builtinCatalog.value, sandboxStatus: status });
    form.availableBuiltinTools = defaults.availableBuiltinTools;
    form.sandboxStatus = defaults.sandboxStatus;
    if (!form.builtinToolsConfigured) {
      form.enabledBuiltinTools = defaults.enabledBuiltinTools;
      form.builtinToolsConfigured = defaults.builtinToolsConfigured;
    }
  } catch (error) {
    Message.warning(error?.message || "无法加载 Agent 安全配置");
  }
});

/** 原生目录选择器只返回用户选择的路径，空字符串表示取消，不改变当前表单。 */
async function chooseWorkspace() {
  if (selectingWorkspace.value) return;
  selectingWorkspace.value = true;
  try {
    const selected = await selectWorkspaceDirectory(form.workspaceMode === "custom" ? form.workspacePath : "");
    if (selected) {
      form.workspaceMode = "custom";
      form.workspacePath = selected;
      form.workspaceDisplayPath = selected;
    }
  } catch (error) {
    Message.error(error?.message || String(error));
  } finally {
    selectingWorkspace.value = false;
  }
}

/** 头像只保留受限的图片 Data URL，真实格式与安全性仍由后端 avatar 模块校验。 */
async function chooseAvatar(event) {
  const input = event?.target;
  const file = input?.files?.[0];
  if (input) input.value = "";
  if (!file) return;
  if (!["image/png", "image/jpeg", "image/gif", "image/webp"].includes(String(file.type || "").toLowerCase())) {
    Message.warning("头像仅支持 PNG、JPEG、GIF 或 WebP");
    return;
  }
  if (file.size <= 0 || file.size > 2 * 1024 * 1024) {
    Message.warning("头像图片不能超过 2 MiB");
    return;
  }
  try {
    form.avatar = await new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onerror = () => reject(reader.error || new Error("读取头像失败"));
      reader.onload = () => resolve(String(reader.result || ""));
      reader.readAsDataURL(file);
    });
  } catch (error) {
    Message.error(error?.message || String(error));
  }
}

/** 创建和更新共用请求投影、错误反馈及收尾；只有更换已有目录需要额外确认。 */
async function save() {
  if (saving.value) return;
  if (!form.name.trim()) { Message.warning("请输入 Agent 名称"); return; }
  if (form.workspaceMode === "custom" && !form.workspacePath.trim()) {
    Message.warning("请选择 Agent 目录");
    return;
  }
  const id = form.id;
  const request = agentFormRequest(form);
  saving.value = true;
  try {
    const previous = agentStore.items.find(item => item.id === id);
    const changed = previous && (previous.workspaceMode !== request.workspaceMode ||
      request.workspaceMode === "custom" && previous.workspacePath !== request.workspacePath);
    if (changed && !await confirmAction({
      title: "更换 Agent 目录",
      message: "更换 Workspace 只影响之后的 Agent Turn，不会移动或删除原目录中的文件。是否继续？",
      confirmText: "继续更换",
    })) return;
    const result = id ? await agentStore.update(id, request) : await agentStore.create(request);
    visible.value = false;
    emit(id ? "updated" : "created", result);
    Message.success(id ? "Agent 设置已保存" : "Agent 已创建");
  } catch (error) {
    Message.error(error?.message || String(error));
  } finally {
    saving.value = false;
  }
}

/** 删除操作交给后端聚合生命周期：清理会话与托管目录，自定义外部目录只解除引用。 */
async function removeAgent() {
  const id = form.id;
  if (!id) return;
  try {
    if (!await confirmAction({
      title: "删除 Agent",
      message: `确定删除 Agent「${form.name}」吗？该 Agent 的全部对话、附件、记忆和 Humbert 管理的 Workspace 会一并删除；如果使用的是自定义外部 Workspace，外部文件不会被删除。`,
      confirmText: "删除", danger: true,
    })) return;
    await agentStore.remove(id);
    visible.value = false;
    emit("deleted", id);
    Message.success("Agent 已删除");
  } catch (error) {
    Message.error(error?.message || String(error));
  }
}
</script>

<template>
  <a-modal
      v-model:visible="visible"
      :title="modalTitle"
      :width="700"
      :mask-closable="!saving"
      :esc-to-close="!saving"
      modal-class="agent-settings-modal"
  >
    <a-tabs
        v-model:active-key="activeTab"
        class="agent-settings-tabs"
    >
      <a-tab-pane key="basic" title="基本设置">
        <a-form :model="form" layout="vertical" class="agent-tab-form">
          <div class="agent-avatar-editor">
            <IdentityAvatar :src="form.avatar || DEFAULT_AGENT_AVATAR" :name="form.name || 'Agent'" :size="64"/>
            <div class="agent-avatar-editor__actions">
              <strong>Agent 头像</strong>
              <span>显示在聊天消息中；未选择时使用默认头像。</span>
              <div>
                <a-button size="small" @click="avatarInput?.click()">选择头像</a-button>
                <a-button v-if="form.avatar" size="small" type="text" status="danger" @click="form.avatar = ''">移除
                </a-button>
              </div>
            </div>
            <input
                ref="avatarInput"
                class="agent-avatar-input"
                type="file"
                accept="image/png,image/jpeg,image/gif,image/webp"
                @change="chooseAvatar"
            />
          </div>
          <a-form-item label="Agent 名称">
            <a-input
                v-model="form.name"
                maxlength="100"
                placeholder="例如：Humbert"
            />
          </a-form-item>
          <a-form-item label="子 Agent 协作">
            <div class="subagent-option">
              <div>
                <strong>允许作为子 Agent 调用</strong>
                <span>启用后，其他 Agent 可以通过 run_agent 把自包含任务交给这个 Agent。</span>
              </div>
              <a-switch v-model="form.subagentEnabled" />
            </div>
          </a-form-item>
          <a-form-item label="Chat 模型">
            <a-select
                v-model="form.modelID"
                allow-clear
                allow-search
                placeholder="请选择主对话模型"
            >
              <a-option
                  v-for="model in availableModels"
                  :key="model.id"
                  :value="model.id"
                  :disabled="!model.enabled"
              >
                {{ model.displayName }} · {{ model.providerName }}{{ model.enabled ? "" : "（已禁用）" }}
              </a-option>
            </a-select>
            <template #extra>
              <div class="model-role-extra">
                <span>普通对话和默认 Runtime 使用该模型。</span>
                <ModelCapabilityBadges v-if="selectedChatModel" :capabilities="selectedChatModel.capabilities" compact/>
              </div>
            </template>
          </a-form-item>
          <div class="model-role-panel">
            <div class="model-role-panel__intro">
              <strong>模型角色</strong>
              <span>留空会自动回退：Utility → Chat；视觉辅助模型只在 Chat 无法直接处理当前图片输入时使用。</span>
            </div>
            <div class="model-role-grid">
              <a-form-item label="Utility 模型">
                <a-select v-model="form.modelRoles.utilityModelID" allow-clear allow-search
                          placeholder="回退 Chat 模型">
                  <a-option
                      v-for="model in roleModelOptions(form.modelRoles.utilityModelID)"
                      :key="model.id" :value="model.id" :disabled="!model.enabled"
                  >
                    {{ model.displayName }} · {{ model.providerName }}{{ model.enabled ? "" : "（已禁用）" }}
                  </a-option>
                </a-select>
                <template #extra>Context 压缩等辅助任务优先使用；窗口不足时 Runtime 会回退当前执行模型。</template>
              </a-form-item>
            </div>
          </div>
          <a-form-item label="Skills">
            <SkillSelector v-model="form.enabledSkills"/>
          </a-form-item>
          <a-form-item label="Agent 目录">
            <div class="workspace-editor">
              <a-radio-group v-model="form.workspaceMode">
                <a-radio value="managed">Humbert 管理</a-radio>
                <a-radio value="custom">自定义目录</a-radio>
              </a-radio-group>
              <div class="workspace-path">
                <div class="workspace-path-text" :title="workspaceDisplay">
                  {{ workspaceDisplay }}
                </div>
                <a-button
                    v-if="form.workspaceMode === 'custom'"
                    type="secondary"
                    :loading="selectingWorkspace"
                    @click="chooseWorkspace"
                >
                  <template #icon>
                    <IconFolder/>
                  </template>
                  {{ form.workspacePath ? "重新选择" : "选择目录" }}
                </a-button>
              </div>
              <div class="workspace-help">
                <template v-if="form.workspaceMode === 'managed'">
                  Humbert 会为这个 Agent 创建独立工作目录。
                </template>
                <template v-else>
                  这是 Agent 的主要工作目录，可正常读写。普通用户文件是否可读由全局“设置 → 安全”决定。
                </template>
              </div>
            </div>
          </a-form-item>
        </a-form>
      </a-tab-pane>
      <a-tab-pane key="config" title="Agent 配置">
        <div class="agent-config-pane">
          <div class="agent-config-intro">
            <strong>文件访问与工具</strong>
            <span>
              这里保存的是这个 Agent 自己的配置。额外可读取目录会写入 Agent Profile，并在之后的对话中继续生效。
            </span>
          </div>
          <AgentSecurityEditor
              v-model:sandbox="form.sandbox"
              v-model:enabled-builtin-tools="form.enabledBuiltinTools"
              @update:enabled-builtin-tools="form.builtinToolsConfigured = true"
              :available-builtin-tools="form.availableBuiltinTools"
              :sandbox-status="form.sandboxStatus"
          />
        </div>
      </a-tab-pane>
      <a-tab-pane key="instruction" title="Agent 指令">
        <div class="instruction-pane">
          <div class="agent-config-intro">
            <strong>Agent Instruction</strong>
            <span>定义这个 Agent 的角色、原则、约束和工作方式。</span>
          </div>
          <a-textarea
              v-model="form.instruction"
              :auto-size="{ minRows: 12, maxRows: 18 }"
              placeholder="定义这个 Agent 的角色、原则与行为方式。"
          />
        </div>
      </a-tab-pane>
    </a-tabs>
    <template #footer>
      <div class="agent-modal-footer">
        <a-button
            v-if="!creating"
            type="text"
            status="danger"
            :disabled="saving"
            @click="removeAgent"
        >
          <template #icon>
            <IconDelete/>
          </template>
          删除 Agent
        </a-button>
        <span v-else></span>
        <a-space>
          <a-button :disabled="saving" @click="visible = false">取消</a-button>
          <a-button type="primary" :loading="saving" @click="save">
            {{ creating ? "创建 Agent" : "保存" }}
          </a-button>
        </a-space>
      </div>
    </template>
  </a-modal>
</template>
<style scoped>
.agent-settings-tabs {
  min-height: 420px;
}
.agent-tab-form,
.agent-config-pane,
.instruction-pane {
  padding: 4px 2px 8px;
}
.agent-avatar-editor {
  display: flex;
  align-items: center;
  gap: 14px;
  margin-bottom: 18px;
  padding: 12px;
  border: 1px solid var(--h-border);
  border-radius: 9px;
  background: var(--h-surface-soft, var(--h-surface));
}
.agent-avatar-editor__actions {
  display: grid;
  gap: 4px;
}
.agent-avatar-editor__actions strong {
  color: var(--h-text);
  font-size: 12px;
}
.agent-avatar-editor__actions span {
  color: var(--h-text-muted);
  font-size: 10px;
}
.agent-avatar-editor__actions > div {
  display: flex;
  gap: 6px;
  margin-top: 3px;
}
.agent-avatar-input {
  display: none;
}
.agent-config-pane,
.instruction-pane {
  display: grid;
  gap: 16px;
}
.agent-config-intro {
  display: grid;
  gap: 5px;
  padding: 10px 12px;
  border: 1px solid var(--h-border);
  border-radius: 9px;
  background: var(--h-surface-soft, var(--h-surface));
}
.agent-config-intro strong {
  color: var(--h-text);
  font-size: 12px;
}
.agent-config-intro span {
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.6;
}
.subagent-option {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  padding: 11px 12px;
  border: 1px solid var(--h-border);
  border-radius: 9px;
  background: var(--h-surface-soft, var(--h-surface));
}
.subagent-option > div {
  display: grid;
  gap: 4px;
}
.subagent-option strong {
  color: var(--h-text);
  font-size: 12px;
}
.subagent-option span {
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.55;
}
.model-role-extra {
  display: grid;
  gap: 6px;
}
.model-role-panel {
  margin-bottom: 18px;
  padding: 12px;
  border: 1px solid var(--h-border);
  border-radius: 9px;
  background: var(--h-surface-soft, var(--h-surface));
}
.model-role-panel__intro {
  display: grid;
  gap: 4px;
  margin-bottom: 12px;
}
.model-role-panel__intro strong {
  color: var(--h-text);
  font-size: 12px;
}
.model-role-panel__intro span,
.model-role-panel :deep(.arco-form-item-extra) {
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.55;
}
.model-role-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0 12px;
}
.workspace-editor {
  width: 100%;
  padding: 12px;
  border: 1px solid var(--h-border);
  border-radius: 9px;
}
.workspace-path {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 10px;
  margin-top: 12px;
}
.workspace-path-text {
  min-width: 0;
  flex: 1;
  overflow: hidden;
  padding: 7px 9px;
  border: 1px solid var(--h-border);
  border-radius: 7px;
  background: var(--h-surface);
  color: var(--h-text-secondary);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.workspace-help {
  margin-top: 8px;
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.6;
}
.agent-modal-footer {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
}
:deep(.agent-settings-modal .arco-modal-body) {
  max-height: 72vh;
  overflow-y: auto;
}
@media (max-width: 760px) {
  .model-role-grid {
    grid-template-columns: 1fr;
  }
  .agent-settings-tabs {
    min-height: 360px;
  }
}
</style>
