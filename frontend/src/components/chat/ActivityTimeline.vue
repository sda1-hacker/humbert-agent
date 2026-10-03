<script setup>
import { computed, ref } from "vue";
import { IconCheckCircle, IconLoading, IconRight, IconTool } from "@arco-design/web-vue/es/icon";
import { browserNeedsHumanVerification, browserScreenshotOfCall, fileChangesOfCalls, toolActionLabel } from "../../utils/toolTrace.js";
import { t } from "../../i18n/index.js";
import BrowserScreenshotCard from "./BrowserScreenshotCard.vue";

const props = defineProps({
  steps: { type: Array, default: () => [] },
  running: { type: Boolean, default: false },
  agentId: { type: String, default: "" },
  sessionId: { type: String, default: "" },
});
const emit = defineEmits(["open-workspace-file"]);
// 历史和实时过程都从摘要开始；流式步骤更新不改变用户的展开选择。
const expanded = ref(false);
const openDetails = ref(new Set());
const toolCount = computed(() => props.steps.filter((step) => step.type === "tool").length);
const thinkingCount = computed(() => props.steps.filter((step) => step.type === "thinking").length);
// 需要用户处理的浏览器提示独立于详情展开状态。
const verificationSteps = computed(() => props.steps.filter((step) => step.type === "tool" && browserNeedsHumanVerification(step.call)));
const screenshots = computed(() => props.steps
    .filter((step) => step.type === "tool")
    .map((step) => ({ key: step.key, ...browserScreenshotOfCall(step.call) }))
    .filter((item) => item.attachmentId));

function toggleDetail(key) {
  const next = new Set(openDetails.value);
  if (next.has(key)) next.delete(key);
  else next.add(key);
  openDetails.value = next;
}

function statusText(status) {
  if (status === "failed") return t("失败");
  if (status === "completed") return t("完成");
  if (status === "running") return t("执行中");
  return t("等待执行");
}

function fileLabel(operation) {
  return t(({ created: "生成", modified: "修改", copied: "复制", moved: "移动", deleted: "删除" })[operation] || "写入");
}

function emptyResultText(call) {
  if (call?.status === "completed") return t("工具未返回内容");
  if (call?.status === "failed") return t("工具未返回错误详情");
  return t("等待工具返回结果…");
}

</script>

