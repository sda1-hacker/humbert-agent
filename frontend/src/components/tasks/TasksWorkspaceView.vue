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
  confirmAction,
} from "../../utils/confirm.js";

import ApprovalCard from "../chat/ApprovalCard.vue";
import EmptyState from "../ui/EmptyState.vue";

import {
  resolveApproval,
} from "../../api/chat.js";

import {
  useAgentStore,
} from "../../stores/agents.js";

import {
  useTaskStore,
} from "../../stores/tasks.js";

const emit = defineEmits(["open-session"]);

const taskStore = useTaskStore();
const agentStore = useAgentStore();

const creating = ref(false);
const saving = ref(false);
const statusSaving = ref(false);
const running = ref(false);
const deletingTask = ref(false);
const deletingRunID = ref("");
const clearingRuns = ref(false);
const approvalID = ref("");
const approvalError = ref("");

const weekdays = [
  { value: 1, label: "一" },
  { value: 2, label: "二" },
  { value: 3, label: "三" },
  { value: 4, label: "四" },
  { value: 5, label: "五" },
  { value: 6, label: "六" },
  { value: 0, label: "日" },
];

function localTimeZone() {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}

function emptyForm() {
  return {
    agentID: agentStore.selectedID || agentStore.items[0]?.id || "",
    name: "",
    prompt: "",
    execution: "agent",
    // 旧任务没有 conversationMode 时后端也会按 isolated 处理；前端同样以独立对话为默认。
    conversationMode: "isolated",
    enabled: true,
    scheduleType: "manual",
    timeZone: localTimeZone(),
    runAt: "",
    intervalMinutes: 60,
    timeOfDay: "09:00",
    weekdays: [1],
    misfirePolicy: "run_once",
    overlapPolicy: "skip",
    maxDurationSeconds: 1800,
    maxModelCalls: 20,
    maxToolCalls: 50,
    maxTotalTokens: 100000,
    maxAttempts: 1,
    retryDelaySeconds: 30,
  };
}

const form = reactive(emptyForm());

const selectedTask = computed(() => taskStore.selectedTask);
const selectedRuns = computed(() => taskStore.selectedRuns);

function toScheduleDateTime(value, timeZone) {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  try {
    const parts = new Intl.DateTimeFormat("en-CA", {
      timeZone: timeZone || localTimeZone(),
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      hourCycle: "h23",
    }).formatToParts(date);
    const part = (type) => parts.find((item) => item.type === type)?.value || "";
    return `${part("year")}-${part("month")}-${part("day")}T${part("hour")}:${part("minute")}`;
  } catch {
    return "";
  }
}

function applyTask(task) {
  if (!task) {
    return;
  }
  Object.assign(form, {
    agentID: task.agentID,
    name: task.name,
    prompt: task.prompt,
    execution: task.execution || "agent",
    conversationMode: task.conversationMode || "isolated",
    enabled: task.status === "active",
    scheduleType: task.schedule?.type || "manual",
    timeZone: task.schedule?.timeZone || localTimeZone(),
    runAt: toScheduleDateTime(task.schedule?.runAt, task.schedule?.timeZone),
    intervalMinutes: task.schedule?.intervalMinutes || 60,
    timeOfDay: task.schedule?.timeOfDay || "09:00",
    weekdays: Array.isArray(task.schedule?.weekdays)
        ? [...task.schedule.weekdays]
        : [1],
    misfirePolicy: task.schedule?.misfirePolicy || "run_once",
    overlapPolicy: task.schedule?.overlapPolicy || "skip",
    maxDurationSeconds: task.limits?.maxDurationSeconds || 1800,
    maxModelCalls: task.limits?.maxModelCalls || 20,
    maxToolCalls: task.limits?.maxToolCalls || 50,
    maxTotalTokens: task.limits?.maxTotalTokens || 100000,
    maxAttempts: task.limits?.maxAttempts || 1,
    retryDelaySeconds: task.limits?.retryDelaySeconds || 30,
  });
}

// 切换任务时加载完整表单；同一任务的后台状态刷新只同步 enabled，避免任务事件
// 或暂停 API 返回时覆盖用户尚未保存的名称、提示词和计划编辑。
watch(() => selectedTask.value?.id, () => {
  if (!creating.value) {
    applyTask(selectedTask.value);
  }
}, { immediate: true });

