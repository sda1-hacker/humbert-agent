<script setup>
import {
  computed,
  onMounted,
  ref,
  watch,
} from "vue";

import {
  Message,
} from "@arco-design/web-vue";

import {
  IconRefresh,
  IconSearch,
  IconSettings,
} from "@arco-design/web-vue/es/icon";

import {
  disableSkillForAgent,
  enableSkillForAgent,
  getSkillState,
} from "../../api/skills.js";

import {
  useAgentStore,
} from "../../stores/agents.js";

const props =
    defineProps({
      initialAgentId: {
        type: String,
        default: "",
      },
    });

const emit =
    defineEmits([
      "agent-change",
      "manage-packages",
      "open-detail",
    ]);

const agentStore =
    useAgentStore();

const rootDir =
    ref("");

const agents =
    ref([]);

const skills =
    ref([]);

const loading =
    ref(false);

const loadError =
    ref("");

const sourceError =
    ref("");

const selectedAgentID =
    ref("");

const catalogSearch =
    ref("");


const updatingKey =
    ref("");

const selectedAgent =
    computed(() => agents.value.find(
        (agent) => agent.id === selectedAgentID.value,
    ) ?? null);

const visibleSkills =
    computed(() => {
      const query =
          catalogSearch.value
              .trim()
              .toLocaleLowerCase();

      if (!query) {
        return skills.value;
      }

      return skills.value.filter(
          (skill) => [
            skill?.alias,
            skill?.name,
            skill?.directoryName,
            skill?.description,
          ]
              .filter(Boolean)
              .join("\n")
              .toLocaleLowerCase()
              .includes(query),
      );
    });

const validSkillNames =
    computed(() => new Set(
        skills.value
            .filter(
                (skill) => Boolean(
                    skill?.valid &&
                    skill?.name,
                ),
            )
            .map(
                (skill) => skill.name,
            ),
    ));

const unavailableSkills =
    computed(() => {
      const enabled =
          Array.isArray(
              selectedAgent.value
                  ?.enabledSkills,
          )
              ? selectedAgent.value.enabledSkills
              : [];

      return [...new Set(enabled)]
          .filter(
              (name) =>
                  !validSkillNames.value.has(
                      name,
                  ),
          )
          .map(
              (name) => {
                const installed =
                    skills.value.find(
                        (skill) =>
                            skill?.name === name,
                    );

                return {
                  name,

                  displayName:
                      installed?.alias
                          ?.trim() ||
                      installed?.name ||
                      name,

                  reason:
                      installed
                          ? (
                              installed.error ||
                              "Skill Package 当前无效"
                          )
                          : "Skill 已从安装目录移除或尚未安装",
                };
              },
          );
    });

const selectedEnabledCount =
    computed(() => (
        Array.isArray(
            selectedAgent.value
                ?.enabledSkills,
        )
            ? selectedAgent.value
                .enabledSkills
                .length
            : 0
    ));

function displayName(skill) {
  return (
      skill?.alias?.trim() ||
      skill?.name ||
      skill?.directoryName ||
      "未命名 Skill"
  );
}

function usedByAgents(skill) {
  return Array.isArray(
      skill?.usedByAgents,
  )
      ? skill.usedByAgents
      : [];
}

function enabledForSelectedAgent(
    skillName,
) {
  if (
      !skillName ||
      !selectedAgent.value
  ) {
    return false;
  }

  const enabled =
      Array.isArray(
          selectedAgent.value
              .enabledSkills,
      )
          ? selectedAgent.value
              .enabledSkills
          : [];

  return enabled.includes(
      skillName,
  );
}

function operationKey(
    skillName,
    agentID = selectedAgentID.value,
) {
  return `${skillName ?? ""}:${agentID ?? ""}`;
}

