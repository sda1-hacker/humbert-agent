<script setup>
import {
  computed,
  onMounted,
  reactive,
  ref,
  watch,
} from "vue";

import { Modal } from "@arco-design/web-vue";
import { Message } from "../../utils/uiMessage.js";

import {
  clearPersistentPermissionAllows,
  clearSessionPermissionRules,
  deletePermissionRule,
  deleteSessionPermissionRule,
  getPermissionState,
  updatePermissionSettings,
} from "../../api/permissions.js";
import {
  useSessionStore,
} from "../../stores/sessions.js";
import SectionCard from "../ui/SectionCard.vue";
import EmptyState from "../ui/EmptyState.vue";
import StatusPill from "../ui/StatusPill.vue";
import { formatDate, t } from "../../i18n/index.js";

const sessionStore =
    useSessionStore();

const loading = ref(false);
const saving = ref(false);
const deletingRuleID = ref("");
const clearingPersistent = ref(false);
const clearingSession = ref(false);
const state = ref(null);

const form = reactive({
  enabled: true,
  readAction: "allow",
  writeAction: "ask",
  execAction: "ask",
  approvalTimeoutMinutes: 30,
});

const actionDefinitions = [
  {
    label: "允许",
    value: "allow",
  },
  {
    label: "询问",
    value: "ask",
  },
  {
    label: "拒绝",
    value: "deny",
  },
];
const actionOptions = computed(() => actionDefinitions.map((option) => ({ ...option, label: t(option.label) })));

const currentSessionID =
    computed(() =>
        sessionStore.selectedID || "",
    );

const persistentRules =
    computed(() => (
        Array.isArray(
            state.value?.persistentRules,
        )
            ? state.value.persistentRules
            : []
    ));

const sessionRules =
    computed(() => (
        Array.isArray(
            state.value?.sessionRules,
        )
            ? state.value.sessionRules
            : []
    ));

const persistentAllowCount =
    computed(() =>
        persistentRules.value.filter(
            (rule) =>
                rule?.action === "allow",
        ).length,
    );


const dangerousConfirmationEnabled =
    computed({
      get: () =>
          Boolean(form.enabled) &&
          form.writeAction === "ask" &&
          form.execAction === "ask",
      set: (enabled) => {
        form.enabled = true;
        form.readAction = "allow";
        form.writeAction = enabled ? "ask" : "allow";
        form.execAction = enabled ? "ask" : "allow";
      },
    });

const hasUnsavedChanges =
    computed(() => {
      if (!state.value) {
        return false;
      }
      return (
          Boolean(form.enabled) !==
          Boolean(state.value.enabled) ||
          form.readAction !==
          state.value.readAction ||
          form.writeAction !==
          state.value.writeAction ||
          form.execAction !==
          state.value.execAction ||
          Math.round(
              Number(
                  form.approvalTimeoutMinutes,
              ) * 60000,
          ) !==
          Number(
              state.value.approvalTimeoutMS,
          )
      );
    });

function actionText(action) {
  switch (action) {
    case "allow":
      return t("允许");
    case "ask":
      return t("询问");
    case "deny":
      return t("拒绝");
    default:
      return action || t("未知");
  }
}

function scopeText(scope) {
  switch (scope) {
    case "session":
      return t("当前会话");
    case "agent":
      return t("长期记住");
    default:
      return scope || t("未知范围");
  }
}

function formatCreatedAt(value) {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return formatDate(date, { dateStyle: "short", timeStyle: "short" });
}

function ruleTarget(rule) {
  if (rule?.command) {
    return `运行本地程序 · ${rule.command}`;
  }
  if (rule?.skillName) {
    const script = rule?.script ? ` · ${rule.script}` : "";
    return `Skill · ${rule.skillName}${script}`;
  }
  if (rule?.mcpServerID) {
    const tool = rule?.mcpTool ? ` · ${rule.mcpTool}` : "";
    return `${rule.mcpServerName || "MCP Server"}${tool}`;
  }
  return rule?.toolName || "未知操作";
}

function shortFingerprint(value) {
  const text = String(value || "");
  if (!text) return "";
  return `${text.slice(0, 10)}…${text.slice(-6)}`;
}

