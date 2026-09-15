<script setup>
import {
  computed,
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
  IconEdit,
  IconFolder,
  IconRefresh,
} from "@arco-design/web-vue/es/icon";

import {
  checkSkillUpdate,
  deleteSkill,
  getSkillDetail,
  readSkillFile,
  reinstallSkill,
  reinstallSkillFromDirectory,
  reinstallSkillFromURL,
  selectSkillDirectory,
  setSkillAlias,
  updateSkill,
} from "../../api/skills.js";

const props =
    defineProps({
      skill: {
        type: Object,
        required: true,
      },
    });

const emit =
    defineEmits([
      "back",
      "deleted",
      "updated",
    ]);

const loading =
    ref(false);

const detail =
    ref(null);

const detailError =
    ref("");

const selectedPath =
    ref("");

const fileContent =
    ref("");

const fileLoading =
    ref(false);

const fileError =
    ref("");

const expandedFolders =
    ref(new Set());

const editingAlias =
    ref(false);

const aliasDraft =
    ref("");

const savingAlias =
    ref(false);

const deleting =
    ref(false);

const checkingUpdate =
    ref(false);

const applyingUpdate =
    ref(false);

const reinstalling =
    ref(false);

const sourceEditorVisible =
    ref(false);

const sourceURLDraft =
    ref("");

const sourceSkillPathDraft =
    ref("");

const relinkingRemote =
    ref(false);

const relinkingLocal =
    ref(false);

const updateCheck =
    ref(null);

const sourceMutating =
    computed(() => (
        applyingUpdate.value ||
        reinstalling.value ||
        relinkingRemote.value ||
        relinkingLocal.value
    ));

const displayName =
    computed(() => (
        props.skill?.alias?.trim() ||
        props.skill?.name ||
        props.skill?.directoryName ||
        "未命名 Skill"
    ));

const usedByAgents =
    computed(() => (
        Array.isArray(
            props.skill?.usedByAgents,
        )
            ? props.skill.usedByAgents
            : []
    ));

const files =
    computed(() => (
        Array.isArray(detail.value?.files)
            ? detail.value.files
            : []
    ));

const diagnostics =
    computed(() => (
        Array.isArray(detail.value?.diagnostics)
            ? detail.value.diagnostics
            : (Array.isArray(props.skill?.diagnostics) ? props.skill.diagnostics : [])
    ));

const scriptRuntimes =
    computed(() => (
        Array.isArray(detail.value?.scriptRuntimes)
            ? detail.value.scriptRuntimes
            : (Array.isArray(props.skill?.scriptRuntimes) ? props.skill.scriptRuntimes : [])
    ));

const skillStatusLabel =
    computed(() => {
      if (!props.skill?.valid) return "无效";
      if (props.skill?.runtimeStatus === "unsupported") return "已安装 · 暂不可启用";
      if (props.skill?.runtimeStatus === "needs_setup") return "已安装 · 需要配置";
      if (props.skill?.specStatus === "legacy") return "可用 · 兼容模式";
      return "可用";
    });

const skillStatusColor =
    computed(() => {
      if (!props.skill?.valid) return "red";
      if (["unsupported", "needs_setup"].includes(props.skill?.runtimeStatus)) return "orange";
      return "green";
    });

const source =
    computed(() => {
      const value =
          detail.value?.source ??
          props.skill?.source;

      return value &&
      typeof value === "object"
          ? value
          : { known: false };
    });

const sourceLabel =
    computed(() => {
      if (!source.value?.known) {
        return "来源未知";
      }

      if (source.value.kind === "local") {
        return "本地目录";
      }

      const provider =
          String(source.value.provider || "")
              .trim();

      return provider || "远程来源";
    });

