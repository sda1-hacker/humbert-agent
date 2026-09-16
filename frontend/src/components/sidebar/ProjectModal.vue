<script setup>
import {
  computed,
  onMounted,
  reactive,
  ref,
  watch,
} from "vue";

import {
  Message,
} from "@arco-design/web-vue";

import {
  Dialogs,
} from "@wailsio/runtime";

import {
  IconDelete,
  IconFolder,
} from "@arco-design/web-vue/es/icon";

import {
  getSandboxStatus,
  listBuiltinTools,
  selectWorkspaceDirectory,
} from "../../api/agents.js";

import {
  useAgentStore,
} from "../../stores/agents.js";

import {
  useModelStore,
} from "../../stores/models.js";

import SkillSelector
  from "../skills/SkillSelector.vue";

import AgentSecurityEditor
  from "../agents/AgentSecurityEditor.vue";

import ModelCapabilityBadges
  from "../models/ModelCapabilityBadges.vue";

const props =
    defineProps({
      project: {
        type: Object,
        default: null,
      },
    });

const visible =
    defineModel(
        "visible",
        {
          type: Boolean,
          default: false,
        },
    );

const emit =
    defineEmits([
      "created",
      "updated",
      "deleted",
    ]);

const agentStore =
    useAgentStore();

const modelStore =
    useModelStore();

const saving =
    ref(false);

const selectingWorkspace =
    ref(false);

const activeTab =
    ref("basic");

const builtinCatalog =
    ref([]);

const platformSandboxStatus =
    ref(null);

const form =
    reactive({
      id: "",

      name: "",

      instruction: "",

      modelID: "",

      modelRoles: {
        utilityModelID: "",
        memoryModelID: "",
        visionModelID: "",
      },

      enabledSkills: [],

      workspaceMode:
          "managed",

      workspacePath: "",

      workspaceDisplayPath:
          "",

      enabledBuiltinTools: [],
      availableBuiltinTools: [],
      builtinToolsConfigured: false,
      sandbox: {
        profile: "",
        additionalWritePaths: [],
        networkMode: "",
        nativeMode: "",
      },
      sandboxStatus: null,
    });

const creating =
    computed(() =>
        !form.id,
    );

/**
 * 已禁用但当前 Project 正在使用的 Model
 * 仍然必须展示出来。
 */
const availableModels =
    computed(() =>
        modelStore.models.filter(
            (model) =>
                model.enabled ||
                model.id ===
                form.modelID,
        ),
    );

function roleModelOptions(selectedID) {
  return modelStore.models.filter((model) => model.enabled || model.id === selectedID);
}

function modelForID(id) {
  if (!id) return null;
  return modelStore.modelByID(id);
}

const selectedChatModel = computed(() => modelForID(form.modelID));

const modalTitle =
    computed(() =>
        creating.value
            ? "新建项目"
            : "项目设置",
    );

const workspaceDisplay =
    computed(() => {
      if (
          form.workspaceMode ===
          "custom"
      ) {
        return (
            form.workspacePath ||
            "尚未选择目录"
        );
      }

      if (
          form.workspaceDisplayPath
      ) {
        return (
            form.workspaceDisplayPath
        );
      }

      return (
          "~/.humbert-agent/workspaces/<agent-id>"
      );
    });

/**
 * 根据当前 Project 重置表单。
 *
 * project=null 表示创建。
 */
