<script setup>
import { computed } from "vue";
import MarkdownRenderer from "../chat/MarkdownRenderer.vue";
import { sourceLocationSummary } from "../../utils/knowledgeSources.js";

const props = defineProps({ visible: Boolean, source: { type: Object, default: null }, loading: Boolean, error: { type: String, default: "" } });
const emit = defineEmits(["update:visible"]);
const location = computed(() => sourceLocationSummary(props.source?.locators));
</script>

<template>
  <a-modal :visible="visible" :title="source?.title || '资料来源'" :width="820" :align-center="true" :modal-style="{maxWidth:'calc(100vw - 32px)',maxHeight:'90vh',display:'inline-flex',flexDirection:'column'}" :body-style="{minHeight:0,overflowY:'auto',padding:'20px 24px'}" :footer="false" @update:visible="emit('update:visible', $event)">
    <a-spin :loading="loading" class="source-loading">
      <a-alert v-if="error" type="warning" class="source-warning">{{ error }}<span v-if="source">；下方展示当时保存在对话中的检索快照。</span></a-alert>
      <template v-if="source">
        <div class="source-meta"><span>{{ source.collection }}</span><span>版本 {{ source.revision }}</span></div>
        <div class="source-location"><span>{{ location.label }}</span><span v-if="location.approximate" class="source-location-note">近似位置</span></div>
        <p v-if="source.header" class="source-header">{{ source.header }}</p>
        <div class="source-body"><MarkdownRenderer :content="source.content || source.excerpt" /></div>
        <p v-if="source.truncated" class="source-note">此处展示受长度限制的原文片段，可在知识库中下载完整原件。</p>
        <details class="source-identifiers"><summary>查看来源标识</summary><dl><dt>文档</dt><dd>{{ source.document_id }}</dd><dt>分块</dt><dd>{{ source.chunk_id }}</dd></dl></details>
      </template>
    </a-spin>
  </a-modal>
</template>

<style scoped>
.source-loading { display: block; width: 100%; min-width: 0; }
.source-warning { margin-bottom: 12px; }
.source-meta { display: flex; flex-wrap: wrap; gap: 6px 12px; color: var(--h-text-muted); font-size: 12px; }
.source-meta span + span { padding-left: 12px; border-left: 1px solid var(--h-border); }
.source-location { display: flex; flex-wrap: wrap; align-items: baseline; gap: 6px 10px; margin-top: 10px; color: var(--h-text-secondary); font-size: 12px; }
.source-location-note { color: var(--h-text-muted); }
.source-header { margin: 12px 0 0; color: var(--h-accent); font-size: 12px; line-height: 1.6; overflow-wrap: anywhere; }
.source-body { min-width: 0; margin-top: 16px; padding-top: 16px; border-top: 1px solid var(--h-border); color: var(--h-text-secondary); font-size: 14px; line-height: 1.85; }
/* PDF 转换产生的单换行按 Markdown 软换行排版；不改动原文、代码块和表格。 */
.source-body :deep(.md-paragraph) { white-space: normal; }
.source-body :deep(.md-heading:first-child) { margin-top: 0; }
.source-note { color: var(--h-text-muted); font-size: 12px; line-height: 1.7; }
.source-identifiers { margin-top: 20px; padding-top: 12px; border-top: 1px solid var(--h-border); color: var(--h-text-muted); font-size: 12px; }
.source-identifiers summary { width: fit-content; cursor: pointer; }
.source-identifiers dl { display: grid; grid-template-columns: auto minmax(0,1fr); gap: 8px 14px; margin-bottom: 0; }
.source-identifiers dd { margin: 0; font-family: var(--h-mono); overflow-wrap: anywhere; }
</style>
