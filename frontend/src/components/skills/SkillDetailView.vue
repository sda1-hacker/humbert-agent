<script setup>
import SkillFileTree from "./SkillFileTree.vue";
import SkillSourcePanel from "./SkillSourcePanel.vue";
import { useSkillFiles } from "../../composables/useSkillFiles.js";

import { computed, ref, watch } from "vue";

import { Message } from "../../utils/uiMessage.js";
import { t, formatDate } from "../../i18n/index.js";

import { confirmAction } from "../../utils/confirm.js";

import { IconDelete, IconEdit } from "@arco-design/web-vue/es/icon";

import { deleteSkill, setSkillAlias } from "../../api/skills.js";

const props = defineProps({
  skill: {
    type: Object,
    required: true,
  },
});

const emit = defineEmits(["back", "deleted", "updated"]);

const editingAlias = ref(false);

const aliasDraft = ref("");

const savingAlias = ref(false);

const deleting = ref(false);

const displayName = computed(
  () =>
    props.skill?.alias?.trim() ||
    props.skill?.name ||
    props.skill?.directoryName ||
    t("未命名 Skill"),
);

const usedByAgents = computed(() =>
  Array.isArray(props.skill?.usedByAgents) ? props.skill.usedByAgents : [],
);

const diagnostics = computed(() =>
  Array.isArray(detail.value?.diagnostics)
    ? detail.value.diagnostics
    : Array.isArray(props.skill?.diagnostics)
      ? props.skill.diagnostics
      : [],
);

const scriptRuntimes = computed(() =>
  Array.isArray(detail.value?.scriptRuntimes)
    ? detail.value.scriptRuntimes
    : Array.isArray(props.skill?.scriptRuntimes)
      ? props.skill.scriptRuntimes
      : [],
);

const skillStatusLabel = computed(() => {
  if (!props.skill?.valid) return "无效";
  if (props.skill?.runtimeStatus === "unsupported")
    return "已安装 · 暂不可启用";
  if (props.skill?.runtimeStatus === "needs_setup") return "已安装 · 需要配置";
  if (props.skill?.specStatus === "legacy") return "可用 · 兼容模式";
  return "可用";
});

const skillStatusColor = computed(() => {
  if (!props.skill?.valid) return "red";
  if (["unsupported", "needs_setup"].includes(props.skill?.runtimeStatus))
    return "orange";
  return "green";
});

const source = computed(() => {
  const value = detail.value?.source ?? props.skill?.source;

  return value && typeof value === "object" ? value : { known: false };
});

function formatUpdatedAt(value) {
  if (!value) {
    return "";
  }

  const date = new Date(value);

  return Number.isNaN(date.getTime())
    ? value
    : formatDate(date, { dateStyle: "short", timeStyle: "short" });
}

function beginAliasEdit() {
  aliasDraft.value = props.skill?.alias ?? "";

  editingAlias.value = true;
}

async function saveAlias() {
  const name = props.skill?.name;

  if (!name || savingAlias.value) {
    return;
  }

  savingAlias.value = true;

  try {
    await setSkillAlias(name, aliasDraft.value.trim());

    editingAlias.value = false;

    emit("updated");

    Message.success("显示名称已保存");
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    savingAlias.value = false;
  }
}

async function removeSkill() {
  const name = props.skill?.name || props.skill?.directoryName;

  if (!name || deleting.value) {
    return;
  }

  if (usedByAgents.value.length > 0) {
    Message.warning(
      `请先为 ${usedByAgents.value.length} 个正在使用该 Skill 的 Agent 关闭开关`,
    );

    return;
  }

  const confirmed = await confirmAction({
    title: "删除 Skill",
    message: `确定删除 Skill「${displayName.value}」吗？此操作只删除本地安装包。`,
    confirmText: "删除",
    danger: true,
  });

  if (!confirmed) {
    return;
  }

  deleting.value = true;

  try {
    await deleteSkill(name);

    Message.success(`Skill「${displayName.value}」已删除`);

    emit("deleted");
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    deleting.value = false;
  }
}