function resetForm(project) {
  activeTab.value = "basic";

  if (!project) {
    Object.assign(
        form,
        {
          id: "",

          name: "",

          instruction: "",

          modelID:
              modelStore
                  .enabledModels[0]
                  ?.id ?? "",

          modelRoles: {
            utilityModelID: "",
            memoryModelID: "",
            visionModelID: "",
          },

          enabledSkills: [],

          workspaceMode:
              "managed",

          workspacePath: "",

          workspaceDisplayPath:
              "",

          enabledBuiltinTools:
              builtinCatalog.value.map((tool) => tool.name),
          availableBuiltinTools:
              [...builtinCatalog.value],
          builtinToolsConfigured:
              builtinCatalog.value.length > 0,
          sandbox: {
            profile: "",
            additionalWritePaths: [],
            networkMode: "",
            nativeMode: "",
          },
          sandboxStatus:
          platformSandboxStatus.value,
        },
    );

    return;
  }

  Object.assign(
      form,
      {
        id:
        project.id,

        name:
        project.name,

        instruction:
        project.instruction,

        modelID:
        project.modelID,

        modelRoles: {
          utilityModelID: project.modelRoles?.utilityModelID ?? "",
          memoryModelID: project.modelRoles?.memoryModelID ?? "",
          visionModelID: project.modelRoles?.visionModelID ?? "",
        },

        enabledSkills:
            Array.isArray(project.enabledSkills)
                ? [...project.enabledSkills]
                : [],

        workspaceMode:
            project.workspaceMode ||
            "managed",

        workspacePath:
            project.workspacePath ||
            "",

        workspaceDisplayPath:
            project.workspaceDisplayPath ||
            "",

        availableBuiltinTools:
            Array.isArray(project.availableBuiltinTools) &&
            project.availableBuiltinTools.length > 0
                ? [...project.availableBuiltinTools]
                : [...builtinCatalog.value],
        enabledBuiltinTools:
            project.builtinToolsConfigured
                ? [...(project.enabledBuiltinTools ?? [])]
                : (
                    Array.isArray(project.availableBuiltinTools) &&
                    project.availableBuiltinTools.length > 0
                        ? project.availableBuiltinTools
                        : builtinCatalog.value
                ).map((tool) => tool.name),
        builtinToolsConfigured:
            Boolean(project.builtinToolsConfigured),
        sandbox: {
          profile: project.sandbox?.profile ?? "",
          additionalWritePaths: [...(project.sandbox?.additionalWritePaths ?? [])],
          networkMode: project.sandbox?.networkMode ?? "",
          nativeMode: project.sandbox?.nativeMode ?? "",
        },
        sandboxStatus:
            project.sandboxStatus ?? platformSandboxStatus.value,
      },
  );
}

onMounted(async () => {
  try {
    const [tools, sandboxStatus] = await Promise.all([
      listBuiltinTools(),
      getSandboxStatus(),
    ]);
    builtinCatalog.value = Array.isArray(tools) ? tools : [];
    platformSandboxStatus.value = sandboxStatus ?? null;

    if (visible.value) {
      resetForm(props.project);
    }
  } catch (error) {
    Message.warning(error?.message ?? "无法加载 Agent 安全配置");
  }
});

/**
 * 每次打开都重新读取 Project，
 * 防止 Modal 保存旧表单状态。
 */
watch(
    [
      () => visible.value,
      () => props.project?.id,
    ],

    ([opened]) => {
      if (!opened) {
        return;
      }

      resetForm(
          props.project,
      );
    },

    {
      immediate: true,
    },
);

/**
 * Model 后加载完成时，新建 Project 自动选择第一个可用模型。
 */
watch(
    () =>
        modelStore.enabledModels
            .map(
                (model) =>
                    model.id,
            )
            .join(","),

    () => {
      if (
          creating.value &&
          !form.modelID &&
          modelStore
              .enabledModels
              .length > 0
      ) {
        form.modelID =
            modelStore
                .enabledModels[0]
                .id;
      }
    },
);

/**
 * 使用 Wails Native Directory Dialog 选择 Workspace。
 */
async function chooseWorkspace() {
  if (
      selectingWorkspace.value
  ) {
    return;
  }

  selectingWorkspace.value =
      true;

  try {
    const selected =
        await selectWorkspaceDirectory(
            form.workspaceMode ===
            "custom"
                ? form.workspacePath
                : "",
        );

    /**
     * 空字符串代表用户取消。
     */
    if (!selected) {
      return;
    }

    form.workspaceMode =
        "custom";

    form.workspacePath =
        selected;

    form.workspaceDisplayPath =
        selected;
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  } finally {
    selectingWorkspace.value =
        false;
  }
}

/**
 * 创建或更新 Project。
 *
 * UI 中的 Project 就是 Agent；Agent 自己拥有 Workspace、模型、Skills 与安全能力。
 */