function applyState(nextState) {
  state.value = nextState ?? null;
  if (!state.value) {
    return;
  }
  form.enabled =
      Boolean(state.value.enabled);
  form.readAction =
      state.value.readAction || "allow";
  form.writeAction =
      state.value.writeAction || "ask";
  form.execAction =
      state.value.execAction || "ask";
  const timeoutMS =
      Number(
          state.value.approvalTimeoutMS,
      );
  form.approvalTimeoutMinutes =
      Number.isFinite(timeoutMS) &&
      timeoutMS > 0
          ? Math.max(
              1,
              Math.round(
                  timeoutMS / 60000,
              ),
          )
          : 30;
}

async function load() {
  if (loading.value) {
    return;
  }
  loading.value = true;
  try {
    const nextState =
        await getPermissionState(
            currentSessionID.value,
        );
    applyState(nextState);
  } catch (error) {
    Message.error(
        error?.message ?? String(error),
    );
  } finally {
    loading.value = false;
  }
}

async function saveSettings() {
  if (saving.value) {
    return;
  }

  const minutes =
      Number(
          form.approvalTimeoutMinutes,
      );
  if (
      !Number.isFinite(minutes) ||
      minutes < 1 ||
      minutes > 1440
  ) {
    Message.error(
        "审批超时必须位于 1-1440 分钟之间",
    );
    return;
  }

  saving.value = true;
  try {
    await updatePermissionSettings({
      enabled: Boolean(form.enabled),
      readAction: form.readAction,
      writeAction: form.writeAction,
      execAction: form.execAction,
      approvalTimeoutMS:
          Math.round(minutes * 60000),
    });
    Message.success(
        "操作确认设置已保存",
    );
    await load();
  } catch (error) {
    Message.error(
        error?.message ?? String(error),
    );
  } finally {
    saving.value = false;
  }
}

function confirmDeletePersistent(rule) {
  if (!rule?.id || deletingRuleID.value) {
    return;
  }

  const isDeny =
      rule.action === "deny";
  Modal.warning({
    title:
        isDeny
            ? "撤销长期拒绝规则？"
            : "撤销长期授权？",
    content:
        `撤销后，${rule.agentName || "该 Agent"} 再次调用 ${ruleTarget(rule)} 时会重新按照其他匹配规则与默认操作确认设置判断。`,
    hideCancel: false,
    okText:
        isDeny
            ? "撤销拒绝"
            : "撤销授权",
    cancelText: "取消",
    onOk: async () => {
      deletingRuleID.value = rule.id;
      try {
        await deletePermissionRule(
            rule.id,
        );
        Message.success(
            isDeny
                ? "长期拒绝规则已撤销"
                : "长期授权已撤销",
        );
        await load();
      } catch (error) {
        Message.error(
            error?.message ?? String(error),
        );
        throw error;
      } finally {
        deletingRuleID.value = "";
      }
    },
  });
}

function confirmDeleteSession(rule) {
  if (
      !currentSessionID.value ||
      !rule?.id ||
      deletingRuleID.value
  ) {
    return;
  }

  Modal.warning({
    title: "撤销当前会话临时授权？",
    content:
        `撤销后，当前会话再次调用 ${ruleTarget(rule)} 时会重新按照长期规则与默认操作确认设置判断。`,
    hideCancel: false,
    okText: "撤销",
    cancelText: "取消",
    onOk: async () => {
      deletingRuleID.value = rule.id;
      try {
        await deleteSessionPermissionRule(
            currentSessionID.value,
            rule.id,
        );
        Message.success(
            "本会话临时授权已撤销",
        );
        await load();
      } catch (error) {
        Message.error(
            error?.message ?? String(error),
        );
        throw error;
      } finally {
        deletingRuleID.value = "";
      }
    },
  });
}

function confirmClearSession() {
  if (
      !currentSessionID.value ||
      sessionRules.value.length === 0 ||
      clearingSession.value
  ) {
    return;
  }

  Modal.warning({
    title: "清除当前会话临时授权？",
    content:
        "清除后，当前会话中临时记住的允许规则会立即失效；长期规则不会受到影响。",
    hideCancel: false,
    okText: "清除临时授权",
    cancelText: "取消",
    onOk: async () => {
      clearingSession.value = true;
      try {
        await clearSessionPermissionRules(
            currentSessionID.value,
        );
        Message.success(
            "当前会话临时授权已清除",
        );
        await load();
      } catch (error) {
        Message.error(
            error?.message ?? String(error),
        );
        throw error;
      } finally {
        clearingSession.value = false;
      }
    },
  });
}

