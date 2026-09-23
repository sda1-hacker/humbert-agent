<script setup>
import { ref, watch, onUnmounted } from 'vue';
import { Message } from '@arco-design/web-vue';
import { listDeskNotes, addDeskNote, retryDeskNote } from '../../api/desk.js';

const props = defineProps({ agentId: {type: String, default: ''} });
const emit = defineEmits(['open-session']);
const notes = ref([]);
const text = ref('');
const loading = ref(false);
const submitting = ref(false);
let timer = null;
const statusLabels = { pending: '待处理', queued: '排队中', starting: '启动中', running: '执行中', waiting_approval: '等待确认', succeeded: '已完成', failed: '失败', timed_out: '超时', interrupted: '已中断', cancelled: '已取消', skipped: '已跳过' };

async function refresh() {
  if (!props.agentId) { notes.value = []; return; }
  loading.value = true;
  try { notes.value = await listDeskNotes(props.agentId) || []; }
  catch (error) { Message.error(error?.message ?? String(error)); }
  finally { loading.value = false; }
}

async function submit() {
  if (!text.value.trim() || submitting.value) return;
  submitting.value = true;
  try { await addDeskNote(props.agentId, text.value); text.value = ''; await refresh(); }
  catch (error) { Message.error(error?.message ?? String(error)); }
  finally { submitting.value = false; }
}

async function retry(taskID) {
  try { await retryDeskNote(taskID); await refresh(); }
  catch (error) { Message.error(error?.message ?? String(error)); }
}

watch(() => props.agentId, () => { void refresh(); }, {immediate: true});
timer = setInterval(() => { if (notes.value.some((note) => ['queued','starting','running','waiting_approval'].includes(note.status))) void refresh(); }, 5000);
onUnmounted(() => clearInterval(timer));
</script>

<template>
  <section v-if="agentId" class="desk-notes">
    <div class="desk-notes__header"><strong>留给 Agent 的便签</strong><span>提交后在后台运行，可在这里查看结果</span></div>
    <div class="desk-notes__compose">
      <a-textarea v-model="text" :max-length="4000" :auto-size="{minRows: 2, maxRows: 5}" placeholder="例如：整理工作区里的会议记录，列出需要跟进的事项" />
      <a-button type="primary" :loading="submitting" :disabled="!text.trim()" @click="submit">交给 Agent</a-button>
    </div>
    <div v-if="loading && !notes.length" class="desk-notes__empty">正在读取便签…</div>
    <div v-if="notes.length" class="desk-notes__list">
      <article v-for="note in notes" :key="note.taskID" class="desk-notes__item">
        <span class="desk-notes__status">{{ statusLabels[note.status] || note.status }}</span>
        <span class="desk-notes__text" :title="note.text">{{ note.text }}</span>
        <span v-if="note.result" class="desk-notes__result" :title="note.result">{{ note.result }}</span>
        <span v-if="note.error" class="desk-notes__error" :title="note.error">{{ note.error }}</span>
        <a-button v-if="note.sessionID" size="mini" type="text" @click="emit('open-session', {agentID: note.agentID, sessionID: note.sessionID})">查看对话</a-button>
        <a-button v-if="['failed','timed_out','interrupted','cancelled'].includes(note.status)" size="mini" type="text" @click="retry(note.taskID)">重试</a-button>
      </article>
    </div>
  </section>
</template>

<style scoped>
.desk-notes { flex: 0 0 auto; padding: 12px 24px; border-bottom: 1px solid var(--h-border); }
.desk-notes__header { display: flex; align-items: baseline; gap: 12px; margin-bottom: 8px; }
.desk-notes__header strong { font-size: 13px; }
.desk-notes__header span { color: var(--h-text-muted); font-size: 11px; }
.desk-notes__compose { display: flex; gap: 8px; }
.desk-notes__compose :deep(.arco-textarea-wrapper) { flex: 1; }
.desk-notes__list { display: flex; flex-direction: column; gap: 4px; max-height: 160px; overflow: auto; margin-top: 8px; }
.desk-notes__item { display: flex; align-items: center; gap: 8px; min-width: 0; font-size: 11px; }
.desk-notes__status { flex: 0 0 auto; color: var(--h-text-muted); }
.desk-notes__text,.desk-notes__result,.desk-notes__error { overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.desk-notes__text { flex: 1; }
.desk-notes__result { flex: 1; color: var(--h-text-secondary); }
.desk-notes__error { color: var(--h-danger); }
.desk-notes__empty { margin-top: 8px; color: var(--h-text-muted); font-size: 11px; }
</style>