function normalizeState(state) {
  const nextSkills =
      Array.isArray(state?.skills)
          ? state.skills.map(
              (skill) => ({
                ...skill,

                alias:
                    typeof skill?.alias ===
                    "string"
                        ? skill.alias
                        : "",

                usedByAgents:
                    Array.isArray(
                        skill?.usedByAgents,
                    )
                        ? skill.usedByAgents
                        : [],

                source:
                    skill?.source &&
                    typeof skill.source === "object"
                        ? skill.source
                        : {known: false},
              }),
          )
          : [];

  const enabledByAgent =
      new Map();

  for (const skill of nextSkills) {
    if (!skill?.name) {
      continue;
    }

    for (
        const agent of
        usedByAgents(skill)
        ) {
      if (!agent?.id) {
        continue;
      }

      if (
          !enabledByAgent.has(
              agent.id,
          )
      ) {
        enabledByAgent.set(
            agent.id,
            new Set(),
        );
      }

      enabledByAgent
          .get(agent.id)
          .add(skill.name);
    }
  }

  const nextAgents =
      Array.isArray(state?.agents)
          ? state.agents
              .map(
                  (agent) => {
                    const explicit =
                        Array.isArray(
                            agent?.enabledSkills,
                        )
                            ? agent.enabledSkills
                                .filter(Boolean)
                            : [];

                    const inferred =
                        [
                          ...(
                              enabledByAgent
                                  .get(agent?.id) ??
                              []
                          ),
                        ];

                    return {
                      id:
                          agent?.id ?? "",

                      name:
                          agent?.name ?? "",

                      enabledSkills:
                          [
                            ...new Set([
                              ...explicit,
                              ...inferred,
                            ]),
                          ],
                    };
                  },
              )
              .filter(
                  (agent) =>
                      Boolean(agent.id),
              )
          : [];

  return {
    rootDir:
        state?.rootDir ?? "",

    sourceError:
        typeof state?.sourceError === "string"
            ? state.sourceError
            : "",

    agents:
    nextAgents,

    skills:
    nextSkills,
  };
}

function syncAgentStore(agentID) {
  const refreshed =
      agents.value.find(
          (agent) =>
              agent.id === agentID,
      );

  if (!refreshed) {
    return;
  }

  const index =
      agentStore.items.findIndex(
          (project) =>
              project.id === agentID,
      );

  if (index < 0) {
    return;
  }

  agentStore.items[index] = {
    ...agentStore.items[index],

    enabledSkills: [
      ...refreshed.enabledSkills,
    ],
  };
}

async function load() {
  if (loading.value) {
    return;
  }

  loading.value = true;
  loadError.value = "";
  sourceError.value = "";

  const previousAgentID =
      selectedAgentID.value;

  try {
    const raw =
        await getSkillState();

    const state =
        normalizeState(raw);

    rootDir.value =
        state.rootDir;

    sourceError.value =
        state.sourceError;

    agents.value =
        state.agents;

    skills.value =
        state.skills;

    const previousExists =
        agents.value.some(
            (agent) =>
                agent.id ===
                previousAgentID,
        );

    const requestedAgentID =
        props.initialAgentId;

    const requestedExists =
        agents.value.some(
            (agent) =>
                agent.id ===
                requestedAgentID,
        );

    const activeAgentID =
        agentStore.selectedID;

    const activeExists =
        agents.value.some(
            (agent) =>
                agent.id ===
                activeAgentID,
        );

    selectedAgentID.value =
        previousExists
            ? previousAgentID
            : (
                requestedExists
                    ? requestedAgentID
                    : (
                        activeExists
                            ? activeAgentID
                            : (
                                agents.value[0]
                                    ?.id ??
                                ""
                            )
                    )
            );
  } catch (error) {
    loadError.value =
        error?.message ??
        String(error);

    console.error(
        "[Skills] 加载技能列表失败",
        error,
    );
  } finally {
    loading.value = false;
  }
}

function openDetail(skill) {
  const name =
      skill?.name ||
      skill?.directoryName ||
      "";

  if (!name) {
    return;
  }

  emit(
      "open-detail",
      name,
  );
}

