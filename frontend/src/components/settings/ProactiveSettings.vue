<script setup>
import { computed, onMounted, reactive, watch } from "vue";
import { Message } from "@arco-design/web-vue";

import { useAgentStore } from "../../stores/agents.js";
import { useProactiveStore } from "../../stores/proactive.js";
import SectionCard from "../ui/SectionCard.vue";

const proactiveStore = useProactiveStore();
const agentStore = useAgentStore();

const ruleDefinitions = [
  { key: "task_failed", title: "任务失败", description: "后台任务执行失败时。" },
  { key: "task_timed_out", title: "任务超时", description: "后台任务超过运行上限时。" },
  { key: "task_interrupted", title: "任务中断", description: "应用退出或运行被中断时。" },
  { key: "task_succeeded", title: "任务完成", description: "后台任务成功完成时。" },
  { key: "task_long_running_finished", title: "长时间任务完成", description: "运行超过 5 分钟的后台任务完成时。" },
  { key: "task_waiting_approval", title: "后台任务等待确认", description: "后台任务需要你确认高风险操作时。" },
  { key: "approval_pending", title: "聊天操作等待确认", description: "普通会话中的 Agent 需要操作确认时。" },
  { key: "workspace_changed", title: "工作区变化", description: "心跳发现 Agent 工作区文件变化时。" },
];

const actionOptions = [
  { value: "ignore", label: "忽略" },
  { value: "notify", label: "通知我" },
  { value: "run_agent", label: "交给 Agent 处理" },
];

function defaultRules() {
  return Object.fromEntries(ruleDefinitions.map((definition) => [definition.key, {
    enabled: false,
    action: "ignore",
    agentID: "",
    agentPrompt: "",
    cooldownSeconds: 0,
  }]));
}

const form = reactive({
  enabled: true,
  heartbeatIntervalMinutes: 5,
  quietHours: { enabled: false, start: "22:00", end: "08:00", timeZone: "" },
  rules: defaultRules(),
});

const recentRecords = computed(() => proactiveStore.records.slice(0, 12));

function copySettings(value) {
  if (!value) return;
  form.enabled = Boolean(value.enabled);
  form.heartbeatIntervalMinutes = value.heartbeatIntervalMinutes || 5;
  Object.assign(form.quietHours, value.quietHours || {});
  form.rules = defaultRules();
  for (const definition of ruleDefinitions) {
    const source = value.rules?.[definition.key] || {};
    form.rules[definition.key] = {
      enabled: Boolean(source.enabled),
      action: source.action || "ignore",
      agentID: source.agentID || "",
      agentPrompt: source.agentPrompt || "",
      cooldownSeconds: Number(source.cooldownSeconds) || 0,
    };
  }
}

watch(() => proactiveStore.settings, copySettings, { immediate: true });

async function save() {
  try {
    await proactiveStore.save(JSON.parse(JSON.stringify(form)));
    Message.success("主动助手设置已保存");
  } catch (error) {
    Message.error(error?.message ?? String(error));
  }
}

async function runHeartbeat() {
  try {
    await proactiveStore.runHeartbeat();
    Message.success("巡检已完成");
  } catch (error) {
    Message.error(error?.message ?? String(error));
  }
}

async function enableDesktopNotifications() {
  if (!("Notification" in globalThis)) {
    Message.warning("当前桌面 WebView 不支持系统通知 API，将继续使用应用内通知");
    return;
  }
  try {
    const result = await globalThis.Notification.requestPermission();
    if (result === "granted") Message.success("系统通知已允许");
    else Message.warning("系统通知未获允许，将继续使用应用内通知");
  } catch (error) {
    Message.warning(error?.message ?? "无法请求系统通知权限");
  }
}

function formatTime(value) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function recordTitle(record) {
  return record?.event?.title || record?.event?.kind || "主动事件";
}

function recordStatus(record) {
  const map = {
    ignored: "已忽略",
    deferred: "免打扰期间延后",
    executing: "处理中",
    succeeded: "已处理",
    failed: "处理失败",
  };
  return map[record?.status] || record?.status || "未知";
}

onMounted(async () => {
  try {
    await Promise.all([
      proactiveStore.settings ? Promise.resolve() : proactiveStore.load(),
      agentStore.items.length ? Promise.resolve() : agentStore.load(),
    ]);
  } catch (error) {
    Message.error(error?.message ?? String(error));
  }
});
</script>