function confirmClearPersistentAllows() {
  if (
      persistentAllowCount.value === 0 ||
      clearingPersistent.value
  ) {
    return;
  }

  Modal.warning({
    title: "撤销全部 Agent 长期授权？",
    content:
        `将撤销 ${persistentAllowCount.value} 条长期允许规则。显式的长期拒绝规则会保留，当前会话临时授权也不会受影响。`,
    hideCancel: false,
    okText: "撤销全部长期授权",
    cancelText: "取消",
    onOk: async () => {
      clearingPersistent.value = true;
      try {
        const deleted =
            await clearPersistentPermissionAllows();
        Message.success(
            `已撤销 ${Number(deleted) || 0} 条长期授权`,
        );
        await load();
      } catch (error) {
        Message.error(
            error?.message ?? String(error),
        );
        throw error;
      } finally {
        clearingPersistent.value = false;
      }
    },
  });
}

watch(
    currentSessionID,
    () => {
      void load();
    },
);

onMounted(() => {
  void load();
});
</script>

<template>
  <div class="permission-settings">
    <SectionCard
        title="操作确认"
        description="建议保持开启。Humbert 在修改文件、删除内容或运行本地程序等高风险操作前会向你确认。"
    >
      <template #actions>
        <div class="settings-card__header-actions">
          <a-button
              size="small"
              type="primary"
              :loading="saving"
              :disabled="!hasUnsavedChanges"
              @click="saveSettings"
          >
            保存
          </a-button>
        </div>
      </template>

      <a-spin :loading="loading">
        <div class="simple-confirmation-row">
          <div>
            <div class="policy-row__title">危险操作前询问</div>
            <div class="policy-row__description">
              开启后，读取普通资料不会频繁打扰你；修改文件和运行本地程序等操作会先请求确认。
            </div>
          </div>
          <a-switch v-model="dangerousConfirmationEnabled" />
        </div>

        <details class="permission-advanced">
          <summary>高级确认规则</summary>
          <div class="policy-form">
            <div class="policy-row">
              <div>
                <div class="policy-row__title">启用操作确认系统</div>
                <div class="policy-row__description">关闭后默认不会弹出操作确认；本地命令仍只能只读访问工作区。</div>
              </div>
              <a-switch v-model="form.enabled"/>
            </div>

            <div class="policy-row">
              <div>
                <div class="policy-row__title">读取操作</div>
                <div class="policy-row__description">读取文件、列目录和网络读取等只读操作的默认处理方式。</div>
              </div>
              <a-select
                  v-model="form.readAction"
                  :options="actionOptions"
                  class="policy-control"
              />
            </div>

            <div class="policy-row">
              <div>
                <div class="policy-row__title">修改文件</div>
                <div class="policy-row__description">写入、编辑等会修改用户数据的操作。</div>
              </div>
              <a-select
                  v-model="form.writeAction"
                  :options="actionOptions"
                  class="policy-control"
              />
            </div>

            <div class="policy-row">
              <div>
                <div class="policy-row__title">运行本地程序</div>
                <div class="policy-row__description">运行本地命令等高风险操作；原生沙盒仍会阻止命令修改或删除工作区文件。</div>
              </div>
              <a-select
                  v-model="form.execAction"
                  :options="actionOptions"
                  class="policy-control"
              />
            </div>

            <div class="policy-row">
              <div>
                <div class="policy-row__title">确认等待时间</div>
                <div class="policy-row__description">确认请求超过这个时间后自动失效。</div>
              </div>
              <div class="timeout-control">
                <a-input-number
                    v-model="form.approvalTimeoutMinutes"
                    :min="1"
                    :max="1440"
                    :precision="0"
                    class="policy-control"
                />
                <span>分钟</span>
              </div>
            </div>
          </div>
        </details>
      </a-spin>
    </SectionCard>

    <SectionCard
        title="已记住的长期授权"
        description="当你选择“始终允许”或“始终拒绝”时，Humbert 会记住这个选择。你可以随时在这里撤销。"
    >
      <template #actions>
        <a-button
            size="small"
            status="danger"
            :disabled="persistentAllowCount === 0"
            :loading="clearingPersistent"
            @click="confirmClearPersistentAllows"
        >
          撤销全部长期授权
        </a-button>
      </template>

      <a-spin :loading="loading">
        <EmptyState
            v-if="persistentRules.length === 0"
            title="暂无 Agent 长期规则"
            description="Agent 选择“始终允许”或“始终拒绝”后，规则会显示在这里。"
            compact
        />

        <div
            v-else
            class="rule-list"
        >
          <article
              v-for="rule in persistentRules"
              :key="rule.id"
              class="rule-item h-list-row"
          >
            <div class="rule-item__main">
              <div class="rule-item__heading">
                <div class="rule-item__title">{{ rule.agentName }}</div>
                <StatusPill
                    :label="rule.toolName === 'run_command' && rule.action === 'allow' && !rule.invocationFingerprint ? '已失效' : actionText(rule.action)"
                    :tone="rule.toolName === 'run_command' && rule.action === 'allow' && !rule.invocationFingerprint ? 'neutral' : rule.action === 'deny' ? 'danger' : 'success'"
                    :dot="false"
                />
              </div>

              <div class="rule-item__target">{{ ruleTarget(rule) }}</div>
              <div v-if="rule.toolName === 'run_command' && rule.action === 'allow' && !rule.invocationFingerprint" class="rule-item__condition">
                旧版命令授权不再生效；后续按当前执行策略处理，可以撤销这条规则。
              </div>
              <div v-if="rule.invocationFingerprint" class="rule-item__condition">
                精确调用标识 · {{ shortFingerprint(rule.invocationFingerprint) }} · 参数或工作目录变化后重新询问
              </div>
              <div v-if="rule.executable" class="rule-item__condition">
                执行文件 · {{ rule.executable }}
              </div>
              <div v-if="rule.skillIdentity" class="rule-item__condition">
                Skill 内容标识 · {{ shortFingerprint(rule.skillIdentity) }}
              </div>
              <div v-if="rule.mcpServerFingerprint" class="rule-item__condition">
                连接器标识 · {{ shortFingerprint(rule.mcpServerFingerprint) }}
              </div>
              <div v-if="rule.sandboxFingerprint && rule.action === 'allow'" class="rule-item__condition">
                安全配置标识 · {{ shortFingerprint(rule.sandboxFingerprint) }} · 安全边界变化后会自动重新询问
              </div>
              <div class="rule-item__meta">
                {{ scopeText(rule.scope) }}
                <template v-if="rule.createdAt">
                  · {{ formatCreatedAt(rule.createdAt) }}
                </template>
              </div>
            </div>

            <a-button
                size="small"
                status="danger"
                :loading="deletingRuleID === rule.id"
                @click="confirmDeletePersistent(rule)"
            >
              撤销
            </a-button>
          </article>
        </div>
      </a-spin>
    </SectionCard>

    <SectionCard
        title="当前会话临时授权"
        :description="currentSessionID ? '只展示当前会话中临时记住的允许规则。应用重启后会自动失效。' : '当前没有打开会话，因此没有可展示的临时授权。'"
    >
      <template #actions>
        <a-button
            size="small"
            status="danger"
            :disabled="!currentSessionID || sessionRules.length === 0"
            :loading="clearingSession"
            @click="confirmClearSession"
        >
          清除当前会话授权
        </a-button>
      </template>

      <a-spin :loading="loading">
        <EmptyState
            v-if="sessionRules.length === 0"
            title="暂无本会话临时授权"
            description="当前会话中临时记住的允许规则会显示在这里。"
            compact
        />

        <div
            v-else
            class="rule-list"
        >
          <article
              v-for="rule in sessionRules"
              :key="rule.id"
              class="rule-item h-list-row"
          >
            <div class="rule-item__main">
              <div class="rule-item__heading">
                <div class="rule-item__title">{{ rule.agentName }}</div>
                <StatusPill :label="rule.toolName === 'run_command' && !rule.invocationFingerprint ? '已失效' : '允许'" :tone="rule.toolName === 'run_command' && !rule.invocationFingerprint ? 'neutral' : 'success'" :dot="false" />
              </div>
              <div class="rule-item__target">{{ ruleTarget(rule) }}</div>
              <div v-if="rule.toolName === 'run_command' && !rule.invocationFingerprint" class="rule-item__condition">
                旧版命令授权不再生效；后续按当前执行策略处理。
              </div>
              <div v-if="rule.invocationFingerprint" class="rule-item__condition">
                精确调用标识 · {{ shortFingerprint(rule.invocationFingerprint) }} · 参数或工作目录变化后重新询问
              </div>
              <div v-if="rule.executable" class="rule-item__condition">
                执行文件 · {{ rule.executable }}
              </div>
              <div v-if="rule.skillIdentity" class="rule-item__condition">
                Skill 内容标识 · {{ shortFingerprint(rule.skillIdentity) }}
              </div>
              <div v-if="rule.mcpServerFingerprint" class="rule-item__condition">
                连接器标识 · {{ shortFingerprint(rule.mcpServerFingerprint) }}
              </div>
              <div v-if="rule.sandboxFingerprint && rule.action === 'allow'" class="rule-item__condition">
                安全配置标识 · {{ shortFingerprint(rule.sandboxFingerprint) }} · 安全边界变化后会自动重新询问
              </div>
              <div class="rule-item__meta">
                {{ scopeText(rule.scope) }}
                <template v-if="rule.createdAt">
                  · {{ formatCreatedAt(rule.createdAt) }}
                </template>
              </div>
            </div>

            <a-button
                size="small"
                status="danger"
                :loading="deletingRuleID === rule.id"
                @click="confirmDeleteSession(rule)"
            >
              撤销
            </a-button>
          </article>
        </div>
      </a-spin>
    </SectionCard>

    <SectionCard
        tone="warning"
        title="关于安全边界"
        description="操作确认只决定是否需要向你询问，不会扩大安全沙盒的文件或网络边界。即使你选择“始终允许”，受保护目录和其他底层限制仍然有效。"
    />
  </div>
