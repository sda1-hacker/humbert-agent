<script setup>
import { safeUrl } from "../../utils/markdown.js";

defineProps({ tokens: { type: Array, default: () => [] } });

function openLink(url) {
  const safe = safeUrl(url);
  if (!safe) return;
  window.open(safe, "_blank", "noopener,noreferrer");
}
</script>

<template>
  <template v-for="(token, index) in tokens" :key="index">
    <code v-if="token.type === 'code'" class="md-inline-code">{{ token.text }}</code>
    <strong v-else-if="token.type === 'strong'">{{ token.text }}</strong>
    <em v-else-if="token.type === 'em'">{{ token.text }}</em>
    <del v-else-if="token.type === 'strike'">{{ token.text }}</del>
    <button v-else-if="token.type === 'link'" type="button" class="md-link" @click="openLink(token.url)">{{ token.text }}</button>
    <img v-else-if="token.type === 'image'" class="md-inline-image" :src="token.url" :alt="token.alt" loading="lazy" />
    <button v-else-if="token.type === 'remote-image'" type="button" class="md-link" @click="openLink(token.url)">打开远程图片{{ token.alt ? `：${token.alt}` : '' }}</button>
    <template v-else>{{ token.text }}</template>
  </template>
</template>