async function save() {
  if (saving.value) {
    return;
  }

  const name =
      form.name.trim();

  if (!name) {
    Message.warning(
        "请输入项目名称",
    );

    return;
  }

  if (
      form.workspaceMode ===
      "custom" &&
      !form.workspacePath.trim()
  ) {
    Message.warning(
        "请选择项目目录",
    );

    return;
  }

  saving.value = true;

  try {
    const request = {
      name,

      instruction:
      form.instruction,

      modelID:
      form.modelID,

      modelRoles: {
        utilityModelID: form.modelRoles.utilityModelID || "",
        memoryModelID: form.modelRoles.memoryModelID || "",
        visionModelID: form.modelRoles.visionModelID || "",
      },

      enabledSkills:
          [...form.enabledSkills],

      workspaceMode:
      form.workspaceMode,

      /**
       * Managed Workspace 永远不提交自定义路径。
       */
      workspacePath:
          form.workspaceMode ===
          "custom"
              ? form.workspacePath
              : "",

      builtinToolsConfigured:
          form.builtinToolsConfigured || form.availableBuiltinTools.length > 0,
      enabledBuiltinTools:
          [...form.enabledBuiltinTools],
      sandbox: {
        profile: form.sandbox.profile,
        additionalWritePaths: [...form.sandbox.additionalWritePaths],
        networkMode: form.sandbox.networkMode,
        nativeMode: form.sandbox.nativeMode,
      },
    };

    if (creating.value) {
      const result =
          await agentStore.create(
              request,
          );

      visible.value = false;

      emit(
          "created",
          result,
      );

      Message.success(
          "项目已创建",
      );

      return;
    }

    const previous =
        agentStore.items.find(
            (item) =>
                item.id ===
                form.id,
        );

    const workspaceChanged =
        previous &&
        (
            previous.workspaceMode !==
            form.workspaceMode ||
            (
                form.workspaceMode ===
                "custom" &&
                previous.workspacePath !==
                form.workspacePath
            )
        );

    if (workspaceChanged) {
      const answer =
          await Dialogs.Question({
            Title:
                "更换项目目录",

            Message:
                "更换 Workspace 只影响之后的 Agent Turn，不会移动或删除原目录中的文件。是否继续？",

            Buttons: [
              {
                Label:
                    "继续更换",

                IsDefault:
                    false,
              },
              {
                Label:
                    "取消",

                IsDefault:
                    true,
              },
            ],
          });

      if (
          answer !==
          "继续更换"
      ) {
        return;
      }
    }

    const result =
        await agentStore.update(
            form.id,
            request,
        );

    visible.value = false;

    emit(
        "updated",
        result,
    );

    Message.success(
        "项目设置已保存",
    );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  } finally {
    saving.value =
        false;
  }
}

/**
 * 删除 Project（即删除对应 Agent Aggregate）。
 *
 * 后端会级联删除全部 Session、附件、Session Memory、Agent Profile，以及 Humbert 管理的
 * managed workspace。Custom Workspace 属于用户外部目录，只解除引用，不会删除真实文件。
 */