watch(() => selectedTask.value?.status, (status) => {
  if (!creating.value && status) {
    form.enabled = status === "active";
  }
});

function startCreating() {
  creating.value = true;
  Object.assign(form, emptyForm());
}

function cancelCreating() {
  creating.value = false;
  applyTask(selectedTask.value);
}

async function selectTask(task) {
  creating.value = false;
  try {
    await taskStore.select(task.id);
  } catch (error) {
    Message.error(error?.message ?? String(error));
  }
}

function toggleWeekday(day) {
  if (form.weekdays.includes(day)) {
    form.weekdays = form.weekdays.filter((value) => value !== day);
  } else {
    form.weekdays = [...form.weekdays, day];
  }
}

function requestFromForm() {
  const schedule = {
    type: form.scheduleType,
    timeZone: form.scheduleType === "manual" ? "" : form.timeZone,
    // 不附加浏览器时区偏移；后端会按用户选择的 IANA 时区解析这个 wall-clock 值。
    runAt: form.scheduleType === "once" ? form.runAt : "",
    intervalMinutes: form.scheduleType === "interval"
        ? Number(form.intervalMinutes)
        : 0,
    timeOfDay: ["daily", "weekly"].includes(form.scheduleType)
        ? form.timeOfDay
        : "",
    weekdays: form.scheduleType === "weekly" ? [...form.weekdays] : [],
    misfirePolicy: form.misfirePolicy,
    overlapPolicy: form.overlapPolicy,
  };
  return {
    agentID: form.agentID,
    name: form.name.trim(),
    prompt: form.prompt.trim(),
    execution: form.execution,
    // 仅通知任务没有 Session；这里显式归一成 isolated，避免隐藏表单值影响后端语义。
    conversationMode: form.execution === "agent" ? form.conversationMode : "isolated",
    status: form.enabled ? "active" : "paused",
    schedule,
    limits: {
      maxDurationSeconds: Number(form.maxDurationSeconds),
      maxModelCalls: Number(form.maxModelCalls),
      maxToolCalls: Number(form.maxToolCalls),
      maxTotalTokens: Number(form.maxTotalTokens),
      maxAttempts: Number(form.maxAttempts),
      retryDelaySeconds: Number(form.retryDelaySeconds),
    },
  };
}

async function save() {
  if (!form.agentID || !form.name.trim() || !form.prompt.trim()) {
    Message.warning(`请选择 Agent，并填写任务名称和${form.execution === "notification" ? "通知内容" : "提示词"}`);
    return;
  }
  if (form.scheduleType === "once" && !form.runAt) {
    Message.warning("请选择单次运行时间");
    return;
  }
  if (form.scheduleType === "weekly" && form.weekdays.length === 0) {
    Message.warning("每周任务至少选择一天");
    return;
  }
  saving.value = true;
  try {
    if (creating.value) {
      await taskStore.create(requestFromForm());
      creating.value = false;
      Message.success("任务已创建");
    } else if (selectedTask.value) {
      const taskID = selectedTask.value.id;
      const updated = await taskStore.update(taskID, requestFromForm());
      if (taskStore.selectedID === taskID) {
        applyTask(updated);
      }
      Message.success("任务已保存");
    }
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    saving.value = false;
  }
}

async function changeTaskEnabled(enabled) {
  if (creating.value || !selectedTask.value || statusSaving.value) {
    return;
  }
  const taskID = selectedTask.value.id;
  const previous = selectedTask.value.status === "active";
  const hadActiveRun = selectedRuns.value.some((run) => canCancel(run));
  if (Boolean(enabled) === previous) {
    return;
  }

  statusSaving.value = true;
  try {
    await taskStore.setStatus(taskID, enabled ? "active" : "paused");
    if (enabled) {
      Message.success("任务计划已恢复");
    } else {
      Message.success(hadActiveRun
          ? "任务计划已暂停；当前运行不会自动中止"
          : "任务计划已暂停");
    }
  } catch (error) {
    if (taskStore.selectedID === taskID) {
      form.enabled = previous;
    }
    Message.error(error?.message ?? String(error));
  } finally {
    statusSaving.value = false;
  }
}

async function runNow() {
  if (!selectedTask.value || running.value) {
    return;
  }
  running.value = true;
  try {
    await taskStore.runNow(selectedTask.value.id);
    Message.success("任务已进入运行队列");
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    running.value = false;
  }
}

