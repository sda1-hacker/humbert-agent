<script setup>
import {
  computed,
} from "vue";

import ThinkingProcessCard
  from "./ThinkingProcessCard.vue";

import ToolTraceGroup
  from "./ToolTraceGroup.vue";

import ApprovalCard
  from "./ApprovalCard.vue";

import MarkdownRenderer
  from "./MarkdownRenderer.vue";

const props =
    defineProps({
      agentName: {
        type: String,
        default: "Humbert",
      },

      modelName: {
        type: String,
        default: "",
      },

      content: {
        type: String,
        default: "",
      },

      reasoning: {
        type: String,
        default: "",
      },

      running: {
        type: Boolean,
        default: false,
      },

      tools: {
        type: Array,
        default: () => [],
      },

      /** 当前 Turn 正在等待的 Human Approval。 */
      approval: {
        type: Object,
        default: null,
      },

      approvalResolving: {
        type: Boolean,
        default: false,
      },

      approvalError: {
        type: String,
        default: "",
      },
    });

const emit =
    defineEmits([
      "approval-decision",
    ]);

const authorText =
    computed(() => {
      if (!props.modelName) {
        return props.agentName;
      }

      return (
          `${props.agentName} · ${props.modelName}`
      );
    });

const answering =
    computed(() =>
        props.content.length > 0,
    );

const hasReasoning =
    computed(() =>
        props.reasoning.trim().length > 0,
    );

const hasTools =
    computed(() =>
        props.tools.length > 0,
    );

const showThinking =
    computed(() =>
        hasReasoning.value ||
        (
            props.running &&
            !answering.value &&
            !hasTools.value
        ),
    );

/**
 * 一旦最终正文开始输出，当前 Reasoning 阶段视为结束。后续 Tool Calling Round 如果再次
 * 产生 reasoning.delta，RuntimeStore 仍会继续追加内容；只要最终正文尚未开始，卡片保持
 * “思考中”。
 */
const reasoningRunning =
    computed(() =>
        props.running &&
        !answering.value,
    );

const liveTrace =
    computed(() => ({
      key:
          "live-tool-trace",

      notes: [],

      calls:
      props.tools,
    }));
</script>

<template>
  <article class="live-turn">
    <header class="live-header">
      {{ authorText }}
    </header>

    <!--
      Thinking 与 Tool Calling 是两个不同概念：

      - ThinkingProcessCard 只展示 Provider reasoning_content；
      - ToolTraceGroup 只展示真实 Tool Lifecycle。

      两者可以同时存在，不再出现“思考过程 · N 个工具”这种概念混合。
    -->
    <ThinkingProcessCard
        v-if="showThinking"
        :content="reasoning"
        :running="reasoningRunning"
    />

    <ToolTraceGroup
        v-if="hasTools"
        :trace="liveTrace"
        :live="running && !answering"
    />

    <ApprovalCard
        v-if="approval"
        :approval="approval"
        :resolving="approvalResolving"
        :error="approvalError"
        @decide="emit('approval-decision', $event)"
    />

    <!--
      第一个 assistant.delta 到达之前不创建 Answer Bubble。

      实时回答使用稳定宽度，避免随着每几个 Token 横向长大导致频繁重新换行和
      scrollHeight 变化。
    -->
    <div
        v-if="content"
        class="live-answer"
    >
      <MarkdownRenderer :content="content" />

      <span
          v-if="running"
          class="streaming-cursor"
          aria-hidden="true"
      ></span>
    </div>
  </article>
</template>

<style scoped>
.live-turn {
  display: flex;

  width: 100%;

  min-width: 0;

  align-items: flex-start;

  flex-direction: column;

  margin-bottom: 28px;

  overflow-x: hidden;
}

.live-header {
  max-width: 100%;

  margin-bottom: 9px;

  overflow: hidden;

  color: var(--h-accent);

  font-size: 11px;

  line-height: 1.3;

  text-overflow: ellipsis;

  white-space: nowrap;
}

.live-answer {
  width: min(
      74%,
      650px
  );

  max-width: 100%;

  min-width: 0;

  padding: 10px 14px;

  overflow-x: hidden;

  border: 1px solid var(--h-border);

  border-radius: 4px 13px 13px 13px;

  background: var(--h-surface);

  color: var(--h-text);

  font-size: 14px;

  line-height: 1.75;

  overflow-wrap: anywhere;

  word-break: break-word;

  box-shadow: none;
}

.streaming-cursor {
  display: inline-block;

  width: 2px;
  height: 15px;

  margin-left: 3px;

  vertical-align: -2px;

  background: var(--h-accent);

  animation: cursor-blink 900ms infinite;
}

@keyframes cursor-blink {
  0%,
  45% {
    opacity: 1;
  }

  46%,
  100% {
    opacity: 0;
  }
}

@media (
max-width: 900px
) {
  .live-answer {
    width: 84%;
  }
}

@media (
prefers-reduced-motion: reduce
) {
  .streaming-cursor {
    animation: none;
  }
}
</style>
