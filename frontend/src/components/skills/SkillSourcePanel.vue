<script setup>
import { computed, ref, toRef } from "vue";
import { IconRefresh } from "@arco-design/web-vue/es/icon";
import { Message } from "../../utils/uiMessage.js";
import { t } from "../../i18n/index.js";
import { confirmAction } from "../../utils/confirm.js";
import {
  checkSkillUpdate,
  updateSkill,
  reinstallSkill,
  reinstallSkillFromURL,
  reinstallSkillFromDirectory,
  selectSkillDirectory,
} from "../../api/skills.js";
const props = defineProps({
  skill: { type: Object, required: true },
  source: { type: Object, required: true },
  displayName: String,
  formatUpdatedAt: { type: Function, required: true },
  refresh: { type: Function, required: true },
});
const source = toRef(props, "source");

const checkingUpdate = ref(false);

const applyingUpdate = ref(false);

const reinstalling = ref(false);

const sourceEditorVisible = ref(false);

const sourceURLDraft = ref("");

const sourceSkillPathDraft = ref("");

const relinkingRemote = ref(false);

const relinkingLocal = ref(false);

const updateCheck = ref(null);

const sourceMutating = computed(
  () =>
    applyingUpdate.value ||
    reinstalling.value ||
    relinkingRemote.value ||
    relinkingLocal.value,
);

const sourceLabel = computed(() => {
  if (!source.value?.known) {
    return "来源未知";
  }

  if (source.value.kind === "local") {
    return "本地目录";
  }

  const provider = String(source.value.provider || "").trim();

  return provider || "远程来源";
});

function shortIdentity(value) {
  const text = String(value || "").trim();

  if (text.length <= 16) {
    return text;
  }

  return `${text.slice(0, 12)}…${text.slice(-6)}`;
}

async function confirmMutation(title, message, actionLabel) {
  return confirmAction({
    title,
    message,
    confirmText: actionLabel,
  });
}

async function checkForUpdate() {
  const name = props.skill?.name;

  if (
    !name ||
    !props.skill?.valid ||
    checkingUpdate.value ||
    sourceMutating.value
  ) {
    return;
  }

  checkingUpdate.value = true;

  try {
    const result = await checkSkillUpdate(name);

    updateCheck.value = result;

    if (result?.updateAvailable) {
      Message.info("检测到新的 Skill Package 内容");
    } else {
      Message.success("当前 Skill 已是记录来源中的最新内容");
    }
  } catch (error) {
    updateCheck.value = null;
    Message.error(error?.message ?? String(error));
  } finally {
    checkingUpdate.value = false;
  }
}

async function applyAvailableUpdate() {
  const name = props.skill?.name;

  if (
    !name ||
    !props.skill?.valid ||
    checkingUpdate.value ||
    sourceMutating.value
  ) {
    return;
  }

  const confirmed = await confirmMutation(
    "更新 Skill",
    `将从已记录来源下载/读取「${props.displayName}」的新内容，完整校验后原子替换本地 Package。Agent 启用状态和本地显示名称都会保留。`,
    "更新",
  );

  if (!confirmed) {
    return;
  }

  applyingUpdate.value = true;

  try {
    const result = await updateSkill(name);

    updateCheck.value = null;
    await props.refresh();

    Message.success(
      result?.changed
        ? "Skill 已更新，下一轮 Runtime Snapshot 将使用新版本"
        : "来源内容没有变化，无需更新",
    );
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    applyingUpdate.value = false;
  }
}

async function forceReinstallFromSource() {
  const name = props.skill?.name;

  if (!name || checkingUpdate.value || sourceMutating.value) {
    return;
  }

  const repairing = !props.skill?.valid;

  const confirmed = await confirmMutation(
    repairing ? "修复 Skill" : "重新安装 Skill",
    repairing
      ? `将从已记录来源重新获取「${props.displayName}」并修复当前无效 Package。修复过程会先完整校验候选内容，再原子替换；Agent 启用状态和本地显示名称都会保留。`
      : `将从已记录来源重新获取「${props.displayName}」并原子替换当前 Package。这个操作不会修改任何 Agent 的 enabled_skills，也不会清除本地显示名称。`,
    repairing ? "修复" : "重新安装",
  );

  if (!confirmed) {
    return;
  }

  reinstalling.value = true;

  try {
    await reinstallSkill(name);

    updateCheck.value = null;
    await props.refresh();

    Message.success(
      props.skill?.valid
        ? "Skill 已从记录来源重新安装"
        : "Skill 已从记录来源修复",
    );
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    reinstalling.value = false;
  }
}