<template>
  <section v-if="steps.length || running" class="activity-timeline" :aria-label="t('执行过程')">
    <button class="activity-summary" type="button" :aria-expanded="expanded" @click="expanded = !expanded">
      <IconLoading v-if="running" class="activity-summary__icon" aria-hidden="true"/>
      <IconCheckCircle v-else class="activity-summary__icon" aria-hidden="true"/>
      <span class="activity-summary__label">
        <span>{{ t(running ? '正在处理' : '处理完成') }}</span>
        <span v-if="toolCount" class="activity-summary__meta"> · {{ t('{count} 个工具', { count: toolCount }) }}</span>
        <span v-if="thinkingCount" class="activity-summary__meta"> · {{ t('{count} 次思考', { count: thinkingCount }) }}</span>
      </span>
      <IconRight class="activity-summary__arrow" :class="{ 'activity-summary__arrow--open': expanded }" aria-hidden="true"/>
    </button>
    <div v-if="expanded" class="activity-steps">
      <template v-for="step in steps" :key="step.key">
        <div v-if="step.type === 'thinking'" class="activity-step">
          <button
              v-if="step.content"
              class="activity-step__heading activity-step__heading--button"
              type="button"
              :aria-expanded="openDetails.has(step.key)"
              @click="toggleDetail(step.key)"
          >
            <IconRight class="activity-step__marker" :class="{ 'activity-step__marker--open': openDetails.has(step.key) }" aria-hidden="true"/>
            <span>{{ t(step.status === 'running' ? '正在思考…' : '思考完成') }}</span>
          </button>
          <div v-else class="activity-step__heading">
            <span class="activity-step__marker" aria-hidden="true">·</span>
            <span>{{ t(step.status === 'running' ? '正在分析…' : '分析完成') }}</span>
          </div>
          <p v-if="step.note" class="activity-step__note">{{ step.note }}</p>
          <div v-if="step.content && openDetails.has(step.key)" class="activity-step__detail">
            <div class="activity-step__source-hint">{{ t('模型原始思考内容 · 语言由模型决定') }}</div>
            <div class="activity-step__reasoning">{{ step.content }}</div>
          </div>
        </div>

        <div v-else-if="step.type === 'tool'" class="activity-step activity-step--tool">
          <button
              class="activity-tool"
              type="button"
              :aria-expanded="openDetails.has(step.key)"
              @click="toggleDetail(step.key)"
          >
            <IconTool class="activity-tool__icon" aria-hidden="true"/>
            <span class="activity-tool__action">{{ step.call ? toolActionLabel(step.call) : t('调用工具') }}</span>
            <span class="activity-tool__status" :class="`activity-tool__status--${step.call?.status || 'requested'}`">{{ statusText(step.call?.status) }}</span>
            <IconRight class="activity-tool__arrow" :class="{ 'activity-tool__arrow--open': openDetails.has(step.key) }" aria-hidden="true"/>
          </button>
          <div v-if="openDetails.has(step.key)" class="activity-step__detail activity-step__detail--tool">
            <section class="activity-tool-detail__call" :aria-label="t('工具调用')">
              <header class="activity-tool-detail__heading">
                <h4>{{ t('工具调用') }}</h4>
                <code v-if="step.call?.name" class="activity-step__technical">{{ step.call.name }}</code>
              </header>
              <div class="activity-tool-detail__label">{{ t('调用参数') }}</div>
              <pre v-if="step.call?.formattedArguments || step.call?.arguments">{{ step.call.formattedArguments || step.call.arguments }}</pre>
              <p v-else class="activity-tool-detail__empty">{{ t('无参数') }}</p>
            </section>
            <section class="activity-tool-detail__result" :aria-label="t('执行结果')">
              <header class="activity-tool-detail__heading">
                <h4>{{ t('执行结果') }}</h4>
                <span class="activity-tool__status" :class="`activity-tool__status--${step.call?.status || 'requested'}`">{{ statusText(step.call?.status) }}</span>
              </header>
              <pre v-if="step.call?.error || step.call?.formattedResult || step.call?.result" :class="{ 'activity-step__error': step.call?.status === 'failed' }">{{ step.call.error || step.call.formattedResult || step.call.result }}</pre>
              <p v-else class="activity-tool-detail__empty">{{ emptyResultText(step.call) }}</p>
            </section>
          </div>
          <button
              v-for="change in fileChangesOfCalls([step.call])"
              :key="`${change.operation}:${change.path}`"
              class="activity-file"
              type="button"
              :disabled="!change.browsable"
              :title="change.path"
              @click="emit('open-workspace-file', { path: change.path })"
          >
            <span aria-hidden="true">▤</span>
            <span class="activity-file__path">{{ change.path }}</span>
            <span class="activity-file__operation">{{ fileLabel(change.operation) }}</span>
            <span v-if="change.browsable" aria-hidden="true">↗</span>
          </button>
        </div>
      </template>
      <div v-if="running && !steps.length" class="activity-step__heading activity-step__pending">{{ t('正在分析…') }}</div>
    </div>
    <div v-for="step in verificationSteps" :key="`verification:${step.key}`" class="activity-tool__verification" role="status">
      {{ t('网页需要安全验证。请在浏览器窗口手动完成，完成后告诉 Agent 继续。') }}
    </div>
    <BrowserScreenshotCard
        v-for="screenshot in screenshots"
        :key="screenshot.key"
        :session-id="sessionId"
        :attachment-id="screenshot.attachmentId"
        :name="screenshot.name"
    />
  </section>
</template>