async function setSkillEnabled(
    skill,
    enabled,
) {
  const agent =
      selectedAgent.value;

  if (
      !agent?.id ||
      !skill?.name ||
      updatingKey.value
  ) {
    return;
  }

  const key =
      operationKey(
          skill.name,
          agent.id,
      );

  updatingKey.value = key;

  try {
    if (enabled) {
      await enableSkillForAgent(
          skill.name,
          agent.id,
      );
    } else {
      await disableSkillForAgent(
          skill.name,
          agent.id,
      );
    }

    await load();
    syncAgentStore(agent.id);

    Message.success(
        `「${displayName(skill)}」已为 ${agent.name || agent.id} ${enabled ? "启用" : "停用"}`,
    );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  } finally {
    updatingKey.value = "";
  }
}

function onSkillSwitchChange(
    skill,
    checked,
) {
  void setSkillEnabled(
      skill,
      checked === true,
  );
}

async function removeUnavailableReference(
    name,
) {
  const agent =
      selectedAgent.value;

  if (
      !agent?.id ||
      !name ||
      updatingKey.value
  ) {
    return;
  }

  const key =
      operationKey(
          name,
          agent.id,
      );

  updatingKey.value = key;

  try {
    await disableSkillForAgent(
        name,
        agent.id,
    );

    await load();
    syncAgentStore(agent.id);

    Message.success(
        `已从 ${agent.name || agent.id} 移除不可用 Skill「${name}」`,
    );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  } finally {
    updatingKey.value = "";
  }
}


watch(
    selectedAgentID,
    (value) => {
      if (value) {
        emit(
            "agent-change",
            value,
        );
      }
    },
);

onMounted(load);
</script>

