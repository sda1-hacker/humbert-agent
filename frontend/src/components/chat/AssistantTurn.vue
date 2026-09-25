<script setup>
import {
  computed,
} from "vue";

import ActivityTimeline from "./ActivityTimeline.vue";

import MarkdownRenderer
  from "./MarkdownRenderer.vue";

import MessageActions
  from "./MessageActions.vue";

import IdentityAvatar
  from "../ui/IdentityAvatar.vue";

import { scheduledTasksOfCalls } from "../../utils/toolTrace.js";
import { formatDate } from "../../i18n/index.js";

const emit = defineEmits([
  "open-workspace-file",
  "open-task",
]);

const props =
    defineProps({
      message: {
        type: Object,
        default: null,
      },

      trace: {
        type: Object,
        default: null,
      },

      activity: { type: Array, default: () => [] },

      agentName: {
        type: String,
        default: "Humbert",
      },

      agentAvatar: {
        type: String,
        default: "",
      },

      agentId: {
        type: String,
        default: "",
      },

      sessionId: { type: String, default: "" },

      modelName: {
        type: String,
        default: "",
      },
    });

const authorText =
    computed(() => {
      if (!props.modelName) {
        return props.agentName;
      }

      return (
          `${props.agentName} · ${props.modelName}`
      );
    });

const content =
    computed(() =>
        props.message?.content ||
        "",
    );

const scheduledTasks = computed(() => scheduledTasksOfCalls(props.trace?.calls || []));

function taskTime(value) {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : formatDate(date, { dateStyle: "short", timeStyle: "short" });
}

const incomplete =
    computed(() => {
      const metadata =
          props.message?.metadata;

      return Boolean(
          metadata &&
          typeof metadata ===
          "object" &&
          metadata.incomplete,
      );
    });
</script>

<template>
  <article class="assistant-turn" :data-entry-id="message?.id || ''">
    <header class="assistant-header">
      <IdentityAvatar :src="agentAvatar" :name="agentName" :size="30" />
      <span>{{ authorText }}</span>
    </header>

    <ActivityTimeline :steps="activity" :agent-id="agentId" :session-id="sessionId" @open-workspace-file="emit('open-workspace-file', $event)" />

    <section v-if="scheduledTasks.length" class="turn-tasks" aria-label="已安排的任务">
      <button v-for="task in scheduledTasks" :key="task.id" type="button" class="turn-task" @click="emit('open-task', task.id)">
        <strong>已安排：{{ task.name }}</strong>
        <span v-if="task.nextRunAt">下次：{{ taskTime(task.nextRunAt) }}</span>
        <span>查看任务 ›</span>
      </button>
    </section>

    <div
        v-if="content"
        class="assistant-answer"
    >
      <MarkdownRenderer :content="content" />
    </div>

    <MessageActions
        :message-id="message?.id || ''"
        :content="content"
        :session-id="message?.sessionID || ''"
    />

    <div
        v-if="incomplete"
        class="assistant-incomplete"
    >
      本次回答未完整结束
    </div>
  </article>
</template>

<style scoped>
.turn-tasks { display: grid; gap: 8px; margin: 10px 0; }
.turn-task { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; width: 100%; padding: 10px 12px; border: 1px solid var(--h-border); border-radius: 9px; background: var(--h-surface); color: var(--h-text); cursor: pointer; text-align: left; }
.turn-task:hover { background: var(--h-surface-hover); }
.turn-task span { color: var(--h-text-muted); font-size: 12px; }
.assistant-turn {
  display: flex;

  width: 100%;

  min-width: 0;

  align-items: flex-start;

  flex-direction: column;

  margin-bottom: 28px;
}

.assistant-header {
  display: flex;

  align-items: center;

  gap: 10px;

  max-width: 100%;

  margin-bottom: 9px;

  overflow: hidden;

  color: var(--h-accent);

  font-size: 11px;

  font-weight: 450;

  line-height: 1.3;

  text-overflow: ellipsis;

  white-space: nowrap;
}

.assistant-answer {
  max-width: min(
      74%,
      650px
  );

  min-width: 0;

  padding: 0;

  border: 0;

  border-radius: 0;

  background: transparent;

  color: var(--h-text);

  font-size: 14px;

  line-height: 1.75;

  overflow-wrap: anywhere;

  word-break: break-word;
}


.assistant-incomplete {
  margin-top: 7px;

  color: var(--h-warning);

  font-size: 10px;
}

@media (
max-width: 900px
) {
  .assistant-answer {
    width: auto;
    max-width: 84%;
  }
}
</style>