<style scoped>
.activity-timeline { width: 100%; min-width: 0; margin: 0 0 14px; color: var(--h-text-secondary); font: 13px/1.5 var(--h-ui); }
.activity-summary { display: flex; width: fit-content; max-width: 100%; min-height: 32px; align-items: center; gap: 8px; padding: 5px 8px; border: 0; border-radius: var(--h-radius-sm); background: transparent; color: var(--h-text-secondary); text-align: left; cursor: pointer; font: inherit; transition: background-color 130ms ease; }
.activity-summary:hover { background: var(--h-surface-hover); }
.activity-summary__icon { flex: 0 0 auto; color: var(--h-text-muted); font-size: 14px; }
.activity-summary__label { min-width: 0; overflow-wrap: anywhere; }
.activity-summary__meta { color: var(--h-text-muted); }
.activity-summary__arrow { flex: 0 0 auto; color: var(--h-text-muted); font-size: 12px; transition: transform 130ms ease; }
.activity-summary__arrow--open { transform: rotate(90deg); }
.activity-steps { display: grid; gap: 8px; padding: 8px 0 0; }
.activity-step { min-width: 0; }
.activity-step__heading { display: flex; align-items: center; gap: 8px; min-height: 32px; padding: 5px 8px; color: var(--h-text-muted); font: inherit; }
.activity-step__heading--button { border: 0; border-radius: var(--h-radius-sm); background: none; cursor: pointer; text-align: left; }
.activity-step__heading--button:hover { background: var(--h-surface-hover); }
.activity-step__marker { width: 14px; flex: 0 0 14px; font-size: 12px; transition: transform 130ms ease; }
.activity-step__marker--open { transform: rotate(90deg); }
.activity-step__note { margin: 2px 0 7px 20px; font-size: 12px; line-height: 1.5; }
.activity-step__detail { margin: 6px 0 4px 30px; padding: 10px 12px; border: 0; border-radius: var(--h-radius-md); background: var(--h-surface); font-size: 13px; line-height: 1.6; overflow-wrap: anywhere; }
.activity-step__reasoning { white-space: pre-wrap; max-height: 260px; overflow: auto; }
.activity-step__source-hint { margin-bottom: 8px; color: var(--h-text-muted); font-size: 11px; }
.activity-step__technical { margin-bottom: 6px; color: var(--h-text-muted); font: 12px/1.5 var(--h-mono); }
.activity-step__detail pre { margin: 5px 0; max-height: 220px; overflow: auto; white-space: pre-wrap; font: 12px/1.6 var(--h-mono); }
.activity-step__detail--tool { display: grid; gap: 12px; }
.activity-tool-detail__heading { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 12px; margin-bottom: 8px; }
.activity-tool-detail__heading h4 { margin: 0; color: var(--h-text-secondary); font: 500 12px/1.5 var(--h-ui); }
.activity-tool-detail__heading .activity-step__technical { margin: 0; overflow-wrap: anywhere; }
.activity-tool-detail__label, .activity-tool-detail__empty { color: var(--h-text-muted); font: 12px/1.5 var(--h-ui); }
.activity-tool-detail__label { margin-bottom: 4px; }
.activity-tool-detail__empty { margin: 0; }
.activity-tool-detail__result { padding-top: 12px; border-top: 1px solid var(--h-border); }
.activity-step__detail--tool pre { margin: 0; }
.activity-step__error { color: var(--h-danger, #a3483a); }
.activity-tool { display: flex; width: 100%; min-width: 0; align-items: center; gap: 8px; min-height: 34px; padding: 7px 8px; border: 0; border-radius: var(--h-radius-sm); background: transparent; color: var(--h-text-secondary); text-align: left; cursor: pointer; font: inherit; }
.activity-tool:hover, .activity-file:hover:not(:disabled) { background: var(--h-surface-hover); }
.activity-tool__icon { flex: 0 0 auto; color: var(--h-text-muted); font-size: 14px; }
.activity-tool__action { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.activity-tool__status { margin-left: auto; flex: 0 0 auto; color: var(--h-text-muted); }
.activity-tool__status--failed { color: var(--h-danger, #a3483a); }
.activity-tool__arrow { flex: 0 0 auto; font-size: 12px; color: var(--h-text-muted); transition: transform 130ms ease; }
.activity-tool__arrow--open { transform: rotate(90deg); }
.activity-tool__verification { width: 100%; margin: 8px 0 0; padding: 10px 12px; border: 0; border-radius: var(--h-radius-md); background: var(--h-accent-soft); color: var(--h-text); font-size: 13px; line-height: 1.5; }
.activity-file { display: flex; width: min(calc(100% - 20px), 680px); min-width: 0; align-items: center; gap: 9px; margin: 6px 0 0 20px; padding: 10px 12px; border: 1px solid var(--h-border); border-radius: 8px; background: var(--h-surface); color: var(--h-text); cursor: pointer; text-align: left; font: inherit; font-size: 12px; }
.activity-file:disabled { cursor: default; }
.activity-file__path { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.activity-file__operation { margin-left: auto; color: var(--h-text-muted); }
.activity-step__pending { padding-left: 20px; }
@media (prefers-reduced-motion: reduce) {
  .activity-summary, .activity-summary__arrow, .activity-step__marker, .activity-tool__arrow { transition: none; }
  .activity-summary__icon { animation: none; }
}
</style>