<template>
  <div class="skill-settings">
    <section class="skill-control-panel">
      <div class="skill-control-panel__top">
        <div class="skill-control-panel__copy">
          <h2>为 Agent 配置技能</h2>
          <p>
            先选择 Agent / 项目，再用右侧开关决定它可以使用哪些 Skill。安装、更新与修复 Package 请进入「设置 → 技能」。
          </p>
        </div>

        <div class="skill-control-panel__actions">
          <a-button
              :loading="loading"
              @click="load"
          >
            <template #icon>
              <IconRefresh/>
            </template>
            刷新
          </a-button>

          <a-button
              type="primary"
              @click="emit('manage-packages')"
          >
            <template #icon>
              <IconSettings/>
            </template>
            管理 Skill Packages
          </a-button>
        </div>
      </div>

      <div
          v-if="loadError"
          class="skill-load-error"
      >
        <div>
          <strong>Skills 数据加载失败</strong>
          <span>{{ loadError }}</span>
        </div>

        <a-button
            size="small"
            @click="load"
        >
          重试
        </a-button>
      </div>

      <a-alert
          v-if="sourceError"
          type="warning"
          :show-icon="true"
      >
        {{ sourceError }}。Skill 列表仍可使用，但来源更新/修复功能会暂时不可用。
      </a-alert>

      <div class="agent-picker">
        <div class="agent-picker__identity">
          <span class="agent-picker__avatar" aria-hidden="true">
            {{ (selectedAgent?.name || "A").slice(0, 1).toUpperCase() }}
          </span>

          <div class="agent-picker__copy">
            <span>当前 Agent / 项目</span>
            <small>
              {{ selectedAgent ? `已启用 ${selectedEnabledCount} 个 Skill` : "创建 Agent 后即可在这里配置" }}
            </small>
          </div>
        </div>

        <a-select
            v-model="selectedAgentID"
            class="agent-picker__select"
            :disabled="agents.length === 0"
            placeholder="选择 Agent / 项目"
        >
          <a-option
              v-for="agent in agents"
              :key="agent.id"
              :value="agent.id"
          >
            {{ agent.name || agent.id }}
          </a-option>
        </a-select>
      </div>

      <div
          v-if="unavailableSkills.length > 0"
          class="stale-skills"
      >
        <div class="stale-skills__copy">
          <strong>
            {{ selectedAgent?.name || "当前 Agent" }} 有 {{ unavailableSkills.length }} 个失效 Skill 配置
          </strong>
          <span>
            这些配置对应已删除或损坏的 Skill Package，可能阻止该 Agent 开始新的对话轮次，可以在这里安全移除。
          </span>
        </div>

        <div class="stale-skills__items">
          <div
              v-for="issue in unavailableSkills"
              :key="issue.name"
              class="stale-skills__item"
          >
            <div>
              <strong>{{ issue.displayName }}</strong>
              <code>{{ issue.name }}</code>
              <span>{{ issue.reason }}</span>
            </div>

            <a-button
                size="small"
                status="warning"
                :loading="updatingKey === operationKey(issue.name)"
                :disabled="Boolean(updatingKey)"
                @click="removeUnavailableReference(issue.name)"
            >
              从 Agent 移除
            </a-button>
          </div>
        </div>
      </div>

      <div class="skill-toolbar">
        <a-input
            v-model="catalogSearch"
            allow-clear
            placeholder="搜索显示名称、Skill 名称或描述"
        >
          <template #prefix>
            <IconSearch/>
          </template>
        </a-input>

        <span>{{ visibleSkills.length }} / {{ skills.length }}</span>
      </div>

      <div class="skill-browse-hint">
        <span class="skill-browse-hint__icon" aria-hidden="true">↗</span>
        <span>
          点击任意 Skill 卡片会打开独立详情页，可查看 SKILL.md / references / scripts，并修改中文显示名称；右侧开关只负责当前 Agent 的启用状态。
        </span>
      </div>
    </section>

    <div
        v-if="loading && skills.length === 0"
        class="skill-page-state"
    >
      正在读取本地 Skills…
    </div>

    <div
        v-else-if="visibleSkills.length > 0"
        class="skill-list"
    >
      <article
          v-for="skill in visibleSkills"
          :key="skill.directoryName || skill.name"
          class="skill-row"
          :class="{
            'skill-row--enabled': selectedAgent && enabledForSelectedAgent(skill.name),
            'skill-row--invalid': !skill.valid,
            'skill-row--unsupported': skill.valid && ['unsupported', 'needs_setup'].includes(skill.runtimeStatus),
          }"
          role="button"
          tabindex="0"
          :aria-label="`查看 ${displayName(skill)} 详情`"
          @click="openDetail(skill)"
          @keydown.enter="openDetail(skill)"
      >
        <div class="skill-row__body">
          <div class="skill-row__title">
            <strong>{{ displayName(skill) }}</strong>

            <code v-if="skill.alias">
              {{ skill.name }}
            </code>

            <span
                v-if="!skill.valid"
                class="skill-status skill-status--error"
            >
              无效
            </span>
            <span
                v-else-if="skill.runtimeStatus === 'unsupported'"
                class="skill-status skill-status--warning"
            >
              暂不可启用
            </span>
            <span
                v-else-if="skill.runtimeStatus === 'needs_setup'"
                class="skill-status skill-status--warning"
            >
              需要配置
            </span>
            <span
                v-else-if="skill.specStatus === 'legacy'"
                class="skill-status skill-status--muted"
            >
              兼容模式
            </span>
          </div>

          <p :class="{ 'skill-row__error': !skill.valid }">
            {{ skill.valid ? skill.description : (skill.error || "Skill Package 校验失败") }}
          </p>

          <div class="skill-row__meta">
            <span v-if="skill.valid">
              {{ skill.fileCount }} 个文件
            </span>
            <span v-if="skill.hasScripts">scripts</span>
            <span v-if="skill.hasReferences">references</span>
            <span v-if="skill.hasAssets">assets</span>
            <span v-if="skill.valid && ['unsupported', 'needs_setup'].includes(skill.runtimeStatus)"
                  class="skill-row__runtime-warning">
              {{
                skill.runtimeMessage || (skill.runtimeStatus === 'needs_setup' ? "需要补充运行环境" : "当前 Runtime 暂不支持启用")
              }}
            </span>
            <span v-if="usedByAgents(skill).length > 0">
              {{ usedByAgents(skill).length }} 个 Agent 已启用
            </span>
            <span v-if="!skill.valid && skill.source?.known">
              可从已记录来源修复
            </span>
          </div>
        </div>

        <div class="skill-row__actions">
          <button
              type="button"
              class="skill-row__detail"
              title="查看 Skill 详情并修改显示名称"
              @click.stop="openDetail(skill)"
          >
            <span>查看详情</span>
            <span aria-hidden="true">›</span>
          </button>

          <div
              class="skill-row__switch"
              @click.stop
          >
            <span>
              {{ selectedAgent ? (enabledForSelectedAgent(skill.name) ? "已启用" : "未启用") : "选择 Agent 后启用" }}
            </span>

            <a-switch
                :model-value="enabledForSelectedAgent(skill.name)"
                :loading="updatingKey === operationKey(skill.name)"
                :disabled="
                  !selectedAgent ||
                  Boolean(updatingKey) ||
                  ((!skill.valid || skill.runtimeStatus === 'unsupporte') && !enabledForSelectedAgent(skill.name))
            "
                @change="onSkillSwitchChange(skill, $event)"
            />
          </div>
        </div>
      </article>
    </div>

    <div
        v-else
        class="skill-page-state"
    >
      {{ skills.length > 0 ? "没有匹配的 Skill" : "还没有安装 Skill" }}
    </div>

    <div class="skill-settings__footer">
      <span>
        Skill 文件预览只读；scripts 只有在 Agent 启用 run_skill_script 且通过 Sandbox / Permission 后才会执行。
      </span>
      <code>{{ rootDir || "~/.humbert-agent/skills" }}</code>
    </div>
  </div>