async function removeProject() {
  if (!form.id) {
    return;
  }

  try {
    const answer =
        await Dialogs.Question({
          Title:
              "删除项目",

          Message:
              `确定删除项目「${form.name}」吗？该项目的全部对话、附件、记忆和 Humbert 管理的 Workspace 会一并删除；如果使用的是自定义外部 Workspace，外部文件不会被删除。`,

          Buttons: [
            {
              Label:
                  "删除",

              IsDefault:
                  false,
            },
            {
              Label:
                  "取消",

              IsDefault:
                  true,
            },
          ],
        });

    if (
        answer !== "删除"
    ) {
      return;
    }

    const deletedID =
        form.id;

    await agentStore.remove(
        deletedID,
    );

    visible.value = false;

    emit(
        "deleted",
        deletedID,
    );

    Message.success(
        "项目已删除",
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
  <a-modal
      v-model:visible="visible"
      :title="modalTitle"
      :width="700"
      :mask-closable="!saving"
      :esc-to-close="!saving"
      modal-class="project-settings-modal"
  >
    <a-tabs
        v-model:active-key="activeTab"
        class="project-settings-tabs"
    >
      <a-tab-pane key="basic" title="基本设置">
        <a-form :model="form" layout="vertical" class="project-tab-form">
          <a-form-item label="项目名称">
            <a-input
                v-model="form.name"
                maxlength="100"
                placeholder="例如：Humbert"
            />
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
                <ModelCapabilityBadges v-if="selectedChatModel" :capabilities="selectedChatModel.capabilities" compact />
              </div>
            </template>
          </a-form-item>

          <div class="model-role-panel">
            <div class="model-role-panel__intro">
              <strong>模型角色</strong>
              <span>留空会自动回退：Utility → Chat，Memory → Utility → Chat；Vision 只在 Chat 无法处理当前图片/文件输入时使用。</span>
            </div>
            <div class="model-role-grid">
              <a-form-item label="Utility 模型">
                <a-select v-model="form.modelRoles.utilityModelID" allow-clear allow-search placeholder="回退 Chat 模型">
                  <a-option
                      v-for="model in roleModelOptions(form.modelRoles.utilityModelID)"
                      :key="model.id" :value="model.id" :disabled="!model.enabled"
                  >
                    {{ model.displayName }} · {{ model.providerName }}{{ model.enabled ? "" : "（已禁用）" }}
                  </a-option>
                </a-select>
                <template #extra>Context 压缩等辅助任务优先使用；窗口不足时 Runtime 会回退当前执行模型。</template>
              </a-form-item>

              <a-form-item label="Memory 模型">
                <a-select v-model="form.modelRoles.memoryModelID" allow-clear allow-search placeholder="回退 Utility / Chat">
                  <a-option
                      v-for="model in roleModelOptions(form.modelRoles.memoryModelID)"
                      :key="model.id" :value="model.id" :disabled="!model.enabled"
                  >
                    {{ model.displayName }} · {{ model.providerName }}{{ model.enabled ? "" : "（已禁用）" }}
                  </a-option>
                </a-select>
                <template #extra>Session Memory 摘要与刷新使用该模型。</template>
              </a-form-item>

              <a-form-item label="Vision 模型">
                <a-select v-model="form.modelRoles.visionModelID" allow-clear allow-search placeholder="仅 Chat 不满足附件能力时使用">
                  <a-option
                      v-for="model in roleModelOptions(form.modelRoles.visionModelID)"
                      :key="model.id" :value="model.id" :disabled="!model.enabled"
                  >
                    {{ model.displayName }} · {{ model.providerName }}{{ model.enabled ? "" : "（已禁用）" }}
                  </a-option>
                </a-select>
                <template #extra>用于补足 Chat 的 Vision / Files 能力；若 Agent 暴露工具，该模型也必须支持 Tools。</template>
              </a-form-item>
            </div>
          </div>

          <a-form-item label="Skills">
            <SkillSelector v-model="form.enabledSkills" />
          </a-form-item>

          <a-form-item label="项目目录">
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
                  <template #icon><IconFolder /></template>
                  {{ form.workspacePath ? "重新选择" : "选择目录" }}
                </a-button>
              </div>

              <div class="workspace-help">
                <template v-if="form.workspaceMode === 'managed'">
                  Humbert 会为这个项目创建独立工作目录。
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
        <div class="project-config-pane">
          <div class="project-config-intro">
            <strong>文件访问与工具</strong>
            <span>
              这里保存的是这个项目自己的 Agent 配置。额外可读取目录会写入 Agent Profile，并在之后的对话中继续生效。
            </span>
          </div>

          <AgentSecurityEditor
              v-model:sandbox="form.sandbox"
              v-model:enabled-builtin-tools="form.enabledBuiltinTools"
              :available-builtin-tools="form.availableBuiltinTools"
              :sandbox-status="form.sandboxStatus"
          />
        </div>
      </a-tab-pane>

      <a-tab-pane key="instruction" title="Agent 指令">
        <div class="instruction-pane">
          <div class="project-config-intro">
            <strong>Agent Instruction</strong>
            <span>定义这个项目中 Agent 的角色、原则、约束和工作方式。</span>
          </div>
          <a-textarea
              v-model="form.instruction"
              :auto-size="{ minRows: 12, maxRows: 18 }"
              placeholder="定义这个项目中 Agent 的角色、原则与行为方式。"
          />
        </div>
      </a-tab-pane>
    </a-tabs>

    <template #footer>
      <div class="project-modal-footer">
        <a-button
            v-if="!creating"
            type="text"
            status="danger"
            :disabled="saving"
            @click="removeProject"
        >
          <template #icon><IconDelete /></template>
          删除项目
        </a-button>
        <span v-else></span>

        <a-space>
          <a-button :disabled="saving" @click="visible = false">取消</a-button>
          <a-button type="primary" :loading="saving" @click="save">
            {{ creating ? "创建项目" : "保存" }}
          </a-button>
        </a-space>
      </div>
    </template>
  </a-modal>
</template>

<style scoped>
.project-settings-tabs {
  min-height: 420px;
}

.project-tab-form,
.project-config-pane,
.instruction-pane {
  padding: 4px 2px 8px;
}

.project-config-pane,
.instruction-pane {
  display: grid;
  gap: 16px;
}

.project-config-intro {
  display: grid;
  gap: 5px;
  padding: 10px 12px;
  border: 1px solid var(--h-border);
  border-radius: 9px;
  background: var(--h-surface-soft, var(--h-surface));
}

.project-config-intro strong {
  color: var(--h-text);
  font-size: 12px;
}

.project-config-intro span {
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.6;
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

.project-modal-footer {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
}

:deep(.project-settings-modal .arco-modal-body) {
  max-height: 72vh;
  overflow-y: auto;
}

@media (max-width: 760px) {
  .model-role-grid {
    grid-template-columns: 1fr;
  }

  .project-settings-tabs {
    min-height: 360px;
  }
}
</style>