const {
  detail,
  loading,
  detailError,
  selectedPath,
  fileContent,
  fileLoading,
  fileError,
  loadFile,
  loadDetail: readDetail,
} = useSkillFiles(() => props.skill);
async function loadDetail() {
  editingAlias.value = false;
  aliasDraft.value = props.skill?.alias ?? "";
  await readDetail();
}
async function refreshAfterMutation() {
  await loadDetail();
  emit("updated");
}

watch(() => [props.skill?.name, props.skill?.valid], loadDetail, {
  immediate: true,
});
</script>
<template>
  <section class="skill-detail">
    <header class="skill-detail__topbar">
      <button type="button" class="skill-detail__back" @click="emit('back')">
        <span aria-hidden="true">‹</span>
        返回技能列表
      </button>

      <div class="skill-detail__top-actions">
        <a-button
          status="danger"
          :disabled="usedByAgents.length > 0"
          :loading="deleting"
          @click="removeSkill"
        >
          <template #icon>
            <IconDelete />
          </template>
          删除 Skill
        </a-button>
      </div>
    </header>

    <section class="skill-detail__hero">
      <div class="skill-detail__identity">
        <div class="skill-detail__eyebrow">Skill 详情</div>

        <div class="skill-detail__title-row">
          <h1>{{ displayName }}</h1>

          <a-tag :color="skillStatusColor" size="small">
            {{ t(skillStatusLabel) }}
          </a-tag>
        </div>

        <code v-if="skill.alias" class="skill-detail__canonical">
          {{ skill.name }}
        </code>

        <p>
          {{
            skill.valid
              ? skill.description
              : skill.error || "Skill Package 校验失败"
          }}
        </p>
        <p
          v-if="
            skill.valid &&
            ['unsupported', 'needs_setup'].includes(skill.runtimeStatus)
          "
          class="skill-detail__runtime-warning"
        >
          {{
            skill.runtimeMessage ||
            (skill.runtimeStatus === "needs_setup"
              ? "这个 Skill 可以启用，但执行部分能力前需要补充运行环境。"
              : "这个 Skill 已安装，但当前 Humbert Runtime 暂不支持启用。")
          }}
        </p>
      </div>

      <div class="skill-detail__alias">
        <div class="skill-detail__section-label">本地显示名称</div>

        <div v-if="!editingAlias" class="skill-detail__alias-view">
          <div>
            <strong>{{ displayName }}</strong>
            <span> 原始名称：{{ skill.name }} </span>
          </div>

          <a-button v-if="skill.valid" type="text" @click="beginAliasEdit">
            <template #icon>
              <IconEdit />
            </template>
            修改显示名称
          </a-button>
        </div>

        <div v-else class="skill-detail__alias-edit">
          <a-input
            v-model="aliasDraft"
            allow-clear
            :max-length="80"
            show-word-limit
            placeholder="例如：前端设计"
          />

          <div class="skill-detail__alias-actions">
            <a-button :disabled="savingAlias" @click="editingAlias = false">
              取消
            </a-button>

            <a-button type="primary" :loading="savingAlias" @click="saveAlias">
              保存
            </a-button>
          </div>
        </div>

        <p class="skill-detail__alias-help">
          这里只修改 Humbert 的本地显示名称，不会改写第三方 SKILL.md，也不会改变
          Agent.config.json 中的 canonical skill name。
        </p>
      </div>
    </section>

    <section v-if="skill.valid && detail" class="skill-compatibility">
      <div class="skill-compatibility__summary">
        <div>
          <div class="skill-detail__section-label">兼容与运行环境</div>
          <strong>{{
            detail.specStatus === "legacy"
              ? "Humbert 兼容模式"
              : "Agent Skills 标准"
          }}</strong>
          <span v-if="detail.specMessage">{{ detail.specMessage }}</span>
          <span v-else-if="detail.compatibility">{{
            detail.compatibility
          }}</span>
          <span v-else>安装与 Runtime 兼容状态已通过本地诊断。</span>
        </div>
        <div class="skill-compatibility__tags">
          <a-tag v-if="detail.license" size="small">{{ detail.license }}</a-tag>
          <a-tag v-if="detail.allowedTools" color="blue" size="small"
            >声明 tools</a-tag
          >
          <a-tag v-if="scriptRuntimes.length > 0" color="orange" size="small">
            {{ scriptRuntimes.length }} 个 script
          </a-tag>
        </div>
      </div>

      <div v-if="detail.allowedTools" class="skill-compatibility__row">
        <span>Skill 声明的 allowed-tools</span>
        <code>{{ detail.allowedTools }}</code>
        <small
          >仅作为兼容提示，不会绕过 Agent Tool 开关、Permission、Approval 或
          Sandbox。</small
        >
      </div>

      <div
        v-if="detail.metadata && Object.keys(detail.metadata).length > 0"
        class="skill-compatibility__row"
      >
        <span>metadata</span>
        <div class="skill-metadata">
          <span v-for="(value, key) in detail.metadata" :key="key">
            <code>{{ key }}</code
            >=<code>{{ value }}</code>
          </span>
        </div>
      </div>

      <div v-if="diagnostics.length > 0" class="skill-diagnostics">
        <div
          v-for="item in diagnostics"
          :key="`${item.code}:${item.message}`"
          class="skill-diagnostics__item"
          :class="`skill-diagnostics__item--${item.level || 'info'}`"
        >
          <strong>{{
            item.level === "error"
              ? "阻止启用"
              : item.level === "warning"
                ? "需要注意"
                : "说明"
          }}</strong>
          <span>{{ item.message }}</span>
        </div>
      </div>

      <div v-if="scriptRuntimes.length > 0" class="skill-script-runtimes">
        <div
          v-for="runtime in scriptRuntimes"
          :key="runtime.path"
          class="skill-script-runtimes__item"
        >
          <div>
            <code>{{ runtime.path }}</code>
            <span>{{ runtime.language || "未知脚本" }}</span>
          </div>
          <a-tag
            size="small"
            :color="runtime.supported && runtime.available ? 'green' : 'orange'"
          >
            {{
              runtime.supported && runtime.available
                ? `可通过 ${runtime.command} 运行`
                : runtime.message || "需要配置"
            }}
          </a-tag>
        </div>
        <p>
          脚本只会通过 <code>run_skill_script</code> 在当前 Workspace
          的临时副本中执行，并继续受只读命令沙盒、Permission 与 Approval 约束。
        </p>
      </div>
    </section>

    <section v-if="usedByAgents.length > 0" class="skill-detail__usage">
      <div>
        <strong> 当前已被 {{ usedByAgents.length }} 个 Agent 启用 </strong>
        <span> 删除前需要先回到技能列表，为对应 Agent 关闭这个 Skill。 </span>
      </div>

      <div class="skill-detail__usage-tags">
        <a-tag v-for="agent in usedByAgents" :key="agent.id" size="small">
          {{ agent.name || agent.id }}
        </a-tag>
      </div>
    </section>

    <SkillSourcePanel
      v-if="(skill.valid && detail) || !skill.valid"
      :skill="skill"
      :source="source"
      :display-name="displayName"
      :format-updated-at="formatUpdatedAt"
      :refresh="refreshAfterMutation"
    />

    <a-alert v-if="!skill.valid" type="error" :show-icon="true">
      {{ skill.error || "Skill Package 当前无效，无法浏览文件内容。" }}
      {{
        source.known
          ? " 可以在上方使用“从来源修复”。"
          : " 可以在上方重新选择 URL 或本地目录进行修复。"
      }}
    </a-alert>

    <a-spin v-else :loading="loading" class="skill-detail__spin">
      <a-alert v-if="detailError" type="error" :show-icon="true">
        {{ detailError }}
      </a-alert>

      <div v-else-if="detail" class="skill-detail__workspace">
        <SkillFileTree
          :detail="detail"
          :selected-path="selectedPath"
          @select="loadFile"
        />

        <section class="skill-preview">
          <header class="skill-preview__header">
            <div>
              <strong>
                {{ selectedPath || "选择一个文件" }}
              </strong>

              <span v-if="detail.updatedAt">
                Package 更新于 {{ formatUpdatedAt(detail.updatedAt) }}
              </span>
            </div>

            <a-tag
              v-if="selectedPath.startsWith('scripts/')"
              color="orange"
              size="small"
            >
              预览只读 · 执行需 run_skill_script
            </a-tag>
          </header>

          <a-spin :loading="fileLoading" class="skill-preview__body">
            <div v-if="fileError" class="skill-preview__empty">
              {{ fileError }}
            </div>

            <pre
              v-else-if="selectedPath"
              class="skill-preview__code"
            ><code>{{ fileContent }}</code></pre>

            <div v-else class="skill-preview__empty">
              从左侧选择 SKILL.md、references 或 scripts 文件查看内容。
            </div>
          </a-spin>
        </section>
      </div>
    </a-spin>
  </section>
