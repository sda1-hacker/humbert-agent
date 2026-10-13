<script setup>
import { computed } from "vue";
import katex from "katex";
import "katex/dist/katex.min.css";

const props = defineProps({
  text: { type: String, default: "" },
  raw: { type: String, default: "" },
  display: { type: Boolean, default: false },
});

const html = computed(() => {
  if (props.text.length > 16384) return "";
  try {
    return katex.renderToString(props.text, {
      displayMode: props.display,
      output: "htmlAndMathml",
      throwOnError: true,
      strict: "ignore",
      trust: false,
      maxExpand: 1000,
      maxSize: 20,
      macros: {},
    });
  } catch {
    return "";
  }
});
</script>

<template>
  <!-- v-html 只接收 KaTeX 的受限渲染结果，Markdown 原文与错误信息始终走文本插值。 -->
  <span v-if="html" class="md-math" :class="{ 'md-math-display': display }" v-html="html" />
  <span v-else class="md-math-fallback" :class="{ 'md-math-display': display }">{{ raw || text }}</span>
</template>

<style scoped>
.md-math { white-space: normal; word-break: normal; overflow-wrap: normal; }
.md-math:not(.md-math-display) { display: inline-block; max-width: 100%; padding: .1em 0; overflow-x: auto; overflow-y: hidden; vertical-align: middle; line-height: 1.2; }
.md-math :deep(.katex) { font-size: 1.1em; }
.md-math-display { display: block; max-width: 100%; margin: .85em 0; padding: .2em 0; overflow-x: auto; overflow-y: hidden; }
.md-math-display :deep(.katex-display) { width: max-content; min-width: 100%; margin: 0; }
.md-math-fallback { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
