<script setup>
defineProps({ sources: { type: Array, default: () => [] } });
const emit = defineEmits(["open"]);
</script>

<template>
  <section v-if="sources.length" class="turn-sources" aria-label="知识库检索来源">
    <span class="sources-label">检索来源 · {{ sources.length }}</span>
    <div class="source-items"><button v-for="(source,i) in sources" :key="source.id" type="button" class="source-item" :title="source.header || source.title" @click="emit('open', source.id)"><span class="source-number">{{ i + 1 }}</span><span class="source-title">{{ source.title }}</span><span class="source-collection">{{ source.collection }}</span></button></div>
  </section>
</template>

<style scoped>
.turn-sources { width: 100%; max-width: 720px; margin: 16px 0 8px; }
.sources-label { font-size: 11px; color: var(--h-text-muted); }
.source-items { display: flex; flex-wrap: wrap; gap: 7px; margin-top: 7px; }
.source-item { display: inline-flex; align-items: center; gap: 7px; max-width: 100%; padding: 7px 10px; border: 1px solid var(--h-border); border-radius: 8px; background: var(--h-surface); color: var(--h-text); text-align: left; cursor: pointer; font-size: 12px; }
.source-item:hover { border-color: var(--h-accent); }
.source-number { color: var(--h-accent); font-size: 11px; }
.source-title { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 250px; }
.source-collection { color: var(--h-text-muted); max-width: 100px; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
</style>