async function relinkFromURL() {
  const name = props.skill?.name;

  const sourceURL = sourceURLDraft.value.trim();

  if (!name || checkingUpdate.value || sourceMutating.value) {
    return;
  }

  if (!sourceURL) {
    Message.warning("请输入新的 Skill URL");
    return;
  }

  const repairing = !props.skill?.valid;

  const confirmed = await confirmMutation(
    repairing ? "从 URL 修复 Skill" : "从 URL 重新安装",
    `Humbert 会先完整验证来源中的 canonical name 必须仍是「${name}」，再原子替换当前 Package 并记录新的更新来源。${repairing ? " 当前无效 Package 只会在候选内容验证通过后被替换。" : ""}`,
    repairing ? "修复" : "重新安装",
  );

  if (!confirmed) {
    return;
  }

  relinkingRemote.value = true;

  try {
    await reinstallSkillFromURL(
      name,
      sourceURL,
      sourceSkillPathDraft.value.trim(),
    );

    sourceURLDraft.value = "";
    sourceSkillPathDraft.value = "";
    sourceEditorVisible.value = false;
    updateCheck.value = null;

    await props.refresh();

    Message.success(
      props.skill?.valid
        ? "Skill 已重新安装并记录远程来源"
        : "Skill 已从远程来源修复并记录来源",
    );
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    relinkingRemote.value = false;
  }
}

