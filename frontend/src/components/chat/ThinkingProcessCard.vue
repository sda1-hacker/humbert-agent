<script setup>
import {
  computed,
  ref,
} from "vue";

const props =
    defineProps({
      /**
       * Provider 真正返回的 reasoning_content。
       *
       * 这里不根据 Tool 数量或普通 Assistant 文本伪造“思考过程”。历史数据来自
       * Session JSONL v3，实时数据来自 assistant.reasoning.delta。
       */
      content: {
        type: String,
        default: "",
      },

      /**
       * 当前 Provider Reasoning 是否仍可能继续增长。
       */
      running: {
        type: Boolean,
        default: false,
      },
    });

/**
 * 思考过程永远默认折叠，并且 Streaming 期间不会自动改变 expanded。
 *
 * 自动展开/折叠会让 Message Viewport 高度在模型输出时突然跳变；用户主动点击才改变
 * 卡片高度，可以和消息列表的帧级滚动策略配合，保持稳定视觉体验。
 */
const expanded =
    ref(false);

const normalizedContent =
    computed(() =>
        typeof props.content === "string"
            ? props.content.trim()
            : "",
    );

const hasContent =
    computed(() =>
        normalizedContent.value.length > 0,
    );

const title =
    computed(() => {
      if (props.running) {
        return hasContent.value
            ? "思考过程 · 思考中"
            : "思考过程 · 正在分析…";
      }

      return "思考过程 · 已完成";
    });

function toggle() {
  expanded.value =
      !expanded.value;
}
</script>

<template>
  <section class="thinking-process">
    <button
        type="button"
        class="thinking-header"
        :aria-expanded="expanded"
        @click="toggle"
    >
      <span
          class="thinking-icon"
          aria-hidden="true"
      >
        ✦
      </span>

      <span class="thinking-title">
        {{ title }}
      </span>

      <span
          class="thinking-chevron"
          :class="{
          'thinking-chevron--open':
            expanded,
        }"
          aria-hidden="true"
      >
        ›
      </span>
    </button>

    <div
        v-if="expanded"
        class="thinking-content"
    >
      <div
          v-if="hasContent"
          class="thinking-text"
      >
        {{ normalizedContent }}
      </div>

      <div
          v-if="running"
          class="thinking-state"
          :class="{
          'thinking-state--with-content':
            hasContent,
        }"
      >
        <span
            class="thinking-dots"
            aria-hidden="true"
        >
          <span></span>
          <span></span>
          <span></span>
        </span>

        <span>
          {{
            hasContent
                ? "仍在思考…"
                : "正在分析问题…"
          }}
        </span>
      </div>

      <div
          v-else-if="hasContent"
          class="thinking-state thinking-state--completed"
      >
        <span aria-hidden="true">
          ✓
        </span>

        <span>
          已完成思考
        </span>
      </div>

      <div
          v-else
          class="thinking-state"
      >
        当前模型没有返回可展示的 reasoning_content。
      </div>
    </div>
  </section>
</template>

<style scoped>
.thinking-process {
  width: 100%;

  min-width: 0;

  margin-bottom: 12px;
}

.thinking-header {
  display: flex;

  width: 100%;

  min-width: 0;

  align-items: center;

  gap: 9px;

  padding:
      9px 12px;

  cursor: pointer;

  border:
      1px solid
      var(--h-border);

  border-radius: 9px;

  outline: none;

  background:
      var(--h-surface-hover);

  color:
      var(--h-text-secondary);

  text-align: left;

  box-shadow: none;

  transition:
      border-color
      120ms ease,
      background-color
      120ms ease;
}

.thinking-header:hover {
  border-color:
      var(--h-border-strong);

  background:
      var(--h-surface);
}

.thinking-icon {
  flex: 0 0 auto;

  color:
      var(--h-accent);

  font-size: 13px;

  line-height: 1;
}

.thinking-title {
  min-width: 0;

  flex: 1;

  overflow: hidden;

  font-size: 12px;

  line-height: 1.45;

  text-overflow: ellipsis;

  white-space: nowrap;
}

.thinking-chevron {
  flex: 0 0 auto;

  color:
      var(--h-text-muted);

  font-size: 18px;

  line-height: 1;

  transition:
      transform
      130ms ease;
}

.thinking-chevron--open {
  transform:
      rotate(90deg);
}

.thinking-content {
  margin-top: 6px;

  padding:
      10px 13px;

  border-left:
      1px solid
      var(--h-border);
}

/*
 * Reasoning 可能很长，展开后也不能无限撑高消息列表。
 *
 * 这里给它自己的纵向阅读区域；主 Message Viewport 只感知一个有界高度变化，从而避免
 * 展开数千字 Thinking 后整个聊天滚动位置剧烈跳变。
 */
.thinking-text {
  max-height: 320px;

  overflow-x: hidden;
  overflow-y: auto;

  padding-right: 6px;

  color:
      var(--h-text-secondary);

  font-size: 12px;

  line-height: 1.75;

  white-space: pre-wrap;

  overflow-wrap: anywhere;

  word-break: break-word;
}

.thinking-state {
  display: flex;

  align-items: center;

  gap: 8px;

  color:
      var(--h-text-muted);

  font-size: 11px;
}

.thinking-state--with-content {
  margin-top: 9px;
}

.thinking-state--completed {
  margin-top: 9px;

  color:
      var(--h-success);
}

.thinking-dots {
  display: inline-flex;

  width: 24px;

  align-items: center;

  gap: 3px;
}

.thinking-dots span {
  width: 4px;
  height: 4px;

  border-radius: 50%;

  background:
      var(--h-accent);

  animation:
      thinking-dot
      1.1s
      ease-in-out
      infinite;
}

.thinking-dots span:nth-child(2) {
  animation-delay: 140ms;
}

.thinking-dots span:nth-child(3) {
  animation-delay: 280ms;
}

@keyframes thinking-dot {
  0%,
  60%,
  100% {
    opacity: 0.28;

    transform:
        translateY(0);
  }

  30% {
    opacity: 1;

    transform:
        translateY(-2px);
  }
}

@media (
prefers-reduced-motion: reduce
) {
  .thinking-dots span,
  .thinking-chevron {
    animation: none;

    transition: none;
  }
}
</style>
