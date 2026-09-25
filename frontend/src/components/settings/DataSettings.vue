<script setup>
import { onMounted, ref } from "vue";
import { Message } from "../../utils/uiMessage.js";
import { exportBackup, scheduleRestore, getPendingBackupStatus, cancelPendingBackup, getPendingRestoreStatus, cancelPendingRestore } from "../../api/app.js";
import { confirmAction } from "../../utils/confirm.js";

const busy = ref(false);
const backupPassword = ref("");
const backupPasswordConfirm = ref("");
const restorePassword = ref("");
const pendingBackup = ref(null);
const pendingRestore = ref(null);

async function refreshBackupStatus() {
  try {
    const [backup, restore] = await Promise.all([getPendingBackupStatus(), getPendingRestoreStatus()]);
    pendingBackup.value = backup?.destination ? backup : null;
    pendingRestore.value = restore?.archive ? restore : null;
  } catch (error) { Message.error(error?.message || String(error)); }
}

async function cancelRestore() {
  busy.value = true;
  try {
    await cancelPendingRestore();
    pendingRestore.value = null;
    Message.success("已取消待执行的数据恢复");
  } catch (error) { Message.error(error?.message || String(error)); }
  finally { busy.value = false; }
}

async function cancelBackup() {
  busy.value = true;
  try {
    await cancelPendingBackup();
    pendingBackup.value = null;
    Message.success("已取消待执行的备份");
  } catch (error) { Message.error(error?.message || String(error)); }
  finally { busy.value = false; }
}

onMounted(refreshBackupStatus);

async function saveBackup() {
  if (backupPassword.value.length < 12 || backupPassword.value !== backupPasswordConfirm.value) {
    Message.warning("请输入至少 12 个字符的备份口令，并确认两次输入一致");
    return;
  }
  busy.value = true;
  try {
    const path = await exportBackup(backupPassword.value);
    if (path) {
      Message.success(`备份已安排：完全退出并重新启动 Humbert 后保存到 ${path}`);
      await refreshBackupStatus();
    }
  } catch (error) { Message.error(error?.message || String(error)); }
  finally { busy.value = false; backupPassword.value = ""; backupPasswordConfirm.value = ""; }
}

async function restoreBackup() {
  const confirmed = await confirmAction({
    title: "恢复数据备份",
    message: "下次启动时将用所选备份替换当前数据。现有数据会保留在回退目录中；请先导出一份当前数据备份。",
    confirmText: "选择备份",
  });
  if (!confirmed) return;
  busy.value = true;
  try {
    const path = await scheduleRestore(restorePassword.value);
    if (path) {
      Message.success("备份已验证。完全退出并重新启动 Humbert 后将恢复数据。");
      await refreshBackupStatus();
    }
  } catch (error) { Message.error(error?.message || String(error)); }
  finally { busy.value = false; restorePassword.value = ""; }
}
</script>

<template>
  <section class="data-settings">
    <h2>数据备份</h2>
    <p>备份包含 Agent、会话、附件、任务、技能、工作区和模型密钥。输入口令后选择保存位置；下次启动前会在数据服务尚未运行时生成加密备份。请妥善保存口令，遗失后无法恢复。</p>
    <a-input-password v-model="backupPassword" placeholder="备份口令（至少 12 个字符）" :max-length="256" autocomplete="new-password" />
    <a-input-password v-model="backupPasswordConfirm" placeholder="再次输入备份口令" :max-length="256" autocomplete="new-password" />
    <a-button type="primary" :loading="busy" @click="saveBackup">安排加密备份</a-button>
    <div v-if="pendingBackup" class="pending-backup">
      <p>待备份位置：{{ pendingBackup.destination }}</p>
      <p v-if="pendingBackup.lastError" class="backup-error">上次启动备份失败：{{ pendingBackup.lastError }}。下次启动将重试。</p>
      <a-button :disabled="busy" @click="cancelBackup">取消待执行备份</a-button>
    </div>
    <h2 style="margin-top: 10px">恢复数据</h2>
    <p>加密备份需输入创建时的口令；旧版 ZIP 可留空。验证后完全退出并重新启动 Humbert，原数据会保留在同级回退目录。</p>
    <a-input-password v-model="restorePassword" placeholder="加密备份口令" :max-length="256" autocomplete="off" />
    <a-button :disabled="busy" @click="restoreBackup">选择备份并安排恢复</a-button>
    <div v-if="pendingRestore" class="pending-backup" role="status">
      <p>下次启动将恢复：{{ pendingRestore.archive }}</p>
      <p v-if="pendingRestore.scheduledAt">安排时间：{{ new Date(pendingRestore.scheduledAt).toLocaleString() }}</p>
      <p>类型：{{ pendingRestore.encrypted ? '加密备份' : '旧版 ZIP 备份' }}。当前数据会保留在回退目录。</p>
      <a-button :disabled="busy" @click="cancelRestore">取消待执行恢复</a-button>
    </div>
  </section>
</template>

<style scoped>
.data-settings { max-width: 720px; padding: 20px; border: 1px solid var(--h-border); border-radius: 12px; background: var(--h-surface); }
.data-settings h2 { margin: 0 0 8px; font-size: 16px; }
.data-settings p { color: var(--h-text-muted); line-height: 1.6; }
.data-settings :deep(.arco-input-wrapper) { display: block; max-width: 420px; margin: 10px 0; }
.data-settings code { overflow-wrap: anywhere; }
.pending-backup { margin-top: 12px; overflow-wrap: anywhere; }
.backup-error { color: var(--color-danger-6, #b42318) !important; }
</style>
