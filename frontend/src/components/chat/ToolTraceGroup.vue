<script setup>
import {
  computed,
  ref,
} from "vue";

import {
  summarizeToolTrace,
} from "../../utils/toolTrace.js";

const props =
    defineProps({
      trace: {
        type: Object,
        required: true,
      },

      /**
       * live 只描述当前 Trace 是否仍在执行。
       *
       * 它不再控制卡片是否展开。
       */
      live: {
        type: Boolean,
        default: false,
      },
    });

/**
 * Tool Trace 永远默认折叠。
 *
 * 即使：
 *
 *   tool.started
 *   tool.completed
 *   assistant.delta
 *
 * 不断到来，也不会改变 expanded。
 *
 * 这样整个卡片在 Agent 执行期间保持固定高度，
 * Message Viewport 不会因为 Tool 数量增长不断跳动。
 */
const expanded =
    ref(false);

/**
 * Tool Detail 同样只有用户主动点击以后展开。
 */
const expandedCalls =
    ref(new Set());

const summary =
    computed(() =>
        summarizeToolTrace(
            props.trace?.calls,
        ),
    );

const summaryText =
    computed(() => {
      const count =
          summary.value.total;

      if (
          summary.value.failed > 0
      ) {
        return (
            `工具调用 · ${count} 个` +
            ` · ${summary.value.failed} 个失败`
        );
      }

      if (props.live) {
        return (
            `工具调用 · ${count} 个 · 执行中`
        );
      }

      if (count > 0) {
        return (
            `工具调用 · ${count} 个`
        );
      }

      return "工具调用";
    });

function toggleExpanded() {
  expanded.value =
      !expanded.value;
}

function toggleCall(callID) {
  const next =
      new Set(
          expandedCalls.value,
      );

  if (next.has(callID)) {
    next.delete(callID);
  } else {
    next.add(callID);
  }

  expandedCalls.value =
      next;
}

function isCallExpanded(
    callID,
) {
  return expandedCalls.value.has(
      callID,
  );
}

function statusText(status) {
  switch (status) {
    case "completed":
      return "完成";

    case "failed":
      return "失败";

    case "running":
      return "执行中";

    case "requested":
      return "等待执行";

    default:
      return "处理中";
  }
}

function statusClass(status) {
  switch (status) {
    case "completed":
      return (
          "tool-status--completed"
      );

    case "failed":
      return (
          "tool-status--failed"
      );

    default:
      return (
          "tool-status--pending"
      );
  }
}
</script>

<template>
  <section class="tool-trace">
    <button
        type="button"
        class="trace-header"
        :aria-expanded="expanded"
        @click="toggleExpanded"
    >
      <span
          class="trace-icon"
          aria-hidden="true"
      >
        ◇
      </span>

      <span class="trace-title">
        {{ summaryText }}
      </span>

      <span
          class="trace-chevron"
          :class="{
          'trace-chevron--open':
            expanded,
        }"
          aria-hidden="true"
      >
        ›
      </span>
    </button>

    <div
        v-if="expanded"
        class="trace-content"
    >
      <div
          v-if="
          Array.isArray(
            trace.notes,
          ) &&
          trace.notes.length > 0
        "
          class="trace-notes"
      >
        <div class="trace-label">
          过程说明
        </div>

        <p
            v-for="
            (note, index) in
            trace.notes
          "
            :key="index"
            class="trace-note"
        >
          {{ note }}
        </p>
      </div>

      <div class="tool-list">
        <article
            v-for="
            (call, index) in
            trace.calls
          "
            :key="
            call.id ||
            index
          "
            class="tool-item"
        >
          <button
              type="button"
              class="tool-summary"
              :aria-expanded="
              isCallExpanded(
                call.id,
              )
            "
              @click="
              toggleCall(
                call.id,
              )
            "
          >
            <span class="tool-index">
              {{ index + 1 }}
            </span>

            <span class="tool-main">
              <span class="tool-action">
                {{
                  call.actionLabel
                }}
              </span>

              <span
                  class="
                  tool-technical-name
                "
              >
                {{ call.name }}
              </span>
            </span>

            <span
                class="tool-status"
                :class="
                statusClass(
                  call.status,
                )
              "
            >
              <span
                  v-if="
                  call.status ===
                  'completed'
                "
                  aria-hidden="true"
              >
                ✓
              </span>

              <span
                  v-else-if="
                  call.status ===
                  'failed'
                "
                  aria-hidden="true"
              >
                !
              </span>

              <span
                  v-else
                  class="tool-status-dot"
                  aria-hidden="true"
              ></span>

              {{
                statusText(
                    call.status,
                )
              }}

              <template
                  v-if="
                  Number.isFinite(
                    call.durationMS,
                  ) &&
                  call.durationMS > 0
                "
              >
                · {{ call.durationMS }}ms
              </template>
            </span>

            <span
                class="tool-chevron"
                :class="{
                'tool-chevron--open':
                  isCallExpanded(
                    call.id,
                  ),
              }"
                aria-hidden="true"
            >
              ›
            </span>
          </button>

          <div
              v-if="
              isCallExpanded(
                call.id,
              )
            "
              class="tool-detail"
          >
            <div
                v-if="
                call.formattedArguments
              "
                class="
                tool-detail-section
              "
            >
              <div class="trace-label">
                参数
              </div>

              <pre
                  class="trace-code"
              >{{ call.formattedArguments }}</pre>
            </div>

            <div
                v-if="
                call.formattedResult
              "
                class="
                tool-detail-section
              "
            >
              <div class="trace-label">
                结果
              </div>

              <pre
                  class="
                  trace-code
                  trace-code--result
                "
              >{{ call.formattedResult }}</pre>
            </div>

            <div
                v-if="call.error"
                class="
                tool-detail-section
              "
            >
              <div class="trace-label">
                错误
              </div>

              <pre
                  class="
                  trace-code
                  trace-code--error
                "
              >{{ call.error }}</pre>
            </div>
          </div>
        </article>
      </div>
    </div>
  </section>