async function deleteSelected() {
  if (!selectedTask.value || deletingTask.value) {
    return;
  }
  const taskID = selectedTask.value.id;
  const taskName = selectedTask.value.name;
  const confirmed = await confirmAction({
    title: "删除任务",
    message: `永久删除“${taskName}”？任务配置、全部运行历史及其对应对话都会被删除，此操作无法撤销。`,
    confirmText: "删除",
    danger: true,
  });
  if (!confirmed) {
    return;
  }
  deletingTask.value = true;
  try {
    await taskStore.remove(taskID);
    Message.success("任务已删除");
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    deletingTask.value = false;
  }
}

async function deleteRun(run) {
  if (!run?.id || deletingRunID.value) {
    return;
  }
  const task = taskStore.items.find((item) => item.id === run.taskID);
  const continuous = (task?.conversationMode || "isolated") === "continuous";
  const confirmed = await confirmAction({
    title: "删除运行记录",
    message: continuous
        ? "这条运行历史会被永久删除；该任务的连续对话仍会保留，其他运行不会受到影响。"
        : "这条运行历史及其独立对话会被永久删除，此操作无法撤销。",
    confirmText: "删除",
    danger: true,
  });
  if (!confirmed) {
    return;
  }
  deletingRunID.value = run.id;
  try {
    await taskStore.removeRun(run.taskID, run.id);
    Message.success("运行记录已删除");
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    deletingRunID.value = "";
  }
}

async function clearRuns() {
  if (!selectedTask.value || selectedRuns.value.length === 0 || clearingRuns.value) {
    return;
  }
  const taskID = selectedTask.value.id;
  const continuous = (selectedTask.value.conversationMode || "isolated") === "continuous";
  const confirmed = await confirmAction({
    title: "清空运行历史",
    message: continuous
        ? "全部运行记录会被永久清空，但当前连续对话会保留，后续运行会继续使用它。"
        : "全部运行历史及其独立对话都会被永久删除，此操作无法撤销。",
    confirmText: "清空",
    danger: true,
  });
  if (!confirmed) {
    return;
  }
  clearingRuns.value = true;
  try {
    await taskStore.clearRuns(taskID);
    Message.success("运行历史已清空");
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    clearingRuns.value = false;
  }
}

async function cancelRun(run) {
  try {
    await taskStore.cancelRun(run.taskID, run.id);
    Message.success("已请求取消运行");
  } catch (error) {
    Message.error(error?.message ?? String(error));
  }
}

async function decideApproval(run, decision) {
  if (!run.approval?.id || approvalID.value) {
    return;
  }
  approvalID.value = run.approval.id;
  approvalError.value = "";
  try {
    await resolveApproval(run.approval.id, decision);
    await taskStore.loadRuns(run.taskID);
  } catch (error) {
    approvalError.value = error?.message ?? String(error);
  } finally {
    approvalID.value = "";
  }
}

function agentName(agentID) {
  return agentStore.items.find((agent) => agent.id === agentID)?.name || "未知 Agent";
}

function statusText(status) {
  return ({
    queued: "排队中",
    starting: "启动中",
    running: "运行中",
    waiting_approval: "等待确认",
    succeeded: "成功",
    failed: "失败",
    cancelled: "已取消",
    timed_out: "已超时",
    interrupted: "已中断",
    skipped: "已跳过",
  })[status] || status;
}

function conversationModeText(value) {
  return value === "continuous" ? "连续对话" : "独立对话";
}

function scheduleText(task) {
  const schedule = task.schedule || {};
  if (schedule.type === "manual") return "仅手动运行";
  if (schedule.type === "once") return `单次 · ${formatTime(schedule.runAt)}`;
  if (schedule.type === "interval") return `每 ${schedule.intervalMinutes} 分钟`;
  if (schedule.type === "daily") return `每天 ${schedule.timeOfDay}`;
  if (schedule.type === "weekly") {
    const labels = (schedule.weekdays || [])
        .map((day) => weekdays.find((item) => item.value === day)?.label)
        .filter(Boolean)
        .join("、");
    return `每周${labels} ${schedule.timeOfDay}`;
  }
  return schedule.type;
}

function formatTime(value) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(date);
}

function canCancel(run) {
  return ["queued", "starting", "running", "waiting_approval"].includes(run.status);
}

