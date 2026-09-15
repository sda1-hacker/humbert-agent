<script setup>
import { computed, ref } from "vue";
import MarkdownInline from "./MarkdownInline.vue";
import { parseMarkdown } from "../../utils/markdown.js";

const props = defineProps({ content: { type: String, default: "" } });
const blocks = computed(() => parseMarkdown(props.content));
const copiedIndex = ref(-1);

async function copyCode(text, index) {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const area = document.createElement("textarea");
    area.value = text; area.style.position = "fixed"; area.style.opacity = "0";
    document.body.appendChild(area); area.select(); document.execCommand("copy"); area.remove();
  }
  copiedIndex.value = index;
  window.setTimeout(() => { if (copiedIndex.value === index) copiedIndex.value = -1; }, 1200);
}
</script>

<template>
  <div class="markdown-body">
    <template v-for="(block, index) in blocks" :key="index">
      <component :is="`h${block.level}`" v-if="block.type === 'heading'" class="md-heading"><MarkdownInline :tokens="block.inline" /></component>
      <p v-else-if="block.type === 'paragraph'" class="md-paragraph"><MarkdownInline :tokens="block.inline" /></p>
      <blockquote v-else-if="block.type === 'quote'" class="md-quote">
        <MarkdownRenderer :content="block.text || ''" />
      </blockquote>
      <component :is="block.ordered ? 'ol' : 'ul'" v-else-if="block.type === 'list'" class="md-list">
        <li v-for="(item, itemIndex) in block.items" :key="itemIndex"><MarkdownInline :tokens="item" /></li>
      </component>
      <div v-else-if="block.type === 'code'" class="md-code-block">
        <div class="md-code-toolbar"><span>{{ block.language || 'text' }}</span><button type="button" @click="copyCode(block.text, index)">{{ copiedIndex === index ? '已复制' : '复制' }}</button></div>
        <pre><code>{{ block.text }}</code></pre>
      </div>
      <div v-else-if="block.type === 'table'" class="md-table-wrap">
        <table><thead><tr><th v-for="(cell, cellIndex) in block.headers" :key="cellIndex"><MarkdownInline :tokens="cell" /></th></tr></thead>
          <tbody><tr v-for="(row, rowIndex) in block.rows" :key="rowIndex"><td v-for="(cell, cellIndex) in row" :key="cellIndex"><MarkdownInline :tokens="cell" /></td></tr></tbody></table>
      </div>
      <hr v-else-if="block.type === 'rule'" class="md-rule" />
    </template>
  </div>
</template>

<style scoped>
.markdown-body { min-width: 0; overflow-wrap: anywhere; word-break: break-word; }
.md-paragraph { margin: 0 0 .75em; white-space: pre-wrap; }
.md-paragraph:last-child { margin-bottom: 0; }
.md-heading { margin: .85em 0 .45em; line-height: 1.35; }
h1.md-heading { font-size: 1.45em; } h2.md-heading { font-size: 1.3em; } h3.md-heading { font-size: 1.17em; }
.md-list { margin: .45em 0 .8em; padding-left: 1.6em; }
.md-list li + li { margin-top: .22em; }
.md-quote { margin: .7em 0; padding: .2em .85em; border-left: 3px solid var(--h-border-strong); color: var(--h-text-muted); }
.md-quote p { margin: .35em 0; }
.md-code-block { margin: .75em 0; overflow: hidden; border: 1px solid var(--h-border); border-radius: 8px; background: var(--h-bg); }
.md-code-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 10px; padding: 6px 10px; border-bottom: 1px solid var(--h-border); color: var(--h-text-muted); font-size: 11px; }
.md-code-toolbar button { border: 0; background: transparent; color: var(--h-accent); cursor: pointer; font: inherit; }
.md-code-block pre { margin: 0; padding: 12px; overflow-x: auto; white-space: pre; font-size: 12px; line-height: 1.55; }
.md-table-wrap { margin: .75em 0; overflow-x: auto; }
table { width: 100%; border-collapse: collapse; font-size: .95em; }
th, td { padding: 7px 9px; border: 1px solid var(--h-border); text-align: left; vertical-align: top; }
th { background: var(--h-bg); font-weight: 600; }
.md-rule { border: 0; border-top: 1px solid var(--h-border); margin: 1em 0; }
:deep(.md-inline-code) { padding: .12em .36em; border-radius: 4px; background: var(--h-bg); font-size: .92em; }
:deep(.md-link) { padding: 0; border: 0; background: transparent; color: var(--h-accent); text-decoration: underline; cursor: pointer; font: inherit; }
:deep(.md-inline-image) { display: block; max-width: min(100%, 620px); max-height: 480px; margin: .65em 0; border-radius: 8px; object-fit: contain; }
</style>