const treeRows =
    computed(() => {
      const folders =
          new Set();

      const rows = [];

      for (const file of files.value) {
        const path =
            typeof file?.path ===
            "string"
                ? file.path
                : "";

        if (!path) {
          continue;
        }

        const parts =
            path.split("/");

        for (
            let index = 1;
            index < parts.length;
            index += 1
        ) {
          folders.add(
              parts
                  .slice(0, index)
                  .join("/"),
          );
        }
      }

      for (const folder of folders) {
        rows.push({
          path:
          folder,

          name:
              folder
                  .split("/")
                  .slice(-1)[0],

          folder:
              true,

          level:
              folder
                  .split("/")
                  .length - 1,

          text:
              false,

          sizeBytes:
              0,
        });
      }

      for (const file of files.value) {
        const path =
            typeof file?.path ===
            "string"
                ? file.path
                : "";

        if (!path) {
          continue;
        }

        rows.push({
          path,

          name:
              path
                  .split("/")
                  .slice(-1)[0],

          folder:
              false,

          level:
              path
                  .split("/")
                  .length - 1,

          text:
              Boolean(file?.text),

          sizeBytes:
              Number(file?.sizeBytes) ||
              0,
        });
      }

      rows.sort(
          (left, right) => {
            if (
                left.path ===
                "SKILL.md"
            ) {
              return -1;
            }

            if (
                right.path ===
                "SKILL.md"
            ) {
              return 1;
            }

            return left.path.localeCompare(
                right.path,
            );
          },
      );

      return rows.filter(
          (row) => {
            const parts =
                row.path.split("/");

            if (parts.length <= 1) {
              return true;
            }

            for (
                let index = 1;
                index < parts.length;
                index += 1
            ) {
              const parent =
                  parts
                      .slice(0, index)
                      .join("/");

              if (
                  !expandedFolders.value
                      .has(parent)
              ) {
                return false;
              }
            }

            return true;
          },
      );
    });

function formatBytes(value) {
  const bytes =
      Number(value) || 0;

  if (bytes < 1024) {
    return `${bytes} B`;
  }

  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KiB`;
  }

  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`;
}

function formatUpdatedAt(value) {
  if (!value) {
    return "";
  }

  const date =
      new Date(value);

  return Number.isNaN(
      date.getTime(),
  )
      ? value
      : date.toLocaleString();
}

function shortIdentity(value) {
  const text =
      String(value || "").trim();

  if (text.length <= 16) {
    return text;
  }

  return `${text.slice(0, 12)}…${text.slice(-6)}`;
}

async function confirmMutation(title, message, actionLabel) {
  const answer =
      await Dialogs.Question({
        Title: title,
        Message: message,
        Buttons: [
          {
            Label: actionLabel,
            IsDefault: false,
          },
          {
            Label: "取消",
            IsDefault: true,
          },
        ],
      });

  return answer === actionLabel;
}

function toggleFolder(path) {
  const next =
      new Set(
          expandedFolders.value,
      );

  if (next.has(path)) {
    next.delete(path);
  } else {
    next.add(path);
  }

  expandedFolders.value = next;
}

function beginAliasEdit() {
  aliasDraft.value =
      props.skill?.alias ?? "";

  editingAlias.value = true;
}

async function saveAlias() {
  const name =
      props.skill?.name;

  if (
      !name ||
      savingAlias.value
  ) {
    return;
  }

  savingAlias.value = true;

  try {
    await setSkillAlias(
        name,
        aliasDraft.value.trim(),
    );

    editingAlias.value = false;

    emit("updated");

    Message.success(
        "显示名称已保存",
    );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  } finally {
    savingAlias.value = false;
  }
}