</template>

<style scoped>
.tool-trace {
  width: 100%;

  min-width: 0;

  margin: 2px 0 15px;
}

.trace-header {
  display: flex;

  width: 100%;

  min-width: 0;

  align-items: center;

  gap: 9px;

  padding: 10px 13px;

  cursor: pointer;

  border: 1px solid var(--h-border);

  border-radius: 9px;

  outline: none;

  background: var(--h-surface-hover);

  color: var(--h-text-secondary);

  text-align: left;

  box-shadow: none;

  transition: border-color 120ms ease,
  background-color 120ms ease;
}

.trace-header:hover {
  border-color: var(--h-border-strong);

  background: var(--h-surface);
}

.trace-icon {
  flex: 0 0 auto;

  color: var(--h-accent);

  font-size: 13px;
}

.trace-title {
  min-width: 0;

  flex: 1;

  overflow: hidden;

  font-size: 12px;

  line-height: 1.45;

  text-overflow: ellipsis;

  white-space: nowrap;
}

.trace-chevron,
.tool-chevron {
  flex: 0 0 auto;

  color: var(--h-text-muted);

  font-size: 18px;

  line-height: 1;

  transition: transform 130ms ease;
}

.trace-chevron--open,
.tool-chevron--open {
  transform: rotate(90deg);
}

.trace-content {
  margin-top: 7px;

  padding: 7px 0 2px 12px;

  border-left: 1px solid var(--h-border);
}

.trace-notes {
  margin: 0 8px 10px;
}

.trace-label {
  margin-bottom: 5px;

  color: var(--h-text-muted);

  font-size: 9px;
}

.trace-note {
  margin: 0 0 6px;

  color: var(--h-text-secondary);

  font-size: 11px;

  line-height: 1.65;

  white-space: pre-wrap;

  overflow-wrap: anywhere;
}

.tool-list {
  display: grid;

  gap: 6px;
}

.tool-item {
  min-width: 0;

  overflow: hidden;

  border: 1px solid var(--h-border);

  border-radius: 8px;

  background: var(--h-surface);
}

.tool-summary {
  display: flex;

  width: 100%;

  min-width: 0;

  align-items: center;

  gap: 9px;

  padding: 9px 10px;

  border: 0;

  outline: none;

  background: transparent;

  cursor: pointer;

  text-align: left;

  box-shadow: none;
}

.tool-summary:hover {
  background: var(--h-surface-hover);
}

.tool-index {
  display: grid;

  width: 19px;
  height: 19px;

  flex: 0 0 19px;

  place-items: center;

  border: 1px solid var(--h-border);

  border-radius: 50%;

  color: var(--h-text-muted);

  font-size: 8px;
}

.tool-main {
  display: flex;

  min-width: 0;

  flex: 1;

  align-items: baseline;

  gap: 7px;
}

.tool-action {
  min-width: 0;

  overflow: hidden;

  color: var(--h-text-secondary);

  font-size: 11px;

  text-overflow: ellipsis;

  white-space: nowrap;
}

.tool-technical-name {
  flex: 0 0 auto;

  color: var(--h-text-muted);

  font-family: ui-monospace,
  SFMono-Regular,
  Menlo,
  Monaco,
  Consolas,
  monospace;

  font-size: 8px;
}

.tool-status {
  display: inline-flex;

  flex: 0 0 auto;

  align-items: center;

  gap: 4px;

  font-size: 8px;
}

.tool-status--completed {
  color: var(--h-success);
}

.tool-status--failed {
  color: var(--h-danger);
}

.tool-status--pending {
  color: var(--h-warning);
}

.tool-status-dot {
  width: 5px;
  height: 5px;

  border-radius: 50%;

  background: currentColor;

  animation: tool-pulse 1.15s ease-in-out infinite;
}

@keyframes tool-pulse {
  0%,
  100% {
    opacity: 0.35;
  }

  50% {
    opacity: 1;
  }
}

.tool-detail {
  padding: 0 10px 10px 38px;
}

.tool-detail-section +
.tool-detail-section {
  margin-top: 9px;
}

.trace-code {
  width: 100%;

  max-height: 190px;

  margin: 0;

  overflow: auto;

  padding: 8px 9px;

  border: 1px solid var(--h-border);

  border-radius: 6px;

  background: var(--h-bg);

  color: var(--h-text-secondary);

  font-family: ui-monospace,
  SFMono-Regular,
  Menlo,
  Monaco,
  Consolas,
  monospace;

  font-size: 9px;

  line-height: 1.55;

  white-space: pre-wrap;

  overflow-wrap: anywhere;
}

.trace-code--result {
  max-height: 260px;
}

.trace-code--error {
  color: var(--h-danger);
}
</style>