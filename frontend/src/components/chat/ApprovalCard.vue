<script setup>
import {
  computed,
} from "vue";

import {
  Modal,
} from "@arco-design/web-vue";

const props =
    defineProps({
      approval: {
        type: Object,
        required: true,
      },

      resolving: {
        type: Boolean,
        default: false,
      },

      error: {
        type: String,
        default: "",
      },

      agentName: {
        type: String,
        default: "",
      },

      sessionLabel: {
        type: String,
        default: "本会话",
      },
    });

const emit =
    defineEmits([
      "decide",
    ]);

const presentation =
    computed(() => (
        props.approval?.presentation ?? {}
    ));

const fields =
    computed(() => (
        Array.isArray(
            presentation.value?.fields,
        )
            ? presentation.value.fields
            : []
    ));

const isMCPApproval =
    computed(() => (
        fields.value.some((field) => field?.label === "MCP Server") ||
        String(props.approval?.toolName || "").startsWith("mcp_")
    ));

const mcpServerName =
    computed(() => (
        fields.value.find((field) => field?.label === "MCP Server")?.value || "MCP Server"
    ));

const mcpRawToolName =
    computed(() => (
        fields.value.find((field) => field?.label === "Tool")?.value || props.approval?.toolName || "Tool"
    ));

const riskText =
    computed(() => {
      switch (props.approval?.risk) {
        case "exec":
          return "执行程序";
        case "write":
          return "修改数据";
        case "read":
          return "读取数据";
        default:
          return "受控操作";
      }
    });

const persistentTarget =
    computed(() => {
      if (isMCPApproval.value) {
        return `MCP Server “${mcpServerName.value}” 的 Tool “${mcpRawToolName.value}”`;
      }
      const command =
          fields.value.find(
              (field) =>
                  ["程序", "命令", "Program"].includes(
                      field?.label,
                  ),
          )?.value;
      return command
          ? `“${command}”`
          : `工具 “${props.approval?.toolName || "当前工具"}”`;
    });

const reusableAllowAvailable =
    computed(() => (
        !["install_skill", "schedule_task"].includes(props.approval?.toolName)
    ));

const agentScopeText =
    computed(() => {
      const name = String(props.agentName || "").trim();
      return name
          ? `子 Agent “${name}”`
          : "当前 Agent";
    });

const sessionAllowText =
    computed(() => `${props.sessionLabel || "本会话"}允许`);

const sessionScopeText =
    computed(() => (
        props.sessionLabel === "本会话"
            ? "当前会话"
            : "这个子 Agent 会话"
    ));

const approvalHint =
    computed(() => {
      if (props.approval?.toolName === "schedule_task") {
        return "请核对时间、时区和执行内容。确认只创建这一项任务；后续安排仍需单独确认。";
      }
      if (!reusableAllowAvailable.value) {
        return "远程 Skill 安装每次都需要单独确认；不会保存可自动安装其他 URL 的会话级或 Agent 级允许规则。";
      }
      if (isMCPApproval.value) {
        return `“${sessionAllowText.value}”只作用于${sessionScopeText.value}；Agent 长期规则只作用于${agentScopeText.value}。MCP 规则还会绑定当前 Server 的安全指纹，配置变化后旧规则不会继续自动生效。`;
      }
      return `“${sessionAllowText.value}”只作用于${sessionScopeText.value}；“Agent 始终允许 / 拒绝”会保存为${agentScopeText.value}的长期规则。`;
    });

function emitDecision(decision) {
  if (props.resolving) {
    return;
  }
  emit(
      "decide",
      decision,
  );
}

/**
 * 提交 Approval 决策。
 *
 * Exec + Agent 持久 Allow 会在本地长期生效，因此额外做一次明确确认。这里只是 UX 防误触，
 * 真正的 Permission Action、Scope 与 Tool 参数仍由 Go 后端校验；前端不会回传 raw Arguments。
 */
function decide(decision) {
  if (props.resolving) {
    return;
  }

  if (
      decision === "allow_agent" &&
      props.approval?.risk === "exec"
  ) {
    Modal.warning({
      title: "长期允许执行程序？",
      content:
          `将为${agentScopeText.value}长期允许 ${persistentTarget.value}。这个授权在应用重启后仍然有效，你可以随时在「设置 → 操作确认」中撤销。`,
      hideCancel: false,
      okText: "确认长期允许",
      cancelText: "取消",
      onOk: () => {
        emitDecision(decision);
      },
    });
    return;
  }

  if (decision === "deny_agent") {
    Modal.warning({
      title: `让${agentScopeText.value}始终拒绝？`,
      content:
          `后续${agentScopeText.value}对 ${persistentTarget.value} 的匹配调用将直接被拒绝，不再弹出审批。你可以在「设置 → 操作确认」中撤销这条拒绝规则。`,
      hideCancel: false,
      okText: "确认始终拒绝",
      cancelText: "取消",
      onOk: () => {
        emitDecision(decision);
      },
    });
    return;
  }

  emitDecision(decision);
}
</script>