function canDeleteRun(run) {
  return ["succeeded", "failed", "cancelled", "timed_out", "interrupted", "skipped"].includes(run.status);
}

onMounted(async () => {
  try {
    if (taskStore.items.length === 0) {
      await taskStore.load();
    }
    if (taskStore.selectedID) {
      await taskStore.loadRuns(taskStore.selectedID);
    }
  } catch (error) {
    Message.error(error?.message ?? String(error));
  }
});
</script>

<template>
  <main class="tasks-workspace">
    <header class="tasks-header">
      <div>
        <h1>任务</h1>
        <p>让 Agent 按计划运行，或发送提醒。定时任务仅在 Humbert 运行时执行。</p>
      </div>
      <a-button type="primary" @click="startCreating">新建任务</a-button>
    </header>

    <a-alert v-if="taskStore.refreshError" class="task-refresh-alert" type="warning" closable>
      任务状态自动刷新失败：{{ taskStore.refreshError }}
    </a-alert>

    <div class="tasks-layout">
      <aside class="task-list">
        <div v-if="taskStore.loading" class="task-list__loading">正在加载…</div>
        <EmptyState
            v-else-if="taskStore.items.length === 0"
            title="还没有主动任务"
            description="创建后可以手动运行，或设置单次、周期、每天与每周计划。"
        />
        <button
            v-for="task in taskStore.items"
            :key="task.id"
            type="button"
            class="task-list__item"
            :class="{ 'task-list__item--active': !creating && task.id === taskStore.selectedID }"
            @click="selectTask(task)"
        >
          <span class="task-list__topline">
            <strong>{{ task.name }}</strong>
            <span :class="['task-state', `task-state--${task.status}`]">
              {{ task.status === "active" ? "计划启用" : "已暂停" }}
            </span>
          </span>
          <small>{{ agentName(task.agentID) }}</small>
          <small v-if="task.execution === 'agent'">{{ conversationModeText(task.conversationMode) }}</small>
          <small v-else>仅通知 · 不创建对话</small>
          <small>{{ scheduleText(task) }}</small>
          <small v-if="task.nextRunAt">下次：{{ formatTime(task.nextRunAt) }}</small>
        </button>
      </aside>

      <div class="task-detail">
        <section v-if="creating || selectedTask" class="task-editor">
          <div class="panel-heading">
            <div>
              <h2>{{ creating ? "新建任务" : "任务配置" }}</h2>
              <p>计划负责触发时间；执行方式决定是启动 Agent，还是仅发送通知。</p>
            </div>
            <a-switch
                v-model="form.enabled"
                :loading="statusSaving"
                :disabled="saving || deletingTask || statusSaving"
                @change="changeTaskEnabled"
            >
              <template #checked>计划启用</template>
              <template #unchecked>计划暂停</template>
            </a-switch>
          </div>

          <div class="form-grid">
            <div class="field field--wide">
              <span>任务名称</span>
              <a-input v-model="form.name" :max-length="120" aria-label="任务名称" placeholder="例如：整理每日工作摘要" />
            </div>

            <div class="field">
              <span>执行方式</span>
              <a-select v-model="form.execution" aria-label="执行方式">
                <a-option value="agent">Agent 执行</a-option>
                <a-option value="notification">仅通知</a-option>
              </a-select>
            </div>

            <div class="field">
              <span>归属 Agent</span>
              <a-select v-model="form.agentID" :disabled="!creating" aria-label="归属 Agent">
                <a-option v-for="agent in agentStore.items" :key="agent.id" :value="agent.id">
                  {{ agent.name }}
                </a-option>
              </a-select>
            </div>

            <div v-if="form.execution === 'agent'" class="field field--wide">
              <span>会话方式</span>
              <a-select v-model="form.conversationMode" aria-label="会话方式">
                <a-option value="isolated">独立对话</a-option>
                <a-option value="continuous">连续对话</a-option>
              </a-select>
              <small class="field-help">
                {{ form.conversationMode === "continuous"
                  ? "所有运行继续使用同一个对话；如果该对话被手动删除，下次运行会自动新建。"
                  : "每次运行创建新的对话，适合日报、检查和彼此独立的任务。" }}
              </small>
            </div>

            <div class="field field--wide">
              <span>{{ form.execution === "notification" ? "通知内容" : "提示词" }}</span>
              <a-textarea
                  v-model="form.prompt"
                  :aria-label="form.execution === 'notification' ? '通知内容' : '提示词'"
                  :auto-size="{ minRows: form.execution === 'notification' ? 3 : 5, maxRows: 12 }"
                  :placeholder="form.execution === 'notification' ? '到点后直接显示这段提醒，不会调用模型。' : '描述任务目标、需要使用的数据和期望输出。'"
              />
            </div>

            <div class="field">
              <span>运行计划</span>
              <a-select v-model="form.scheduleType" aria-label="运行计划">
                <a-option value="manual">仅手动</a-option>
                <a-option value="once">单次</a-option>
                <a-option value="interval">固定间隔</a-option>
                <a-option value="daily">每天</a-option>
                <a-option value="weekly">每周</a-option>
              </a-select>
            </div>

            <div v-if="form.scheduleType !== 'manual'" class="field">
              <span>时区</span>
              <a-input v-model="form.timeZone" aria-label="时区" placeholder="Asia/Shanghai" />
            </div>

            <div v-if="form.scheduleType === 'once'" class="field field--wide">
              <span>运行时间</span>
              <input v-model="form.runAt" class="native-input" type="datetime-local" aria-label="运行时间" />
            </div>

            <div v-if="form.scheduleType === 'interval'" class="field field--wide">
              <span>间隔分钟数</span>
              <a-input-number v-model="form.intervalMinutes" :min="1" :max="525600" aria-label="间隔分钟数" />
            </div>

            <div v-if="['daily', 'weekly'].includes(form.scheduleType)" class="field">
              <span>当天时间</span>
              <input v-model="form.timeOfDay" class="native-input" type="time" aria-label="当天时间" />
            </div>

            <div v-if="form.scheduleType === 'weekly'" class="field">
              <span>星期</span>
              <div class="weekday-row">
                <button
                    v-for="day in weekdays"
                    :key="day.value"
                    type="button"
                    :class="['weekday', { 'weekday--active': form.weekdays.includes(day.value) }]"
                    @click="toggleWeekday(day.value)"
                >{{ day.label }}</button>
              </div>
            </div>

            <div v-if="form.scheduleType !== 'manual'" class="field">
              <span>错过计划</span>
              <a-select v-model="form.misfirePolicy" aria-label="错过计划">
                <a-option value="run_once">恢复后补跑一次</a-option>
                <a-option value="skip">跳过</a-option>
              </a-select>
            </div>

            <div v-if="form.scheduleType !== 'manual'" class="field">
              <span>上一轮未结束</span>
              <a-select v-model="form.overlapPolicy" aria-label="上一轮未结束">
                <a-option value="skip">跳过新一轮</a-option>
                <a-option value="queue_one">最多排队一轮</a-option>
              </a-select>
            </div>
          </div>

          <details class="limits">
            <summary>执行限制与重试</summary>
            <div class="form-grid form-grid--limits">
              <div class="field"><span>最长秒数</span><a-input-number v-model="form.maxDurationSeconds" :min="30" :max="86400" aria-label="最长秒数" /></div>
              <div class="field"><span>最多模型调用</span><a-input-number v-model="form.maxModelCalls" :min="1" :max="100" aria-label="最多模型调用" /></div>
              <div class="field"><span>最多工具调用</span><a-input-number v-model="form.maxToolCalls" :min="1" :max="500" aria-label="最多工具调用" /></div>
              <div class="field"><span>最多 Token 用量</span><a-input-number v-model="form.maxTotalTokens" :min="1000" :max="10000000" aria-label="最多 Token 用量" /></div>
              <div class="field"><span>最多尝试次数</span><a-input-number v-model="form.maxAttempts" :min="1" :max="5" aria-label="最多尝试次数" /></div>
              <div class="field"><span>重试延迟秒数</span><a-input-number v-model="form.retryDelaySeconds" :min="1" :max="21600" aria-label="重试延迟秒数" /></div>
            </div>
          </details>

          <div class="editor-actions">
            <a-button v-if="!creating && selectedTask?.origin === 'chat' && selectedTask?.originRef" @click="emit('open-session', { agentID: selectedTask.agentID, sessionID: selectedTask.originRef })">来源对话</a-button>
            <a-button v-if="creating" @click="cancelCreating">取消</a-button>
            <a-button v-else status="danger" :loading="deletingTask" @click="deleteSelected">删除任务</a-button>
            <span class="editor-actions__spacer"></span>
            <a-button v-if="!creating" :loading="running" @click="runNow">立即运行</a-button>
            <a-button type="primary" :loading="saving" :disabled="statusSaving" @click="save">保存</a-button>
          </div>
        </section>

        <section v-if="selectedTask && !creating" class="run-history">
          <div class="panel-heading">
            <div>
              <h2>运行历史</h2>
              <p>状态与计数会在执行期间自动刷新。</p>
            </div>
            <a-button
                v-if="selectedRuns.length > 0"
                size="small"
                status="danger"
                :loading="clearingRuns"
                :disabled="selectedRuns.some((run) => !canDeleteRun(run))"
                @click="clearRuns"
            >清空历史</a-button>
          </div>

          <div v-if="taskStore.loadingRuns[selectedTask.id]" class="runs-loading">正在加载…</div>
          <EmptyState
              v-else-if="selectedRuns.length === 0"
              title="还没有运行记录"
              description="点击“立即运行”，或等待下一次计划时间。"
          />
          <article v-for="run in selectedRuns" :key="run.id" class="run-card">
            <header>
              <span :class="['run-status', `run-status--${run.status}`]">{{ statusText(run.status) }}</span>
              <time>{{ formatTime(run.createdAt) }}</time>
              <span>第 {{ run.attempt }} 次</span>
            </header>
            <div class="run-metrics">
              <span>模型 {{ run.modelCalls }}</span>
              <span>工具 {{ run.toolCalls }}</span>
              <span>Token {{ run.totalTokens || 0 }}（入 {{ run.inputTokens || 0 }} / 出 {{ run.outputTokens || 0 }}）</span>
              <span>{{ run.trigger === "manual" ? "手动" : run.trigger === "retry" ? "重试" : "计划" }}</span>
            </div>
            <p v-if="run.resultPreview" class="run-result">{{ run.resultPreview }}</p>
            <p v-if="run.error" class="run-error">{{ run.error }}</p>

            <ApprovalCard
                v-if="run.status === 'waiting_approval' && run.approval"
                :approval="run.approval"
                :resolving="approvalID === run.approval.id"
                :error="approvalID === run.approval.id ? approvalError : ''"
                @decide="(decision) => decideApproval(run, decision)"
            />

            <footer>
              <a-button
                  v-if="run.sessionID && run.sessionAvailable"
                  size="small"
                  @click="emit('open-session', { agentID: run.agentID, sessionID: run.sessionID })"
              >查看对话</a-button>
              <a-button v-else-if="run.sessionID" size="small" disabled>对话已删除</a-button>
              <a-button v-if="canCancel(run)" size="small" status="danger" @click="cancelRun(run)">取消运行</a-button>
              <a-button
                  v-if="canDeleteRun(run)"
                  size="small"
                  status="danger"
                  :loading="deletingRunID === run.id"
                  @click="deleteRun(run)"
              >删除记录</a-button>
            </footer>
          </article>
        </section>

        <section v-else-if="!creating" class="workspace-empty">
          <EmptyState title="选择一个任务" description="查看并编辑计划、执行限制与运行历史。" />
        </section>
      </div>
    </div>
  </main>