</template>

<style scoped>
.permission-settings {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.settings-card__header-actions {
  display: flex;
  flex: 0 0 auto;
  gap: 8px;
}

.simple-confirmation-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  padding: 14px;
  border: 1px solid var(--h-border);
  border-radius: 10px;
  background: var(--h-bg);
}

.simple-confirmation-row > div {
  min-width: 0;
}

.permission-advanced {
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px solid var(--h-border);
}

.permission-advanced > summary {
  width: fit-content;
  cursor: pointer;
  color: var(--h-text-secondary);
  font-size: 11px;
  font-weight: 650;
  user-select: none;
}

.policy-form {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 16px;
}

.policy-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  padding: 12px;
  border-radius: 9px;
  background: var(--h-bg);
}

.policy-row > div:first-child {
  min-width: 0;
}

.policy-row__title {
  color: var(--h-text);
  font-size: 12px;
  font-weight: 600;
}

.policy-row__description {
  margin-top: 4px;
  color: var(--h-text-muted);
  font-size: 11px;
  line-height: 1.55;
}

.policy-control {
  width: 150px;
  flex: 0 0 150px;
}

.timeout-control {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--h-text-muted);
  font-size: 11px;
}

.rule-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 14px;
}

.rule-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.rule-item__main {
  min-width: 0;
}

.rule-item__heading {
  display: flex;
  align-items: center;
  gap: 8px;
}

.rule-item__title {
  color: var(--h-text);
  font-size: 12px;
  font-weight: 600;
}

.rule-item__target {
  margin-top: 4px;
  color: var(--h-text);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
  overflow-wrap: anywhere;
}

.rule-item__condition {
  margin-top: 4px;
  color: var(--h-text-muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 9px;
  overflow-wrap: anywhere;
}

.rule-item__meta {
  margin-top: 4px;
  color: var(--h-text-muted);
  font-size: 11px;
}

@media (max-width: 760px) {
  .policy-row {
    align-items: stretch;
    flex-direction: column;
  }

  .settings-card__header-actions {
    width: 100%;
  }

  .policy-control {
    width: 100%;
    flex-basis: auto;
  }
}
</style>