</template>
<style scoped>
.skill-detail {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 16px;
}

.skill-detail__topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.skill-detail__back {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--h-accent-hover);
  cursor: pointer;
  font: inherit;
  font-size: 11px;
}

.skill-detail__back span {
  font-size: 20px;
  line-height: 1;
}

.skill-detail__hero {
  display: grid;
  grid-template-columns: minmax(0, 1.45fr) minmax(300px, 0.8fr);
  gap: 22px;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-lg);
  background: var(--h-surface);
  padding: 22px;
}

.skill-detail__eyebrow {
  margin-bottom: 7px;
  color: var(--h-text-muted);
  font-size: 10px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.skill-detail__title-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 9px;
}

.skill-detail__title-row h1 {
  margin: 0;
  color: var(--h-text);
  font-size: 26px;
  font-weight: 650;
}

.skill-detail__canonical {
  display: inline-block;
  margin-top: 5px;
  color: var(--h-text-muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 11px;
}

.skill-detail__identity p {
  max-width: 860px;
  margin: 12px 0 0;
  color: var(--h-text-secondary);
  font-size: 12px;
  line-height: 1.75;
}

.skill-detail__identity .skill-detail__runtime-warning {
  color: rgb(var(--orange-7));
}

.skill-detail__alias {
  border-left: 1px solid var(--h-border);
  padding-left: 22px;
}

.skill-detail__section-label {
  margin-bottom: 9px;
  color: var(--h-text-muted);
  font-size: 10px;
}

.skill-detail__alias-view {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.skill-detail__alias-view > div {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 4px;
}

.skill-detail__alias-view strong {
  color: var(--h-text);
  font-size: 12px;
}

.skill-detail__alias-view span {
  overflow: hidden;
  color: var(--h-text-muted);
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.skill-detail__alias-edit {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.skill-detail__alias-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.skill-detail__alias-help {
  margin: 10px 0 0;
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.6;
}

.skill-compatibility {
  display: flex;
  flex-direction: column;
  gap: 12px;
  border: 1px solid var(--h-border);
  border-radius: 12px;
  background: var(--h-surface);
  padding: 14px 16px;
}

.skill-compatibility__summary {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.skill-compatibility__summary > div:first-child {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 4px;
}

.skill-compatibility__summary strong {
  color: var(--h-text);
  font-size: 12px;
}

.skill-compatibility__summary span,
.skill-compatibility__row small,
.skill-script-runtimes p {
  color: var(--h-text-muted);
  font-size: 9px;
  line-height: 1.55;
}

.skill-compatibility__tags {
  display: flex;
  flex: 0 0 auto;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 6px;
}

.skill-compatibility__row {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 4px;
  border-top: 1px solid var(--h-border);
  padding-top: 10px;
}

.skill-compatibility__row > span {
  color: var(--h-text-secondary);
  font-size: 9px;
}

.skill-compatibility__row code {
  overflow-wrap: anywhere;
  color: var(--h-text);
  font-size: 9px;
}

.skill-metadata {
  display: flex;
  flex-wrap: wrap;
  gap: 7px 12px;
  color: var(--h-text-muted);
  font-size: 9px;
}

.skill-diagnostics,
.skill-script-runtimes {
  display: flex;
  flex-direction: column;
  gap: 7px;
  border-top: 1px solid var(--h-border);
  padding-top: 10px;
}

.skill-diagnostics__item {
  display: grid;
  grid-template-columns: 70px minmax(0, 1fr);
  gap: 8px;
  font-size: 9px;
  line-height: 1.5;
}

.skill-diagnostics__item strong {
  color: var(--h-text-secondary);
}

.skill-diagnostics__item span {
  color: var(--h-text-muted);
}

.skill-diagnostics__item--warning strong,
.skill-diagnostics__item--error strong {
  color: var(--h-warning, #b7791f);
}

.skill-diagnostics__item--error span {
  color: var(--h-danger);
}

.skill-script-runtimes__item {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.skill-script-runtimes__item > div {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 2px;
}

.skill-script-runtimes__item code {
  overflow: hidden;
  color: var(--h-text);
  font-size: 9px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.skill-script-runtimes__item span {
  color: var(--h-text-muted);
  font-size: 8px;
}

.skill-script-runtimes p {
  margin: 1px 0 0;
}

.skill-detail__usage {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 10px 18px;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-md);
  background: var(--h-surface);
  padding: 12px 14px;
}

.skill-detail__usage > div:first-child {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 3px;
}

.skill-detail__usage strong {
  color: var(--h-text);
  font-size: 11px;
}

.skill-detail__usage span {
  color: var(--h-text-muted);
  font-size: 10px;
}

.skill-detail__usage-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.skill-detail__spin {
  display: block;
  width: 100%;
}

.skill-detail__workspace {
  display: grid;
  min-height: 620px;
  grid-template-columns: minmax(230px, 300px) minmax(0, 1fr);
  overflow: hidden;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-lg);
  background: var(--h-surface);
}

.skill-preview {
  display: flex;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
  background: var(--h-surface);
}

.skill-preview__header {
  display: flex;
  min-height: 54px;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--h-border);
}

.skill-preview__header > div {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 2px;
}

.skill-preview__header strong {
  overflow: hidden;
  color: var(--h-text);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.skill-preview__header span {
  color: var(--h-text-muted);
  font-size: 8px;
}

.skill-preview__body {
  display: block;
  min-height: 0;
  flex: 1;
}

.skill-preview__body :deep(.arco-spin-children) {
  height: 100%;
}

.skill-preview__code {
  min-height: 100%;
  margin: 0;
  overflow: auto;
  padding: 22px 24px 60px;
  color: var(--h-text);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
  line-height: 1.7;
  tab-size: 2;
  white-space: pre;
}

.skill-preview__empty {
  display: grid;
  min-height: 420px;
  place-items: center;
  padding: 24px;
  color: var(--h-text-muted);
  font-size: 10px;
  text-align: center;
}

@media (max-width: 980px) {
  .skill-detail__hero,
  .skill-detail__workspace {
    grid-template-columns: 1fr;
  }

  .skill-detail__alias {
    border-top: 1px solid var(--h-border);
    border-left: 0;
    padding-top: 18px;
    padding-left: 0;
  }
}
</style>