</template>

<style scoped>
.skill-settings {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 14px;
}

.skill-control-panel {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 14px;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-lg);
  background: var(--h-surface);
  padding: 18px;
}

.skill-control-panel__top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 18px;
}

.skill-control-panel__copy h2 {
  margin: 0;
  color: var(--h-text);
  font-size: 17px;
  font-weight: 600;
}

.skill-control-panel__copy p {
  max-width: 760px;
  margin: 6px 0 0;
  color: var(--h-text-secondary);
  font-size: 12px;
  line-height: 1.7;
}

.skill-control-panel__actions {
  display: flex;
  flex: 0 0 auto;
  gap: 8px;
}

.skill-load-error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  border: 1px solid var(--h-danger-border);
  border-radius: var(--h-radius-md);
  background: var(--h-danger-soft);
  padding: 11px 12px;
}

.skill-load-error > div {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 3px;
}

.skill-load-error strong {
  color: var(--h-danger);
  font-size: 12px;
}

.skill-load-error span {
  color: var(--h-text-secondary);
  font-size: 11px;
  word-break: break-word;
}

.agent-picker {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-md);
  background: var(--h-bg);
  padding: 11px 12px;
}

.agent-picker__identity {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 10px;
}

.agent-picker__avatar {
  display: grid;
  width: 32px;
  height: 32px;
  flex: 0 0 32px;
  place-items: center;
  border: 1px solid var(--h-border-strong);
  border-radius: 50%;
  background: var(--h-accent-soft);
  color: var(--h-accent-hover);
  font-size: 12px;
  font-weight: 600;
}

.agent-picker__copy {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 2px;
}

.agent-picker__copy > span {
  color: var(--h-text);
  font-size: 12px;
  font-weight: 600;
}

.agent-picker__copy small {
  color: var(--h-text-muted);
  font-size: 10px;
}

.agent-picker__select {
  width: min(320px, 44%);
  min-width: 210px;
}


.stale-skills {
  display: flex;
  flex-direction: column;
  gap: 10px;
  border: 1px solid var(--h-danger-border);
  border-radius: var(--h-radius-md);
  background: var(--h-danger-soft);
  padding: 12px;
}

.stale-skills__copy,
.stale-skills__item > div {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.stale-skills__copy strong,
.stale-skills__item strong {
  color: var(--h-text);
  font-size: 11px;
}

.stale-skills__copy span,
.stale-skills__item span,
.stale-skills__item code {
  color: var(--h-text-secondary);
  font-size: 10px;
}

.stale-skills__items {
  display: flex;
  flex-direction: column;
  gap: 7px;
}

.stale-skills__item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  border-top: 1px solid var(--h-danger-border);
  padding-top: 8px;
}

