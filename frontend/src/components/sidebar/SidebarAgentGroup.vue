<script setup>
import { t } from "../../i18n/index.js";
import { IconFolder, IconPlus, IconEdit } from "@arco-design/web-vue/es/icon";
import SidebarSessionRow from "./SidebarSessionRow.vue";
defineProps({
  agent: { type: Object, required: true },
  active: Boolean,
  expanded: Boolean,
  searching: Boolean,
  loading: Boolean,
  sessions: { type: Array, default: () => [] },
  selectedSessionID: String,
  runningSessionIDs: { type: Array, default: () => [] },
});
const emit = defineEmits([
  "toggle",
  "select",
  "create",
  "edit",
  "select-session",
  "rename-session",
  "archive-session",
  "remove-session",
]);
</script>

<template>
  <section class="agent-group">
    <div class="agent-row" :class="{ 'agent-row--active': active }">
      <button
        type="button"
        class="agent-toggle"
        :aria-label="t(expanded ? '折叠 Agent' : '展开 Agent')"
        @click.stop="emit('toggle')"
      >
        <span
          class="agent-chevron"
          :class="{ 'agent-chevron--open': expanded || searching }"
          >›</span
        >
      </button>
      <button type="button" class="agent-main" @click="emit('select')">
        <IconFolder class="agent-icon" />
        <span class="agent-text">
          <span class="agent-name">{{ agent.name }}</span>
          <span class="agent-model"
            >{{ agent.modelDisplayName || t("未配置模型")
            }}<span
              v-if="agent.subagentEnabled"
              class="agent-collaboration-mark"
              >{{ t("· 可协作") }}</span
            ></span
          >
        </span>
        <span
          v-if="runningSessionIDs.length"
          class="agent-running"
          :title="t('Agent 中有正在运行的对话')"
        ></span>
      </button>
      <div class="agent-actions">
        <a-tooltip :content="t('新建对话')"
          ><a-button type="text" size="mini" @click.stop="emit('create')"
            ><template #icon><IconPlus /></template></a-button
        ></a-tooltip>
        <a-tooltip :content="t('Agent 设置')"
          ><a-button type="text" size="mini" @click.stop="emit('edit')"
            ><template #icon><IconEdit /></template></a-button
        ></a-tooltip>
      </div>
    </div>
    <div v-if="expanded || searching" class="agent-sessions">
      <button type="button" class="create-session-item" @click="emit('create')">
        <IconPlus /><span>{{ t("新建对话") }}</span>
      </button>
      <div v-if="loading" class="session-placeholder">{{ t("正在加载…") }}</div>
      <div v-else-if="sessions.length === 0" class="session-placeholder">
        {{ t(searching ? "没有匹配的对话" : "还没有对话") }}
      </div>
      <SidebarSessionRow
        v-for="session in sessions"
        :key="session.id"
        :session="session"
        :active="active && selectedSessionID === session.id"
        :running="runningSessionIDs.includes(session.id)"
        @select="emit('select-session', session)"
        @rename="emit('rename-session', session)"
        @archive="emit('archive-session', session)"
        @remove="emit('remove-session', session)"
      />
    </div>
  </section>
</template>

<style scoped>
.agent-group + .agent-group {
  margin-top: 3px;
}

.agent-row {
  display: flex;

  width: 100%;

  min-width: 0;

  align-items: center;

  min-height: 46px;

  border: 0;
  border-left: 2px solid transparent;

  border-radius: 0 6px 6px 0;

  background: transparent;
}

.agent-row:hover {
  background: var(--h-surface-hover);
}

.agent-row--active {
  border-left-color: var(--h-accent);

  background: transparent;
}

.agent-toggle {
  display: grid;

  width: 24px;
  height: 40px;

  flex: 0 0 24px;

  place-items: center;

  padding: 0;

  cursor: pointer;

  border: 0;

  outline: none;

  background: transparent;

  color: var(--h-text-muted);
}

.agent-chevron {
  display: inline-block;

  font-size: 17px;

  line-height: 1;

  transition: transform 120ms ease;
}

.agent-chevron--open {
  transform: rotate(90deg);
}

.agent-main {
  display: flex;

  min-width: 0;

  flex: 1;

  align-items: center;

  gap: 8px;

  align-self: stretch;

  padding: 0;

  cursor: pointer;

  border: 0;

  outline: none;

  background: transparent;

  text-align: left;
}

.agent-icon {
  flex: 0 0 auto;

  color: var(--h-text-muted);

  font-size: 14px;
}

.agent-text {
  display: flex;

  min-width: 0;

  flex: 1;

  flex-direction: column;

  gap: 1px;
}

.agent-name {
  overflow: hidden;

  color: var(--h-text);

  font-size: 12px;

  font-weight: 500;

  text-overflow: ellipsis;

  white-space: nowrap;
}

.agent-model {
  overflow: hidden;

  color: var(--h-text-muted);

  font-size: 12px;

  text-overflow: ellipsis;

  white-space: nowrap;
}

.agent-collaboration-mark {
  color: var(--h-accent);
  font-weight: 600;
}

.agent-running {
  width: 6px;
  height: 6px;

  flex: 0 0 6px;

  margin-right: 3px;

  border-radius: 50%;

  background: var(--h-success);
}

.agent-actions {
  display: none;

  flex: 0 0 auto;

  align-items: center;

  padding-right: 3px;
}

.agent-row:hover .agent-actions,
.agent-row:focus-within .agent-actions,
.agent-row--active .agent-actions {
  display: flex;
}

/*
 * =========================================================
 * Session Level
 * =========================================================
 */

.agent-sessions {
  position: relative;

  margin: 2px 0 6px 23px;

  padding-left: 10px;

  border-left: 1px solid var(--h-border);
}

.create-session-item {
  width: 100%;

  min-width: 0;

  border-radius: 7px;
}

.create-session-item {
  display: flex;

  height: 30px;

  align-items: center;

  gap: 7px;

  padding: 0 8px;

  cursor: pointer;

  border: 0;
  border-left: 2px solid transparent;

  outline: none;

  background: transparent;

  color: var(--h-text-muted);

  font-size: 12px;

  text-align: left;
}

.create-session-item:hover {
  background: var(--h-surface-hover);

  color: var(--h-text-secondary);
}

.session-placeholder {
  padding: 8px 10px;

  color: var(--h-text-muted);

  font-size: 12px;
}
</style>