</template>

<style scoped>
.tasks-workspace {
  position: relative;
  display: grid;
  grid-template-rows: auto minmax(0, 1fr);
  min-width: 0;
  min-height: 0;
  height: 100%;
  background: var(--h-bg);
}

.task-refresh-alert {
  position: absolute;
  z-index: 5;
  top: 76px;
  right: 24px;
  max-width: min(520px, calc(100% - 48px));
}

.tasks-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  padding: 26px 30px 22px;
  border-bottom: 1px solid var(--h-border);
}

.tasks-header h1,
.panel-heading h2 { margin: 0; color: var(--h-text); }
.tasks-header h1 { font-size: 30px; font-weight: 500; letter-spacing: -0.015em; }
.tasks-header p,
.panel-heading p { margin: 5px 0 0; color: var(--h-text-muted); font-size: 13px; }

.tasks-layout {
  display: grid;
  grid-template-columns: 236px minmax(0, 1fr);
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}

.task-list,
.task-detail,
.workspace-empty {
  min-width: 0;
  min-height: 0;
  overflow: auto;
}

.task-list {
  padding: 12px;
  border-right: 1px solid var(--h-border);
}

.task-list__loading,
.runs-loading { padding: 24px; color: var(--h-text-muted); text-align: center; }

.task-list__item {
  display: flex;
  width: 100%;
  flex-direction: column;
  gap: 5px;
  margin-bottom: 7px;
  padding: 12px;
  border: 0;
  border-left: 2px solid transparent;
  border-radius: 0 6px 6px 0;
  color: var(--h-text);
  background: transparent;
  text-align: left;
  cursor: pointer;
}