<template>
  <section class="approval-card">
    <div class="approval-card__heading">
      <span
          class="approval-card__icon"
          aria-hidden="true"
      >!</span>

      <div class="approval-card__heading-text">
        <div class="approval-card__title">
          {{ presentation.title || "请求执行工具" }}
        </div>

        <div class="approval-card__risk">
          {{ riskText }} · 需要你的确认
        </div>
      </div>

      <span v-if="isMCPApproval" class="approval-card__source">MCP</span>
    </div>

    <p
        v-if="presentation.description"
        class="approval-card__description"
    >
      {{ presentation.description }}
    </p>

    <dl
        v-if="fields.length > 0"
        class="approval-card__fields"
    >
      <template
          v-for="(field, index) in fields"
          :key="`${field.label || 'field'}-${index}`"
      >
        <dt>{{ field.label }}</dt>
        <dd>{{ field.value }}</dd>
      </template>
    </dl>

    <div
        v-if="error"
        class="approval-card__error"
    >
      {{ error }}
    </div>

    <div class="approval-card__actions">
      <a-button
          size="small"
          status="danger"
          :disabled="resolving"
          @click="decide('deny')"
      >
        拒绝
      </a-button>

      <a-button
          v-if="props.approval?.toolName !== 'schedule_task'"
          size="small"
          status="danger"
          :disabled="resolving"
          @click="decide('deny_agent')"
      >
        Agent 始终拒绝
      </a-button>

      <a-button
          size="small"
          :loading="resolving"
          @click="decide('allow_once')"
      >
        {{ props.approval?.toolName === "schedule_task" ? "确认创建" : "允许一次" }}
      </a-button>

      <a-button
          v-if="reusableAllowAvailable"
          size="small"
          :disabled="resolving"
          @click="decide('allow_session')"
      >
        {{ sessionAllowText }}
      </a-button>

      <a-button
          v-if="reusableAllowAvailable"
          size="small"
          type="primary"
          :disabled="resolving"
          @click="decide('allow_agent')"
      >
        Agent 始终允许
      </a-button>
    </div>

    <div class="approval-card__hint">
      {{ approvalHint }}
    </div>
  </section>
</template>

<style scoped>
.approval-card {
  width: min(100%, 720px);
  margin: 4px 0 16px;
  padding: 14px;
  border: 1px solid var(--h-border);
  border-radius: 12px;
  background: var(--h-surface);
}

.approval-card__heading {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

.approval-card__icon {
  display: grid;
  width: 24px;
  height: 24px;
  flex: 0 0 24px;
  place-items: center;
  border: 1px solid var(--h-warning);
  border-radius: 50%;
  color: var(--h-warning);
  font-size: 12px;
  font-weight: 700;
}

.approval-card__heading-text {
  min-width: 0;
}

.approval-card__source {
  margin-left: auto;
  padding: 3px 7px;
  border: 1px solid var(--h-accent-border);
  border-radius: 999px;
  background: var(--h-accent-soft);
  color: var(--h-accent-hover);
  font-size: 9px;
  font-weight: 600;
  letter-spacing: .04em;
}

.approval-card__title {
  color: var(--h-text);
  font-size: 13px;
  font-weight: 600;
}

.approval-card__risk,
.approval-card__hint {
  margin-top: 3px;
  color: var(--h-text-muted);
  font-size: 11px;
  line-height: 1.5;
}

.approval-card__description {
  margin: 11px 0 0;
  color: var(--h-text-secondary);
  font-size: 12px;
  line-height: 1.65;
}

.approval-card__fields {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: 6px 12px;
  margin: 12px 0 0;
  padding: 10px 12px;
  border-radius: 8px;
  background: var(--h-bg);
  font-size: 11px;
}

.approval-card__fields dt {
  color: var(--h-text-muted);
}

.approval-card__fields dd {
  min-width: 0;
  margin: 0;
  color: var(--h-text);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  overflow-wrap: anywhere;
}

.approval-card__error {
  margin-top: 10px;
  color: var(--h-danger);
  font-size: 11px;
}

.approval-card__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 14px;
}

.approval-card__hint {
  margin-top: 10px;
}
</style>