async function removeSkill() {
  const name =
      props.skill?.name ||
      props.skill?.directoryName;

  if (
      !name ||
      deleting.value
  ) {
    return;
  }

  if (
      usedByAgents.value.length > 0
  ) {
    Message.warning(
        `请先为 ${usedByAgents.value.length} 个正在使用该 Skill 的 Agent 关闭开关`,
    );

    return;
  }

  const answer =
      await Dialogs.Question({
        Title:
            "删除 Skill",

        Message:
            `确定删除 Skill「${displayName.value}」吗？此操作只删除本地安装包。`,

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

  if (answer !== "删除") {
    return;
  }

  deleting.value = true;

  try {
    await deleteSkill(name);

    Message.success(
        `Skill「${displayName.value}」已删除`,
    );

    emit("deleted");
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  } finally {
    deleting.value = false;
  }
}

async function checkForUpdate() {
  const name =
      props.skill?.name;

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
    const result =
        await checkSkillUpdate(name);

    updateCheck.value = result;

    if (result?.updateAvailable) {
      Message.info(
          "检测到新的 Skill Package 内容",
      );
    } else {
      Message.success(
          "当前 Skill 已是记录来源中的最新内容",
      );
    }
  } catch (error) {
    updateCheck.value = null;
    Message.error(
        error?.message ??
        String(error),
    );
  } finally {
    checkingUpdate.value = false;
  }
}

async function applyAvailableUpdate() {
  const name =
      props.skill?.name;

  if (
      !name ||
      !props.skill?.valid ||
      checkingUpdate.value ||
      sourceMutating.value
  ) {
    return;
  }

  const confirmed =
      await confirmMutation(
          "更新 Skill",
          `将从已记录来源下载/读取「${displayName.value}」的新内容，完整校验后原子替换本地 Package。Agent 启用状态和本地显示名称都会保留。`,
          "更新",
      );

  if (!confirmed) {
    return;
  }

  applyingUpdate.value = true;

  try {
    const result =
        await updateSkill(name);

    updateCheck.value = null;
    await loadDetail();
    emit("updated");

    Message.success(
        result?.changed
            ? "Skill 已更新，下一轮 Runtime Snapshot 将使用新版本"
            : "来源内容没有变化，无需更新",
    );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  } finally {
    applyingUpdate.value = false;
  }
}

async function forceReinstallFromSource() {
  const name =
      props.skill?.name;

  if (
      !name ||
      checkingUpdate.value ||
      sourceMutating.value
  ) {
    return;
  }

  const repairing =
      !props.skill?.valid;

  const confirmed =
      await confirmMutation(
          repairing ? "修复 Skill" : "重新安装 Skill",
          repairing
              ? `将从已记录来源重新获取「${displayName.value}」并修复当前无效 Package。修复过程会先完整校验候选内容，再原子替换；Agent 启用状态和本地显示名称都会保留。`
              : `将从已记录来源重新获取「${displayName.value}」并原子替换当前 Package。这个操作不会修改任何 Agent 的 enabled_skills，也不会清除本地显示名称。`,
          repairing ? "修复" : "重新安装",
      );

  if (!confirmed) {
    return;
  }

  reinstalling.value = true;

  try {
    await reinstallSkill(name);

    updateCheck.value = null;
    await loadDetail();
    emit("updated");

    Message.success(
        props.skill?.valid
            ? "Skill 已从记录来源重新安装"
            : "Skill 已从记录来源修复",
    );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  } finally {
    reinstalling.value = false;
  }
}

async function relinkFromURL() {
  const name =
      props.skill?.name;

  const sourceURL =
      sourceURLDraft.value.trim();

  if (
      !name ||
      checkingUpdate.value ||
      sourceMutating.value
  ) {
    return;
  }

  if (!sourceURL) {
    Message.warning(
        "请输入新的 Skill URL",
    );
    return;
  }

  const repairing =
      !props.skill?.valid;

  const confirmed =
      await confirmMutation(
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

    await loadDetail();
    emit("updated");

    Message.success(
        props.skill?.valid
            ? "Skill 已重新安装并记录远程来源"
            : "Skill 已从远程来源修复并记录来源",
    );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  } finally {
    relinkingRemote.value = false;
  }
}

async function relinkFromLocalDirectory() {
  const name =
      props.skill?.name;

  if (
      !name ||
      checkingUpdate.value ||
      sourceMutating.value
  ) {
    return;
  }

  relinkingLocal.value = true;

  try {
    const selected =
        await selectSkillDirectory(
            source.value?.localDirectory || "",
        );

    if (!selected) {
      return;
    }

    const repairing =
        !props.skill?.valid;

    const confirmed =
        await confirmMutation(
            repairing ? "从本地目录修复 Skill" : "从本地目录重新安装",
            `Humbert 会验证所选目录中的 canonical name 必须仍是「${name}」，再原子替换当前 Package 并记录这个本地目录作为更新来源。${repairing ? " 当前无效 Package 只会在候选内容验证通过后被替换。" : ""}`,
            repairing ? "修复" : "重新安装",
        );

    if (!confirmed) {
      return;
    }

    await reinstallSkillFromDirectory(
        name,
        selected,
    );

    sourceEditorVisible.value = false;
    updateCheck.value = null;

    await loadDetail();
    emit("updated");

    Message.success(
        props.skill?.valid
            ? "Skill 已重新安装并记录本地来源"
            : "Skill 已从本地目录修复并记录来源",
    );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  } finally {
    relinkingLocal.value = false;
  }
}

async function loadFile(path) {
  const file =
      files.value.find(
          (item) =>
              item.path === path,
      );

  if (
      !file ||
      !file.text
  ) {
    selectedPath.value = path;
    fileContent.value = "";
    fileError.value =
        "这个文件不是 UTF-8 文本。详情页只展示文件信息，不读取二进制内容。";

    return;
  }

  selectedPath.value = path;
  fileLoading.value = true;
  fileError.value = "";

  try {
    const result =
        await readSkillFile(
            props.skill.name,
            path,
        );

    fileContent.value =
        result?.content ?? "";
  } catch (error) {
    fileContent.value = "";
    fileError.value =
        error?.message ??
        String(error);

    Message.error(
        fileError.value,
    );
  } finally {
    fileLoading.value = false;
  }
}

async function loadDetail() {
  const name =
      props.skill?.name;

  detail.value = null;
  detailError.value = "";
  selectedPath.value = "";
  fileContent.value = "";
  fileError.value = "";
  editingAlias.value = false;
  aliasDraft.value =
      props.skill?.alias ?? "";
  updateCheck.value = null;
  sourceEditorVisible.value = false;
  sourceURLDraft.value = "";
  sourceSkillPathDraft.value = "";

  if (
      !name ||
      !props.skill?.valid
  ) {
    return;
  }

  loading.value = true;

  try {
    const result =
        await getSkillDetail(name);

    detail.value = result;

    const folders =
        new Set();

    for (
        const file of
        Array.isArray(result?.files)
            ? result.files
            : []
        ) {
      const parts =
          String(
              file?.path ?? "",
          ).split("/");

      for (
          let index = 1;
          index < parts.length;
          index += 1
      ) {
        folders.add(
            parts
                .slice(0, index)
                .join("/"),
        );
      }
    }

    expandedFolders.value = folders;

    const defaultFile =
        result?.files?.find(
            (file) =>
                file.path ===
                "SKILL.md" &&
                file.text,
        ) ??
        result?.files?.find(
            (file) =>
                file.text,
        );

    if (defaultFile?.path) {
      await loadFile(
          defaultFile.path,
      );
    }
  } catch (error) {
    detailError.value =
        error?.message ??
        String(error);
  } finally {
    loading.value = false;
  }
}

watch(
    () => props.skill?.name,
    loadDetail,
    {
      immediate: true,
    },
);
</script>

<template>
  <section class="skill-detail">
    <header class="skill-detail__topbar">
      <button
          type="button"
          class="skill-detail__back"
          @click="emit('back')"
      >
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
        <div class="skill-detail__eyebrow">
          Skill 详情
        </div>

        <div class="skill-detail__title-row">
          <h1>{{ displayName }}</h1>

          <a-tag
              :color="skillStatusColor"
              size="small"
          >
            {{ skillStatusLabel }}
          </a-tag>
        </div>

        <code
            v-if="skill.alias"
            class="skill-detail__canonical"
        >
          {{ skill.name }}
        </code>

        <p>
          {{ skill.valid ? skill.description : (skill.error || "Skill Package 校验失败") }}
        </p>
        <p
            v-if="skill.valid && ['unsupported', 'needs_setup'].includes(skill.runtimeStatus)"
            class="skill-detail__runtime-warning"
        >
          {{ skill.runtimeMessage || (skill.runtimeStatus === 'needs_setup' ? "这个 Skill 可以启用，但执行部分能力前需要补充运行环境。" : "这个 Skill 已安装，但当前 Humbert Runtime 暂不支持启用。") }}
        </p>
      </div>

      <div class="skill-detail__alias">
        <div class="skill-detail__section-label">
          本地显示名称
        </div>

        <div
            v-if="!editingAlias"
            class="skill-detail__alias-view"
        >
          <div>
            <strong>{{ displayName }}</strong>
            <span>
              原始名称：{{ skill.name }}
            </span>
          </div>

          <a-button
              v-if="skill.valid"
              type="text"
              @click="beginAliasEdit"
          >
            <template #icon>
              <IconEdit />
            </template>
            修改显示名称
          </a-button>
        </div>

        <div
            v-else
            class="skill-detail__alias-edit"
        >
          <a-input
              v-model="aliasDraft"
              allow-clear
              :max-length="80"
              show-word-limit
              placeholder="例如：前端设计"
          />

          <div class="skill-detail__alias-actions">
            <a-button
                :disabled="savingAlias"
                @click="editingAlias = false"
            >
              取消
            </a-button>

            <a-button
                type="primary"
                :loading="savingAlias"
                @click="saveAlias"
            >
              保存
            </a-button>
          </div>
        </div>

        <p class="skill-detail__alias-help">
          这里只修改 Humbert 的本地显示名称，不会改写第三方 SKILL.md，也不会改变 Agent.config.json 中的 canonical skill name。
        </p>
      </div>
    </section>

    <section
        v-if="skill.valid && detail"
        class="skill-compatibility"
    >
      <div class="skill-compatibility__summary">
        <div>
          <div class="skill-detail__section-label">兼容与运行环境</div>
          <strong>{{ detail.specStatus === "legacy" ? "Humbert 兼容模式" : "Agent Skills 标准" }}</strong>
          <span v-if="detail.specMessage">{{ detail.specMessage }}</span>
          <span v-else-if="detail.compatibility">{{ detail.compatibility }}</span>
          <span v-else>安装与 Runtime 兼容状态已通过本地诊断。</span>
        </div>
        <div class="skill-compatibility__tags">
          <a-tag v-if="detail.license" size="small">{{ detail.license }}</a-tag>
          <a-tag v-if="detail.allowedTools" color="blue" size="small">声明 tools</a-tag>
          <a-tag v-if="scriptRuntimes.length > 0" color="orange" size="small">
            {{ scriptRuntimes.length }} 个 script
          </a-tag>
        </div>
      </div>

      <div v-if="detail.allowedTools" class="skill-compatibility__row">
        <span>Skill 声明的 allowed-tools</span>
        <code>{{ detail.allowedTools }}</code>
        <small>仅作为兼容提示，不会绕过 Agent Tool 开关、Permission、Approval 或 Sandbox。</small>
      </div>

      <div v-if="detail.metadata && Object.keys(detail.metadata).length > 0" class="skill-compatibility__row">
        <span>metadata</span>
        <div class="skill-metadata">
          <span v-for="(value, key) in detail.metadata" :key="key">
            <code>{{ key }}</code>=<code>{{ value }}</code>
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
          <strong>{{ item.level === "error" ? "阻止启用" : (item.level === "warning" ? "需要注意" : "说明") }}</strong>
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
            {{ runtime.supported && runtime.available ? `可通过 ${runtime.command} 运行` : (runtime.message || "需要配置") }}
          </a-tag>
        </div>
        <p>脚本只会通过 <code>run_skill_script</code> 在当前 Workspace 的临时副本中执行，并继续受命令白名单、Sandbox、Permission 与 Approval 约束。</p>
      </div>
    </section>

    <section
        v-if="usedByAgents.length > 0"
        class="skill-detail__usage"
    >
      <div>
        <strong>
          当前已被 {{ usedByAgents.length }} 个 Agent 启用
        </strong>
        <span>
          删除前需要先回到技能列表，为对应 Agent 关闭这个 Skill。
        </span>
      </div>

      <div class="skill-detail__usage-tags">
        <a-tag
            v-for="agent in usedByAgents"
            :key="agent.id"
            size="small"
        >
          {{ agent.name || agent.id }}
        </a-tag>
      </div>
    </section>

    <section
        v-if="(skill.valid && detail) || !skill.valid"
        class="skill-source"
    >
      <header class="skill-source__header">
        <div>
          <div class="skill-detail__section-label">
            安装与更新来源
          </div>

          <div class="skill-source__title-row">
            <strong>{{ sourceLabel }}</strong>

            <a-tag
                v-if="source.known"
                size="small"
                :color="!skill.valid ? 'orange' : (source.kind === 'remote' ? 'blue' : 'gray')"
            >
              {{ !skill.valid ? "可从来源修复" : (source.kind === "remote" ? "可检查更新" : "本地来源") }}
            </a-tag>

            <a-tag
                v-else
                size="small"
                color="orange"
            >
              未记录
            </a-tag>
          </div>
        </div>

        <div
            v-if="source.known"
            class="skill-source__actions"
        >
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
              :disabled="checkingUpdate || reinstalling || relinkingRemote || relinkingLocal"
              @click="applyAvailableUpdate"
          >
            更新 Skill
          </a-button>

          <a-button
              :type="skill.valid ? 'secondary' : 'primary'"
              :loading="reinstalling"
              :disabled="checkingUpdate || applyingUpdate || relinkingRemote || relinkingLocal"
              @click="forceReinstallFromSource"
          >
            {{ skill.valid ? "重新安装" : "从来源修复" }}
          </a-button>

          <a-button
              type="text"
              @click="sourceEditorVisible = !sourceEditorVisible"
          >
            {{ sourceEditorVisible ? "收起来源设置" : (skill.valid ? "更换来源" : "更换修复来源") }}
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

          <div
              v-if="source.repository"
              class="skill-source__field"
          >
            <span>仓库</span>
            <strong>{{ source.repository }}</strong>
          </div>

          <div
              v-if="source.ref"
              class="skill-source__field"
          >
            <span>Ref</span>
            <code>{{ source.ref }}</code>
          </div>

          <div
              v-if="source.skillPath"
              class="skill-source__field"
          >
            <span>Skill 子目录</span>
            <code>{{ source.skillPath }}</code>
          </div>

          <div class="skill-source__field">
            <span>来源记录 Identity</span>
            <code>{{ shortIdentity(source.recordedIdentity) }}</code>
          </div>

          <div
              v-if="source.installedAt"
              class="skill-source__field"
          >
            <span>首次记录</span>
            <strong>{{ formatUpdatedAt(source.installedAt) }}</strong>
          </div>

          <div
              v-if="source.updatedAt"
              class="skill-source__field"
          >
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
          当前 Package Identity 与 Humbert 上次从来源安装时不同。可能是你手工修改了本地 Skill；可以先检查更新，或使用“重新安装”恢复到记录来源。
        </a-alert>

        <a-alert
            v-if="skill.valid && updateCheck"
            :type="updateCheck.updateAvailable ? 'info' : 'success'"
            :show-icon="true"
            class="skill-source__alert"
        >
          <template v-if="updateCheck.updateAvailable">
            发现新的 Package 内容：{{ shortIdentity(updateCheck.currentIdentity) }} → {{ shortIdentity(updateCheck.candidateIdentity) }}。点击“更新 Skill”后才会原子替换当前安装。
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
        {{ skill.valid
          ? "这个 Skill 没有可复用的安装来源，通常是旧版本 Humbert 安装或手工复制的 Package。它仍可正常使用；重新选择 URL 或本地目录后，Humbert 才能自动检查和更新。"
          : "这个无效 Skill 没有可复用的安装来源。请重新选择 URL 或本地 Skill 目录；Humbert 会先完整验证候选 Package，再原子替换当前损坏内容。" }}
      </a-alert>

      <div
          v-if="!source.known || sourceEditorVisible"
          class="skill-source__editor"
      >
        <div class="skill-source__editor-title">
          {{ !skill.valid
            ? (source.known ? "更换修复来源" : "选择修复来源")
            : (source.known ? "更换来源并重新安装" : "建立更新来源") }}
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
              :disabled="checkingUpdate || applyingUpdate || reinstalling || relinkingLocal"
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
              :disabled="checkingUpdate || applyingUpdate || reinstalling || relinkingRemote"
              @click="relinkFromLocalDirectory"
          >
            {{ skill.valid ? "从Skills目录安装" : "从本地目录修复" }}
          </a-button>
        </div>

        <p>或者从本地目录重新安装并记录来源。</p>
        <p>
          Humbert 会先完整验证 canonical name 必须仍是「{{ skill.name }}」。来源不匹配、下载失败或 Package 校验失败时，{{ skill.valid ? "当前安装" : "当前损坏 Package" }} 保持不变。
        </p>
      </div>
    </section>

    <a-alert
        v-if="!skill.valid"
        type="error"
        :show-icon="true"
    >
      {{ skill.error || "Skill Package 当前无效，无法浏览文件内容。" }}
      {{ source.known ? " 可以在上方使用“从来源修复”。" : " 可以在上方重新选择 URL 或本地目录进行修复。" }}
    </a-alert>

    <a-spin
        v-else
        :loading="loading"
        class="skill-detail__spin"
    >
      <a-alert
          v-if="detailError"
          type="error"
          :show-icon="true"
      >
        {{ detailError }}
      </a-alert>

      <div
          v-else-if="detail"
          class="skill-detail__workspace"
      >
        <aside class="skill-files">
          <div class="skill-files__header">
            <div>
              <strong>Package 文件</strong>
              <span>
                {{ detail.fileCount }} 个文件 · {{ formatBytes(detail.sizeBytes) }}
              </span>
            </div>

            <IconFolder />
          </div>

          <div class="skill-files__tree">
            <button
                v-for="row in treeRows"
                :key="`${row.folder ? 'folder' : 'file'}:${row.path}`"
                type="button"
                class="skill-files__row"
                :class="{
                  'skill-files__row--selected': !row.folder && selectedPath === row.path,
                  'skill-files__row--binary': !row.folder && !row.text,
                }"
                :style="{
                  paddingLeft: `${12 + row.level * 18}px`,
                }"
                @click="row.folder ? toggleFolder(row.path) : loadFile(row.path)"
            >
              <span class="skill-files__marker">
                <template v-if="row.folder">
                  {{ expandedFolders.has(row.path) ? "▾" : "▸" }}
                </template>
                <template v-else>
                  {{ row.text ? "·" : "◇" }}
                </template>
              </span>

              <span class="skill-files__name">
                {{ row.name }}
              </span>

              <span
                  v-if="!row.folder"
                  class="skill-files__size"
              >
                {{ formatBytes(row.sizeBytes) }}
              </span>
            </button>
          </div>

          <div class="skill-files__path">
            <span>安装目录</span>
            <code>{{ detail.rootDir }}</code>
          </div>
        </aside>

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

          <a-spin
              :loading="fileLoading"
              class="skill-preview__body"
          >
            <div
                v-if="fileError"
                class="skill-preview__empty"
            >
              {{ fileError }}
            </div>

            <pre
                v-else-if="selectedPath"
                class="skill-preview__code"
            ><code>{{ fileContent }}</code></pre>

            <div
                v-else
                class="skill-preview__empty"
            >
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