async function relinkFromLocalDirectory() {
  const name = props.skill?.name;

  if (!name || checkingUpdate.value || sourceMutating.value) {
    return;
  }

  relinkingLocal.value = true;

  try {
    const selected = await selectSkillDirectory(
      source.value?.localDirectory || "",
    );

    if (!selected) {
      return;
    }

    const repairing = !props.skill?.valid;

    const confirmed = await confirmMutation(
      repairing ? "从本地目录修复 Skill" : "从本地目录重新安装",
      `Humbert 会验证所选目录中的 canonical name 必须仍是「${name}」，再原子替换当前 Package 并记录这个本地目录作为更新来源。${repairing ? " 当前无效 Package 只会在候选内容验证通过后被替换。" : ""}`,
      repairing ? "修复" : "重新安装",
    );

    if (!confirmed) {
      return;
    }

    await reinstallSkillFromDirectory(name, selected);

    sourceEditorVisible.value = false;
    updateCheck.value = null;

    await props.refresh();

    Message.success(
      props.skill?.valid
        ? "Skill 已重新安装并记录本地来源"
        : "Skill 已从本地目录修复并记录来源",
    );
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    relinkingLocal.value = false;
  }
}
</script>
<template>
  <section class="skill-source">
    <header class="skill-source__header">
      <div>
        <div class="skill-detail__section-label">安装与更新来源</div>

        <div class="skill-source__title-row">
          <strong>{{ t(sourceLabel) }}</strong>

          <a-tag
            v-if="source.known"
            size="small"
            :color="
              !skill.valid
                ? 'orange'
                : source.kind === 'remote'
                  ? 'blue'
                  : 'gray'
            "
          >
            {{
              !skill.valid
                ? "可从来源修复"
                : source.kind === "remote"
                  ? "可检查更新"
                  : "本地来源"
            }}
          </a-tag>

          <a-tag v-else size="small" color="orange"> 未记录 </a-tag>
        </div>
      </div>

      <div v-if="source.known" class="skill-source__actions">
        <a-button
          v-if="skill.valid"
          :loading="checkingUpdate"
          :disabled="sourceMutating"
          @click="checkForUpdate"
        >
          <template #icon>
            <IconRefresh />
          </template>
          检查更新
        </a-button>

        <a-button
          v-if="skill.valid && updateCheck?.updateAvailable"
          type="primary"
          :loading="applyingUpdate"
          :disabled="
            checkingUpdate || reinstalling || relinkingRemote || relinkingLocal
          "
          @click="applyAvailableUpdate"
        >
          更新 Skill
        </a-button>

        <a-button
          :type="skill.valid ? 'secondary' : 'primary'"
          :loading="reinstalling"
          :disabled="
            checkingUpdate ||
            applyingUpdate ||
            relinkingRemote ||
            relinkingLocal
          "
          @click="forceReinstallFromSource"
        >
          {{ skill.valid ? "重新安装" : "从来源修复" }}
        </a-button>

        <a-button
          type="text"
          @click="sourceEditorVisible = !sourceEditorVisible"
        >
          {{
            sourceEditorVisible
              ? "收起来源设置"
              : skill.valid
                ? "更换来源"
                : "更换修复来源"
          }}
        </a-button>
      </div>
    </header>

    <template v-if="source.known">
      <div class="skill-source__grid">
        <div
          v-if="source.displayUrl"
          class="skill-source__field skill-source__field--wide"
        >
          <span>来源地址</span>
          <code>{{ source.displayUrl }}</code>
        </div>

        <div
          v-if="source.localDirectory"
          class="skill-source__field skill-source__field--wide"
        >
          <span>本地目录</span>
          <code>{{ source.localDirectory }}</code>
        </div>

        <div v-if="source.repository" class="skill-source__field">
          <span>仓库</span>
          <strong>{{ source.repository }}</strong>
        </div>

        <div v-if="source.ref" class="skill-source__field">
          <span>Ref</span>
          <code>{{ source.ref }}</code>
        </div>

        <div v-if="source.skillPath" class="skill-source__field">
          <span>Skill 子目录</span>
          <code>{{ source.skillPath }}</code>
        </div>

        <div class="skill-source__field">
          <span>来源记录 Identity</span>
          <code>{{ shortIdentity(source.recordedIdentity) }}</code>
        </div>

        <div v-if="source.installedAt" class="skill-source__field">
          <span>首次记录</span>
          <strong>{{ formatUpdatedAt(source.installedAt) }}</strong>
        </div>

        <div v-if="source.updatedAt" class="skill-source__field">
          <span>最近更新</span>
          <strong>{{ formatUpdatedAt(source.updatedAt) }}</strong>
        </div>
      </div>

      <a-alert
        v-if="skill.valid && source.drifted"
        type="warning"
        :show-icon="true"
        class="skill-source__alert"
      >
        当前 Package Identity 与 Humbert
        上次从来源安装时不同。可能是你手工修改了本地
        Skill；可以先检查更新，或使用“重新安装”恢复到记录来源。
      </a-alert>

      <a-alert
        v-if="skill.valid && updateCheck"
        :type="updateCheck.updateAvailable ? 'info' : 'success'"
        :show-icon="true"
        class="skill-source__alert"
      >
        <template v-if="updateCheck.updateAvailable">
          发现新的 Package 内容：{{
            shortIdentity(updateCheck.currentIdentity)
          }}
          → {{ shortIdentity(updateCheck.candidateIdentity) }}。点击“更新
          Skill”后才会原子替换当前安装。
        </template>

        <template v-else>
          已检查记录来源，当前 Package 内容没有更新。
        </template>
      </a-alert>
    </template>

    <a-alert
      v-else
      type="warning"
      :show-icon="true"
      class="skill-source__alert"
    >
      {{
        skill.valid
          ? "这个 Skill 没有可复用的安装来源，通常是旧版本 Humbert 安装或手工复制的 Package。它仍可正常使用；重新选择 URL 或本地目录后，Humbert 才能自动检查和更新。"
          : "这个无效 Skill 没有可复用的安装来源。请重新选择 URL 或本地 Skill 目录；Humbert 会先完整验证候选 Package，再原子替换当前损坏内容。"
      }}
    </a-alert>

    <div
      v-if="!source.known || sourceEditorVisible"
      class="skill-source__editor"
    >
      <div class="skill-source__editor-title">
        {{
          !skill.valid
            ? source.known
              ? "更换修复来源"
              : "选择修复来源"
            : source.known
              ? "更换来源并重新安装"
              : "建立更新来源"
        }}
      </div>

      <div class="skill-source__remote-row">
        <a-input
          v-model="sourceURLDraft"
          allow-clear
          placeholder="Skill URL：GitHub / GitLab / Gitee / skills.sh / HTTPS ZIP"
          @press-enter="relinkFromURL"
        />

        <a-button
          type="primary"
          :loading="relinkingRemote"
          :disabled="
            checkingUpdate || applyingUpdate || reinstalling || relinkingLocal
          "
          @click="relinkFromURL"
        >
          {{ skill.valid ? "从 URL 重新安装" : "从 URL 修复" }}
        </a-button>
      </div>

      <div class="skill-source__local-row">
        <a-input
          v-model="sourceSkillPathDraft"
          allow-clear
          class="skill-source__path-input"
          placeholder="可选：归档中的 Skill 子目录，例如 skills/frontend-design"
        />

        <a-button
          :loading="relinkingLocal"
          :disabled="
            checkingUpdate || applyingUpdate || reinstalling || relinkingRemote
          "
          @click="relinkFromLocalDirectory"
        >
          {{ skill.valid ? "从Skills目录安装" : "从本地目录修复" }}
        </a-button>
      </div>

      <p>或者从本地目录重新安装并记录来源。</p>
      <p>
        Humbert 会先完整验证 canonical name 必须仍是「{{
          skill.name
        }}」。来源不匹配、下载失败或 Package 校验失败时，{{
          skill.valid ? "当前安装" : "当前损坏 Package"
        }}
        保持不变。
      </p>
    </div>
  </section>