.task-list__item:hover { background: var(--h-surface-hover); }
.task-list__item--active { border-left-color: var(--h-accent); background: transparent; }
.task-list__topline { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.task-list__item small { color: var(--h-text-muted); }

.task-state,
.run-status {
  display: inline-flex;
  align-items: center;
  width: fit-content;
  border-radius: 999px;
  padding: 2px 7px;
  font-size: 11px;
  background: var(--h-surface-hover);
}

.task-state--active,
.run-status--succeeded { color: rgb(var(--green-6)); background: rgb(var(--green-1)); }
.task-state--paused,
.run-status--skipped,
.run-status--cancelled { color: var(--h-text-muted); }
.run-status--failed,
.run-status--timed_out,
.run-status--interrupted { color: rgb(var(--red-6)); background: rgb(var(--red-1)); }
.run-status--running,
.run-status--starting,
.run-status--queued { color: var(--h-accent); background: var(--h-accent-soft); }
.run-status--waiting_approval { color: rgb(var(--orange-6)); background: rgb(var(--orange-1)); }

.task-detail {
  background: var(--h-bg);
}

.task-editor,
.run-history {
  width: min(760px, 100%);
  margin: 0 auto;
  padding: 28px 30px;
}

.run-history { border-top: 1px solid var(--h-border); }
.panel-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 18px; margin-bottom: 18px; }
.panel-heading h2 { font-size: 20px; font-weight: 500; }

