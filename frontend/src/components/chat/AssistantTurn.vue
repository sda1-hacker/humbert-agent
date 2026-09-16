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
      {{ authorText }}
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
.assistant-turn {
  display: flex;

  width: 100%;

  min-width: 0;

  align-items: flex-start;

  flex-direction: column;

  margin-bottom: 28px;
}

.assistant-header {
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

  padding: 10px 14px;

  border: 1px solid var(--h-border);

  border-radius: 4px 13px 13px 13px;

  background: var(--h-surface);

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
    max-width: 84%;
  }
}
</style>
