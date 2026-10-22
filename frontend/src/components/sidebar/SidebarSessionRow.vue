<script setup>
import { t } from "../../i18n/index.js";
import { IconMore, IconEdit, IconDelete } from "@arco-design/web-vue/es/icon";
defineProps({
  session: { type: Object, required: true },
  active: Boolean,
  running: Boolean,
});
const emit = defineEmits(["select", "rename", "archive", "remove"]);
</script>

<template>
  <div class="session-row" :class="{ 'session-row--active': active }">
    <button type="button" class="session-main" @click="emit('select')">
      <span class="session-name">{{ session.title }}</span>
      <span v-if="running" class="session-running"></span>
    </button>
    <div class="session-actions">
      <a-dropdown trigger="click" position="br">
        <a-button
          type="text"
          size="mini"
          :aria-label="t('对话操作')"
          :title="t('对话操作')"
          @click.stop
        >
          <template #icon><IconMore /></template>
        </a-button>
        <template #content>
          <a-doption @click="emit('rename')"
            ><template #icon><IconEdit /></template
            >{{ t("重命名对话") }}</a-doption
          >
          <a-doption @click="emit('archive')">{{
            t(session.archived ? "恢复会话" : "归档会话")
          }}</a-doption>
          <a-doption class="session-delete-option" @click="emit('remove')"
            ><template #icon><IconDelete /></template
            >{{ t("删除对话") }}</a-doption
          >
        </template>
      </a-dropdown>
    </div>
  </div>
</template>

<style scoped>
.session-row {
  width: 100%;
  min-width: 0;
  border-radius: 7px;
}
.session-row {
  display: flex;

  height: 34px;

  align-items: center;

  border: 1px solid transparent;

  background: transparent;
}

.session-row:hover {
  background: var(--h-surface-hover);
}

.session-row--active {
  border-left-color: var(--h-accent);

  background: transparent;
}

.session-main {
  display: flex;

  min-width: 0;

  flex: 1;

  align-items: center;

  gap: 6px;

  align-self: stretch;

  padding: 0 6px 0 10px;

  cursor: pointer;

  border: 0;

  outline: none;

  background: transparent;

  color: var(--h-text-secondary);

  text-align: left;
}

.session-name {
  min-width: 0;

  flex: 1;

  overflow: hidden;

  font-size: 12px;

  text-overflow: ellipsis;

  white-space: nowrap;
}

.session-running {
  width: 5px;
  height: 5px;

  flex: 0 0 5px;

  border-radius: 50%;

  background: var(--h-success);
}

.session-actions {
  display: flex;

  flex: 0 0 auto;

  align-items: center;

  padding-right: 2px;
}

.session-delete-option {
  color: var(--h-danger);
}
</style>