.skill-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
}

.skill-toolbar :deep(.arco-input-wrapper) {
  min-width: 0;
  flex: 1;
}

.skill-toolbar > span {
  flex: 0 0 auto;
  color: var(--h-text-muted);
  font-size: 10px;
}

.skill-browse-hint {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  border-top: 1px solid var(--h-border);
  padding-top: 11px;
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.6;
}

.skill-browse-hint__icon {
  flex: 0 0 auto;
  color: var(--h-accent);
  font-size: 12px;
}

.skill-list {
  display: flex;
  min-width: 0;
  flex-direction: column;
  overflow: hidden;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-lg);
  background: var(--h-surface);
}

.skill-row {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 22px;
  padding: 15px 16px;
  cursor: pointer;
  outline: none;
  background: transparent;
  transition: background-color 120ms ease;
}

.skill-row + .skill-row {
  border-top: 1px solid var(--h-border);
}

.skill-row:hover,
.skill-row:focus-visible {
  background: var(--h-surface-hover);
}

.skill-row--enabled {
  background: var(--h-accent-soft);
}

.skill-row--enabled:hover {
  background: var(--h-accent-soft);
}

.skill-row--invalid {
  background: var(--h-danger-soft);
}

.skill-row--unsupported {
  background: color-mix(in srgb, var(--h-warning, #f59e0b) 5%, transparent);
}

.skill-row__body {
  min-width: 0;
}

.skill-row__title {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 7px;
}

.skill-row__title strong {
  color: var(--h-text);
  font-size: 13px;
  font-weight: 600;
}

.skill-row__title code {
  color: var(--h-text-muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 10px;
}

.skill-status {
  border-radius: 999px;
  padding: 1px 6px;
  font-size: 10px;
}

.skill-status--error {
  background: var(--h-danger-soft);
  color: var(--h-danger);
}

.skill-status--warning {
  background: color-mix(in srgb, var(--h-warning, #f59e0b) 12%, transparent);
  color: var(--h-warning, #b7791f);
}

.skill-status--muted {
  border-color: var(--h-border);
  background: var(--h-surface-hover);
  color: var(--h-text-muted);
}


.skill-row__body p {
  display: -webkit-box;
  max-width: 820px;
  margin: 6px 0 0;
  overflow: hidden;
  color: var(--h-text-secondary);
  font-size: 11px;
  line-height: 1.55;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.skill-row__runtime-warning {
  color: var(--h-warning, #b7791f);
}

.skill-row__error {
  color: var(--h-danger) !important;
}

.skill-row__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 8px;
  color: var(--h-text-muted);
  font-size: 10px;
}

.skill-row__actions {
  display: flex;
  min-width: 170px;
  flex: 0 0 auto;
  flex-direction: column;
  align-items: flex-end;
  gap: 9px;
}

.skill-row__detail {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--h-accent-hover);
  cursor: pointer;
  font: inherit;
  font-size: 10px;
}

.skill-row__detail span:last-child {
  font-size: 15px;
  line-height: 1;
}

.skill-row__switch {
  display: flex;
  align-items: center;
  gap: 8px;
}

.skill-row__switch > span {
  color: var(--h-text-muted);
  font-size: 10px;
  white-space: nowrap;
}

.skill-page-state {
  display: grid;
  min-height: 150px;
  place-items: center;
  border: 1px dashed var(--h-border-strong);
  border-radius: var(--h-radius-lg);
  color: var(--h-text-muted);
  font-size: 11px;
}

.skill-settings__footer {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  color: var(--h-text-muted);
  font-size: 10px;
}

.skill-settings__footer code {
  min-width: 0;
  overflow: hidden;
  color: var(--h-text-secondary);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 900px) {
  .skill-control-panel__top,
  .agent-picker,
  .skill-row {
    align-items: stretch;
    grid-template-columns: 1fr;
    flex-direction: column;
  }

  .agent-picker__select {
    width: 100%;
    min-width: 0;
  }

  .skill-row__actions {
    min-width: 0;
    align-items: flex-start;
  }
}
</style>