.form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
.form-grid--limits { margin-top: 14px; }
.field { display: flex; min-width: 0; flex-direction: column; gap: 7px; color: var(--h-text-muted); font-size: 12px; }
.field-help { color: var(--h-text-subtle, var(--h-text-muted)); font-size: 11px; line-height: 1.55; }
.field--wide { grid-column: 1 / -1; }
.native-input {
  box-sizing: border-box;
  width: 100%;
  height: 32px;
  border: 1px solid var(--h-border);
  border-radius: 4px;
  padding: 0 10px;
  color: var(--h-text);
  background: var(--h-surface);
  font: inherit;
}

.weekday-row { display: flex; gap: 5px; }
.weekday {
  width: 28px;
  height: 28px;
  border: 1px solid var(--h-border);
  border-radius: 6px;
  color: var(--h-text-muted);
  background: transparent;
  cursor: pointer;
}
.weekday--active { border-color: rgb(var(--primary-6)); color: rgb(var(--primary-6)); background: rgb(var(--primary-1)); }

.limits { margin-top: 18px; color: var(--h-text); }
.limits summary { cursor: pointer; color: var(--h-text-muted); font-size: 13px; }
.editor-actions { display: flex; align-items: center; gap: 9px; margin-top: 22px; }
.editor-actions__spacer { flex: 1; }

.run-card { margin-bottom: 12px; border: 0; border-radius: 8px; padding: 15px; background: var(--h-surface); }
.run-card header,
.run-card footer,
.run-metrics { display: flex; align-items: center; gap: 9px; }
.run-card header { color: var(--h-text-muted); font-size: 11px; }
.run-card header time { margin-left: auto; }
.run-metrics { margin-top: 10px; color: var(--h-text-muted); font-size: 12px; }
.run-result,
.run-error { margin: 11px 0 0; white-space: pre-wrap; font-size: 13px; line-height: 1.55; }
.run-result { color: var(--h-text); }
.run-error { color: rgb(var(--red-6)); }
.run-card footer { justify-content: flex-end; margin-top: 12px; }
.workspace-empty { display: grid; place-items: center; }

@media (max-width: 1050px) {
  .tasks-layout { grid-template-columns: 190px minmax(0, 1fr); }
  .task-editor,
  .run-history { padding: 24px 22px; }
  .form-grid { grid-template-columns: minmax(0, 1fr); }
  .field--wide { grid-column: auto; }
  .weekday-row { flex-wrap: wrap; }
}
</style>
