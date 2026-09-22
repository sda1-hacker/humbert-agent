<script setup>
import { ref } from "vue";
import { Message } from "@arco-design/web-vue";
import { exportBackup, scheduleRestore } from "../../api/app.js";
import { confirmAction } from "../../utils/confirm.js";

const busy = ref(false);

async function saveBackup() {
  busy.value = true;
  try {
    const path = await exportBackup();
    if (path) Message.success(`备份已保存：${path}`);
  } catch (error) { Message.error(error?.message || String(error)); }
  finally { busy.value = false; }
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
    const path = await scheduleRestore();
    if (path) Message.success("备份已验证。完全退出并重新启动 Humbert 后将恢复数据。");
  } catch (error) { Message.error(error?.message || String(error)); }
  finally { busy.value = false; }
}
</script>

<template>
  <section class="data-settings">
    <h2>数据备份</h2>
    <p>备份包含 Agent、会话、附件、任务、技能、工作区和模型密钥。请选择可信任的位置保存，并避免在任务运行时导出。</p>
    <a-button type="primary" :loading="busy" @click="saveBackup">导出完整备份</a-button>
    <h2>恢复数据</h2>
    <p>选择并验证备份后，完全退出并重新启动 Humbert。恢复在应用打开数据文件之前执行，原数据会保留在同级回退目录。</p>
    <a-button :disabled="busy" @click="restoreBackup">选择备份并安排恢复</a-button>
  </section>
</template>

<style scoped>
.data-settings { max-width: 720px; padding: 20px; border: 1px solid var(--h-border); border-radius: 12px; background: var(--h-surface); }
.data-settings h2 { margin: 0 0 8px; font-size: 16px; }
.data-settings p { color: var(--h-text-muted); line-height: 1.6; }
.data-settings code { overflow-wrap: anywhere; }
</style>
