<script setup>
import { computed, ref } from "vue";
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
const expanded = ref(true);
const openDetails = ref(new Set());
const toolCount = computed(() => props.steps.filter((step) => step.type === "tool").length);
const thinkingCount = computed(() => props.steps.filter((step) => step.type === "thinking").length);
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

</script>

<template>
  <section v-if="steps.length || running" class="activity-timeline" aria-label="执行过程">
    <button class="activity-summary" type="button" :aria-expanded="expanded" @click="expanded = !expanded">
      <span aria-hidden="true">✦</span>
      <span>{{ t(running ? '正在处理' : '处理完成') }} · {{ t('{count} 个工具', { count: toolCount }) }} · {{ t('{count} 次思考', { count: thinkingCount }) }}</span>
      <span class="activity-summary__arrow" :class="{ 'activity-summary__arrow--open': expanded }" aria-hidden="true">›</span>
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
            <span class="activity-step__marker" :class="{ 'activity-step__marker--open': openDetails.has(step.key) }" aria-hidden="true">›</span>
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
            <span class="activity-tool__icon" aria-hidden="true">◇</span>
            <span class="activity-tool__action">{{ step.call ? toolActionLabel(step.call) : t('调用工具') }}</span>
            <span class="activity-tool__status" :class="`activity-tool__status--${step.call?.status || 'requested'}`">{{ statusText(step.call?.status) }}</span>
            <span class="activity-tool__arrow" :class="{ 'activity-tool__arrow--open': openDetails.has(step.key) }" aria-hidden="true">›</span>
          </button>
          <div v-if="browserNeedsHumanVerification(step.call)" class="activity-tool__verification" role="status">
            {{ t('网页需要安全验证。请在浏览器窗口手动完成，完成后告诉 Agent 继续。') }}
          </div>
          <div v-if="openDetails.has(step.key)" class="activity-step__detail">
            <div class="activity-step__technical">{{ step.call?.name }}</div>
            <pre v-if="step.call?.formattedArguments || step.call?.arguments">{{ step.call.formattedArguments || step.call.arguments }}</pre>
            <pre v-if="step.call?.error || step.call?.formattedResult || step.call?.result" :class="{ 'activity-step__error': step.call?.status === 'failed' }">{{ step.call.error || step.call.formattedResult || step.call.result }}</pre>
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
.activity-timeline { width: 100%; min-width: 0; margin: 2px 0 16px; color: var(--h-text-secondary); }
.activity-summary { display: flex; width: 100%; align-items: center; gap: 9px; padding: 10px 12px; border: 1px solid var(--h-border); border-radius: 9px; background: var(--h-surface-hover); color: var(--h-text-secondary); text-align: left; cursor: pointer; font: inherit; font-size: 12px; }
.activity-summary__arrow { margin-left: auto; font-size: 18px; line-height: 1; transition: transform 130ms ease; }
.activity-summary__arrow--open { transform: rotate(90deg); }
.activity-steps { display: grid; gap: 12px; padding: 14px 1px 0; }
.activity-step { min-width: 0; }
.activity-step__heading { display: flex; align-items: center; gap: 7px; min-height: 25px; color: var(--h-text-muted); font-size: 12px; font-style: italic; }
.activity-step__heading--button { padding: 0; border: 0; background: none; cursor: pointer; text-align: left; }
.activity-step__marker { width: 13px; font-size: 16px; font-style: normal; transition: transform 130ms ease; }
.activity-step__marker--open { transform: rotate(90deg); }
.activity-step__note { margin: 2px 0 7px 20px; font-size: 12px; line-height: 1.5; }
.activity-step__detail { margin: 6px 0 4px 20px; padding: 9px 11px; border: 1px solid var(--h-border); border-radius: 8px; background: var(--h-surface); font-size: 12px; line-height: 1.55; overflow-wrap: anywhere; }
.activity-step__reasoning { white-space: pre-wrap; max-height: 260px; overflow: auto; }
.activity-step__source-hint { margin-bottom: 8px; color: var(--h-text-muted); font-size: 11px; }
.activity-step__technical { margin-bottom: 6px; color: var(--h-text-muted); font-family: monospace; }
.activity-step__detail pre { margin: 5px 0; max-height: 220px; overflow: auto; white-space: pre-wrap; font: inherit; font-family: monospace; }
.activity-step__error { color: var(--h-danger, #a3483a); }
.activity-tool { display: flex; width: min(100%, 700px); align-items: center; gap: 9px; min-height: 39px; padding: 8px 11px; border: 1px solid var(--h-border); border-radius: 8px; background: var(--h-surface); color: var(--h-text); text-align: left; cursor: pointer; font: inherit; font-size: 12px; }
.activity-tool:hover, .activity-file:hover:not(:disabled) { background: var(--h-surface-hover); }
.activity-tool__icon { color: var(--h-accent); font-size: 14px; }
.activity-tool__action { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.activity-tool__status { margin-left: auto; flex: 0 0 auto; color: var(--h-text-muted); }
.activity-tool__status--failed { color: var(--h-danger, #a3483a); }
.activity-tool__arrow { flex: 0 0 auto; font-size: 17px; color: var(--h-text-muted); transition: transform 130ms ease; }
.activity-tool__arrow--open { transform: rotate(90deg); }
.activity-tool__verification { width: min(calc(100% - 20px), 680px); margin: 7px 0 0 20px; padding: 9px 11px; border: 1px solid var(--h-accent-border); border-radius: 8px; background: var(--h-accent-soft); color: var(--h-text); font-size: 12px; line-height: 1.5; }
.activity-file { display: flex; width: min(calc(100% - 20px), 680px); min-width: 0; align-items: center; gap: 9px; margin: 6px 0 0 20px; padding: 10px 12px; border: 1px solid var(--h-border); border-radius: 8px; background: var(--h-surface); color: var(--h-text); cursor: pointer; text-align: left; font: inherit; font-size: 12px; }
.activity-file:disabled { cursor: default; }
.activity-file__path { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.activity-file__operation { margin-left: auto; color: var(--h-text-muted); }
.activity-step__pending { padding-left: 20px; }
</style>