</template>
<style scoped>
.skill-source {
  display: flex;
  flex-direction: column;
  gap: 12px;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-lg);
  background: var(--h-surface);
  padding: 16px;
}

.skill-source__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 14px;
}

.skill-source__title-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 7px;
}

.skill-source__title-row strong {
  color: var(--h-text);
  font-size: 12px;
}

.skill-source__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 7px;
}

.skill-source__grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
}

.skill-source__field {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 4px;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-sm);
  background: var(--h-bg);
  padding: 9px 10px;
}

.skill-source__field--wide {
  grid-column: 1 / -1;
}

.skill-source__field span {
  color: var(--h-text-muted);
  font-size: 8px;
}

.skill-source__field strong,
.skill-source__field code {
  overflow: hidden;
  color: var(--h-text-secondary);
  font-size: 10px;
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.skill-source__field code {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.skill-source__alert {
  margin-top: 1px;
}

.skill-source__editor {
  display: flex;
  flex-direction: column;
  gap: 9px;
  border-top: 1px solid var(--h-border);
  padding-top: 12px;
}

.skill-source__editor-title {
  color: var(--h-text);
  font-size: 11px;
  font-weight: 600;
}

.skill-source__remote-row,
.skill-source__local-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.skill-source__remote-row :deep(.arco-input-wrapper) {
  min-width: 0;
  flex: 1;
}

.skill-source__path-input {
  max-width: 653px;
}

.skill-source__local-row {
  justify-content: space-between;
  color: var(--h-text-muted);
  font-size: 10px;
}

.skill-source__editor p {
  margin: 0;
  color: var(--h-text-muted);
  font-size: 8px;
  line-height: 1.6;
}

@media (max-width: 980px) {
  .skill-source__header,
  .skill-source__remote-row,
  .skill-source__local-row {
    align-items: stretch;
    flex-direction: column;
  }

  .skill-source__actions {
    justify-content: flex-start;
  }

  .skill-source__grid {
    grid-template-columns: 1fr;
  }

  .skill-source__field--wide {
    grid-column: auto;
  }
}
.skill-detail__section-label {
  margin-bottom: 9px;
  color: var(--h-text-muted);
  font-size: 10px;
}
</style>