<template>
  <div class="proactive-settings">
    <SectionCard title="主动助手" description="让 Humbert 在没有新消息时也能巡检任务、审批与工作区变化。">
      <div class="setting-row setting-row--switch">
        <div>
          <strong>启用主动助手</strong>
          <p>关闭后不会处理主动事件，也不会执行心跳巡检。</p>
        </div>
        <a-switch v-model="form.enabled" />
      </div>

      <div class="setting-grid">
        <label>
          <span>心跳间隔（分钟）</span>
          <a-input-number v-model="form.heartbeatIntervalMinutes" :min="1" :max="1440" />
        </label>
        <div class="status-box">
          <span>最近巡检：{{ formatTime(proactiveStore.status?.lastHeartbeatAt) }}</span>
          <span>下次巡检：{{ formatTime(proactiveStore.status?.nextHeartbeatAt) }}</span>
        </div>
      </div>

      <div class="button-row">
        <a-button :disabled="!form.enabled" :loading="proactiveStore.runningHeartbeat" @click="runHeartbeat">立即巡检</a-button>
        <a-button @click="enableDesktopNotifications">允许系统通知</a-button>
      </div>
    </SectionCard>

    <SectionCard title="免打扰" description="免打扰期间，主动事件通知和 Agent 主动执行会延后到下一次心跳。">
      <div class="setting-row setting-row--switch">
        <div><strong>启用免打扰</strong></div>
        <a-switch v-model="form.quietHours.enabled" />
      </div>
      <div class="setting-grid setting-grid--three">
        <label><span>开始</span><a-input v-model="form.quietHours.start" placeholder="22:00" /></label>
        <label><span>结束</span><a-input v-model="form.quietHours.end" placeholder="08:00" /></label>
        <label><span>时区</span><a-input v-model="form.quietHours.timeZone" :placeholder="Intl.DateTimeFormat().resolvedOptions().timeZone" /></label>
      </div>
    </SectionCard>

    <SectionCard title="事件规则" description="简单事件直接通知；复杂事件可以交给一个 Agent 继续判断和处理。">
      <div class="rules">
        <div v-for="definition in ruleDefinitions" :key="definition.key" class="rule-card">
          <div class="rule-card__header">
            <div>
              <strong>{{ definition.title }}</strong>
              <p>{{ definition.description }}</p>
            </div>
            <a-switch v-model="form.rules[definition.key].enabled" />
          </div>

          <div class="rule-card__controls">
            <label>
              <span>动作</span>
              <a-select v-model="form.rules[definition.key].action" :options="actionOptions" />
            </label>
            <label>
              <span>冷却（秒）</span>
              <a-input-number v-model="form.rules[definition.key].cooldownSeconds" :min="0" :max="86400" />
            </label>
          </div>

          <template v-if="form.rules[definition.key].action === 'run_agent'">
            <label class="rule-card__full">
              <span>处理 Agent</span>
              <a-select v-model="form.rules[definition.key].agentID" allow-clear placeholder="默认使用事件所属 Agent">
                <a-option v-for="agent in agentStore.items" :key="agent.id" :value="agent.id">{{ agent.name }}</a-option>
              </a-select>
            </label>
            <label class="rule-card__full">
              <span>处理说明</span>
              <a-textarea v-model="form.rules[definition.key].agentPrompt" :auto-size="{ minRows: 2, maxRows: 5 }" placeholder="为空时使用 Humbert 默认主动处理提示" />
            </label>
          </template>
        </div>
      </div>
    </SectionCard>

    <SectionCard title="最近主动事件" description="用于确认事件是否被忽略、延后、通知或交给 Agent 处理。">
      <div v-if="recentRecords.length" class="records">
        <div v-for="record in recentRecords" :key="record.id" class="record-row">
          <div>
            <strong>{{ recordTitle(record) }}</strong>
            <p>{{ record?.event?.summary || "—" }}</p>
          </div>
          <div class="record-row__meta">
            <span>{{ recordStatus(record) }}</span>
            <span>{{ formatTime(record.updated_at || record.updatedAt) }}</span>
          </div>
        </div>
      </div>
      <div v-else class="empty-records">还没有主动事件。</div>
    </SectionCard>

    <div class="save-row">
      <a-button type="primary" :loading="proactiveStore.saving" @click="save">保存设置</a-button>
    </div>
  </div>
</template>

<style scoped>
.proactive-settings { display: grid; gap: 18px; }
.setting-row { display: flex; align-items: center; justify-content: space-between; gap: 24px; }
.setting-row p, .rule-card p, .record-row p { margin: 5px 0 0; color: var(--h-text-muted); font-size: 13px; line-height: 1.5; }
.setting-grid { display: grid; grid-template-columns: minmax(180px, 260px) minmax(0, 1fr); gap: 16px; margin-top: 18px; }
.setting-grid--three { grid-template-columns: repeat(3, minmax(0, 1fr)); }
.setting-grid label, .rule-card label { display: grid; gap: 7px; color: var(--h-text-muted); font-size: 13px; }
.status-box { display: grid; align-content: center; gap: 4px; color: var(--h-text-muted); font-size: 13px; }
.button-row, .save-row { display: flex; gap: 10px; margin-top: 16px; }
.save-row { justify-content: flex-end; }
.rules { display: grid; gap: 12px; }
.rule-card { padding: 14px; border: 1px solid var(--h-border); border-radius: 12px; background: var(--h-surface); }
.rule-card__header { display: flex; justify-content: space-between; gap: 20px; }
.rule-card__controls { display: grid; grid-template-columns: minmax(180px, 1fr) 160px; gap: 12px; margin-top: 14px; }
.rule-card__full { margin-top: 12px; }
.records { display: grid; gap: 10px; }
.record-row { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 16px; padding: 11px 0; border-bottom: 1px solid var(--h-border); }
.record-row:last-child { border-bottom: 0; }
.record-row__meta { display: grid; justify-items: end; align-content: center; gap: 4px; color: var(--h-text-muted); font-size: 12px; }
.empty-records { color: var(--h-text-muted); font-size: 13px; }
@media (max-width: 900px) { .setting-grid, .setting-grid--three, .rule-card__controls { grid-template-columns: 1fr; } }
</style>
