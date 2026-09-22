<script setup>
import {
  computed,
} from "vue";

import ThinkingProcessCard
  from "./ThinkingProcessCard.vue";

import ToolTraceGroup
  from "./ToolTraceGroup.vue";

import MarkdownRenderer
  from "./MarkdownRenderer.vue";

import MessageActions
  from "./MessageActions.vue";

import IdentityAvatar
  from "../ui/IdentityAvatar.vue";

import {
  fileChangesOfCalls,
  scheduledTasksOfCalls,
} from "../../utils/toolTrace.js";

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

      /**
       * 当前 Assistant Turn 中已经持久化的 Provider Reasoning。
       *
       * buildConversationBlocks 会把 Tool Calling 链中的多个 Assistant reasoning_content
       * Segment 按时间顺序合并到这里，因此即使最终回答本身没有 Reasoning，也不会丢失
       * ToolCall 前的 Thinking。
       */
      reasoning: {
        type: String,
        default: "",
      },

      agentName: {
        type: String,
        default: "Humbert",
      },

      agentAvatar: {
        type: String,
        default: "",
      },

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

const normalizedReasoning =
    computed(() => {
      if (
          typeof props.reasoning ===
          "string" &&
          props.reasoning.trim()
      ) {
        return props.reasoning.trim();
      }

      const fallback =
          props.message
              ?.metadata
              ?.reasoning_content;

      return typeof fallback === "string"
          ? fallback.trim()
          : "";
    });



/**
 * 本轮成功文件工具产生的文件变化。
 *
 * 这里直接基于当前 Assistant Turn 的 Tool Trace 计算，不建立第二份“产物记录”。
 * 用户关心的是这一轮实际碰了哪些文件；完整工具过程仍然可以在 ToolTraceGroup 中展开。
 */
const fileChanges =
    computed(() =>
        fileChangesOfCalls(
            props.trace?.calls || [],
        ),
    );

const scheduledTasks = computed(() => scheduledTasksOfCalls(props.trace?.calls || []));

function taskTime(value) {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function fileOperationLabel(operation) {
  switch (operation) {
    case "created":
      return "生成";
    case "modified":
      return "修改";
    case "copied":
      return "复制";
    case "moved":
      return "移动";
    case "deleted":
      return "删除";
    default:
      return "写入";
  }
}

function openFile(change) {
  if (!change?.browsable) {
    return;
  }
  emit("open-workspace-file", {
    path: change.path,
  });
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
  <article class="assistant-turn">
    <header class="assistant-header">
      <IdentityAvatar :src="agentAvatar" :name="agentName" :size="30" />
      <span>{{ authorText }}</span>
    </header>

    <ThinkingProcessCard
        v-if="normalizedReasoning"
        :content="normalizedReasoning"
        :running="false"
    />

    <ToolTraceGroup
        v-if="trace"
        :trace="trace"
    />

    <section v-if="scheduledTasks.length" class="turn-tasks" aria-label="已安排的任务">
      <button v-for="task in scheduledTasks" :key="task.id" type="button" class="turn-task" @click="emit('open-task', task.id)">
        <strong>已安排：{{ task.name }}</strong>
        <span v-if="task.nextRunAt">下次：{{ taskTime(task.nextRunAt) }}</span>
        <span>查看任务 ›</span>
      </button>
    </section>

    <!--
      文件变化属于“本轮回答结果”，而不是工作区的永久审计面板。
      只有成功的文件工具才会出现在这里；可安全映射到当前 Workspace 的相对路径支持点击预览。
    -->
    <section
        v-if="fileChanges.length"
        class="turn-files"
        aria-label="本轮文件变化"
    >
      <div class="turn-files__title">本轮文件</div>

      <button
          v-for="change in fileChanges"
          :key="`${change.operation}:${change.path}`"
          type="button"
          class="turn-file"
          :class="{ 'turn-file--disabled': !change.browsable }"
          :disabled="!change.browsable"
          :title="change.browsable ? `在工作区打开 ${change.path}` : change.path"
          @click="openFile(change)"
      >
        <span
            class="turn-file__operation"
            :data-operation="change.operation"
        >
          {{ fileOperationLabel(change.operation) }}
        </span>

        <span class="turn-file__path">
          <template v-if="change.operation === 'moved' && change.fromPath">
            {{ change.fromPath }} → {{ change.path }}
          </template>
          <template v-else>
            {{ change.path }}
          </template>
        </span>

        <span
            v-if="change.browsable"
            class="turn-file__open"
            aria-hidden="true"
        >›</span>
      </button>
    </section>

    <div
        v-if="content"
        class="assistant-answer"
    >
      <MarkdownRenderer :content="content" />
    </div>

    <MessageActions
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


.turn-files {
  display: grid;
  width: min(74%, 650px);
  max-width: 100%;
  gap: 5px;
  margin: 0 0 13px;
}

.turn-files__title {
  margin-bottom: 1px;
  color: var(--h-text-muted);
  font-family: var(--h-ui);
  font-size: 10px;
  letter-spacing: 0.04em;
}

.turn-file {
  display: grid;
  width: 100%;
  min-width: 0;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  padding: 7px 9px;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-sm);
  background: var(--h-surface);
  color: var(--h-text);
  cursor: pointer;
  text-align: left;
  transition:
      border-color var(--h-transition),
      background-color var(--h-transition);
}

.turn-file:hover:not(:disabled) {
  border-color: var(--h-accent-border);
  background: var(--h-accent-soft);
}

.turn-file--disabled {
  cursor: default;
  opacity: 0.72;
}

.turn-file__operation {
  padding: 2px 5px;
  border-radius: 4px;
  background: var(--h-accent-soft);
  color: var(--h-accent);
  font-family: var(--h-ui);
  font-size: 9px;
}

.turn-file__operation[data-operation="modified"] {
  background: var(--h-warning-soft);
  color: var(--h-warning);
}

.turn-file__operation[data-operation="deleted"] {
  background: var(--h-danger-soft);
  color: var(--h-danger);
}

.turn-file__path {
  min-width: 0;
  overflow: hidden;
  font-family: var(--h-mono);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.turn-file__open {
  color: var(--h-text-muted);
  font-size: 17px;
  line-height: 1;
}

.assistant-incomplete {
  margin-top: 7px;

  color: var(--h-warning);

  font-size: 10px;
}

@media (
max-width: 900px
) {
  .assistant-answer,
  .turn-files {
    width: auto;
    max-width: 84%;
  }
}
</style>