.skill-files {
  display: flex;
  min-width: 0;
  flex-direction: column;
  border-right: 1px solid var(--h-border);
  background: var(--h-bg);
}

.skill-files__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
  padding: 14px;
  border-bottom: 1px solid var(--h-border);
}

.skill-files__header > div {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.skill-files__header strong {
  color: var(--h-text);
  font-size: 11px;
}

.skill-files__header span {
  color: var(--h-text-muted);
  font-size: 10px;
}

.skill-files__tree {
  min-height: 0;
  flex: 1;
  overflow: auto;
  padding: 6px 0;
}

.skill-files__row {
  display: flex;
  width: 100%;
  min-width: 0;
  align-items: center;
  gap: 6px;
  padding-top: 7px;
  padding-right: 10px;
  padding-bottom: 7px;
  border: 0;
  background: transparent;
  color: var(--h-text-secondary);
  cursor: pointer;
  font: inherit;
  text-align: left;
}

.skill-files__row:hover {
  background: var(--h-surface-hover);
}

.skill-files__row--selected {
  background: var(--h-surface-active);
  color: var(--h-text);
}

.skill-files__row--binary {
  color: var(--h-text-muted);
}

.skill-files__marker {
  width: 12px;
  flex: 0 0 12px;
  color: var(--h-text-muted);
  text-align: center;
}

.skill-files__name {
  min-width: 0;
  flex: 1;
  overflow: hidden;
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.skill-files__size {
  flex: 0 0 auto;
  color: var(--h-text-muted);
  font-size: 8px;
}

.skill-files__path {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 12px;
  border-top: 1px solid var(--h-border);
  color: var(--h-text-muted);
  font-size: 8px;
}

.skill-files__path code {
  overflow: hidden;
  color: var(--h-text-secondary);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  text-overflow: ellipsis;
  white-space: nowrap;
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

  .skill-files {
    min-height: 260px;
    border-right: 0;
    border-bottom: 1px solid var(--h-border);
  }
}
</style>
