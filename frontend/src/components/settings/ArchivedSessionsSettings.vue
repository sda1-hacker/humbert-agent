<script setup>
import { computed, onMounted, ref } from "vue";
import { Message } from "@arco-design/web-vue";
import { useAgentStore } from "../../stores/agents.js";
import { useRuntimeStore } from "../../stores/runtime.js";
import { useSessionStore } from "../../stores/sessions.js";
import { confirmAction } from "../../utils/confirm.js";

const emit = defineEmits(["open-session"]);
const agents = useAgentStore();
const runtime = useRuntimeStore();
const sessions = useSessionStore();
const loading = ref(false);
const busyID = ref("");
const busyAction = ref("");
const query = ref("");

const archived = computed(() => agents.items.flatMap((agent) =>
  sessions.sessionsForAgent(agent.id)
      .filter((session) => session.archived)
      .map((session) => ({ ...session, agentName: agent.name })),
).sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt)));

const visible = computed(() => {
  const keyword = query.value.trim().toLocaleLowerCase();
  return keyword
    ? archived.value.filter((session) =>
      `${session.title} ${session.agentName}`.toLocaleLowerCase().includes(keyword))
    : archived.value;
});

async function refresh() {
  loading.value = true;
  try {
    if (!agents.items.length) await agents.load();
    const results = await Promise.allSettled(agents.items.map((agent) =>
      sessions.loadAgentSessions(agent.id, { force: true })));
    const failed = results.find((result) => result.status === "rejected");
    if (failed) throw failed.reason;
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    loading.value = false;
  }
}

async function restore(session) {
  if (busyID.value) return;
  if (runtime.isSessionRunning(session.id)) {
    Message.warning("请先停止当前对话");
    return;
  }
  busyID.value = session.id;
  busyAction.value = "restore";
  try {
    await sessions.setArchived(session.id, false);
    Message.success("已解除归档");
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    busyID.value = "";
    busyAction.value = "";
  }
}

async function remove(session) {
  if (busyID.value) return;
  if (runtime.isSessionRunning(session.id)) {
    Message.warning("请先停止当前对话");
    return;
  }
  const confirmed = await confirmAction({
    title: "删除归档会话",
    message: `确定永久删除「${session.title}」及其全部消息吗？如果它属于任务，对应的运行历史也会一并删除。`,
    confirmText: "永久删除",
    danger: true,
  });
  if (!confirmed) return;
  busyID.value = session.id;
  busyAction.value = "delete";
  try {
    await sessions.remove(session.id);
    Message.success("归档会话已删除");
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    busyID.value = "";
    busyAction.value = "";
  }
}

function formatDate(value) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "" : date.toLocaleString();
}

function open(session) {
  sessions.search = "";
  emit("open-session", { agentID: session.agentID, sessionID: session.id });
}

onMounted(refresh);
</script>

<template>
  <section class="archived-settings">
    <div class="archived-settings__toolbar">
      <p>归档会话保留完整对话。可以打开查看、解除归档，或永久删除。</p>
      <a-button size="small" :loading="loading" @click="refresh">刷新</a-button>
    </div>

    <a-input v-model="query" allow-clear placeholder="搜索会话或 Agent" aria-label="搜索归档会话" />

    <p v-if="loading && !archived.length" class="archived-settings__empty">正在读取归档会话…</p>
    <p v-else-if="!archived.length" class="archived-settings__empty">还没有归档会话。</p>
    <p v-else-if="!visible.length" class="archived-settings__empty">没有匹配的归档会话。</p>

    <div v-else class="archived-settings__list">
      <div v-for="session in visible" :key="session.id" class="archived-settings__row">
        <div class="archived-settings__details">
          <strong>{{ session.title }}</strong>
          <span>{{ session.agentName }}<template v-if="session.updatedAt"> · {{ formatDate(session.updatedAt) }}</template></span>
        </div>
        <div class="archived-settings__actions">
          <a-button size="small" :disabled="Boolean(busyID)" @click="open(session)">查看</a-button>
          <a-button size="small" :loading="busyID === session.id && busyAction === 'restore'" :disabled="Boolean(busyID) && (busyID !== session.id || busyAction !== 'restore')" @click="restore(session)">解除归档</a-button>
          <a-button size="small" status="danger" :loading="busyID === session.id && busyAction === 'delete'" :disabled="Boolean(busyID) && (busyID !== session.id || busyAction !== 'delete')" @click="remove(session)">删除</a-button>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.archived-settings { max-width: 920px; }
.archived-settings__toolbar { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 18px; }
.archived-settings__toolbar p { margin: 0; color: var(--h-text-muted); line-height: 1.6; }
.archived-settings > :deep(.arco-input-wrapper) { max-width: 420px; margin-bottom: 16px; }
.archived-settings__empty { padding: 32px 16px; border: 1px dashed var(--h-border); border-radius: 10px; color: var(--h-text-muted); text-align: center; }
.archived-settings__list { overflow: hidden; border: 1px solid var(--h-border); border-radius: 12px; background: var(--h-surface); }
.archived-settings__row { display: flex; align-items: center; justify-content: space-between; gap: 18px; padding: 15px 18px; }
.archived-settings__row + .archived-settings__row { border-top: 1px solid var(--h-border); }
.archived-settings__details { display: flex; min-width: 0; flex-direction: column; gap: 4px; }
.archived-settings__details strong { overflow: hidden; color: var(--h-text); font-size: 14px; font-weight: 600; text-overflow: ellipsis; white-space: nowrap; }
.archived-settings__details span { color: var(--h-text-muted); font-size: 12px; }
.archived-settings__actions { display: flex; flex: 0 0 auto; gap: 8px; }
@media (max-width: 760px) {
  .archived-settings__row { align-items: flex-start; flex-direction: column; }
  .archived-settings__actions { flex-wrap: wrap; }
}
</style>
