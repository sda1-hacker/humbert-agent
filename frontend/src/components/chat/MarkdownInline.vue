<script setup>
import { defineAsyncComponent } from "vue";
import { safeUrl } from "../../utils/markdown.js";
import { Browser } from "@wailsio/runtime";
import { Message } from "../../utils/uiMessage.js";

defineProps({ tokens: { type: Array, default: () => [] } });
const MathFormula = defineAsyncComponent(() => import("./MathFormula.vue"));

function openLink(url) {
  const safe = safeUrl(url);
  if (!safe) return;
  if (/^https?:/i.test(safe)) {
    void Browser.OpenURL(safe).catch((error) => Message.error(error?.message || String(error)));
  } else {
    window.open(safe, "_blank", "noopener,noreferrer");
  }
}

function openImage(url) {
  const safe = safeUrl(url);
  if (safe) window.open(safe, "_blank", "noopener,noreferrer");
}
</script>

<template>
  <template v-for="(token, index) in tokens" :key="index">
    <code v-if="token.type === 'code'" class="md-inline-code">{{ token.text }}</code>
    <MathFormula v-else-if="token.type === 'math'" :text="token.text" :raw="token.raw" :display="token.display" />
    <strong v-else-if="token.type === 'strong'"><MarkdownInline :tokens="token.children" /></strong>
    <em v-else-if="token.type === 'em'"><MarkdownInline :tokens="token.children" /></em>
    <del v-else-if="token.type === 'strike'"><MarkdownInline :tokens="token.children" /></del>
    <button v-else-if="token.type === 'link'" type="button" class="md-link" @click="openLink(token.url)">{{ token.text }}</button>
    <img v-else-if="token.type === 'image'" class="md-inline-image" :src="token.url" :alt="token.alt" loading="lazy" />
    <button v-else-if="token.type === 'remote-image'" type="button" class="md-link" @click="openImage(token.url)">打开远程图片{{ token.alt ? `：${token.alt}` : '' }}</button>
    <template v-else>{{ token.text }}</template>
  </template>
</template>
