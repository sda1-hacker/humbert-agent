<script setup>
defineProps({ leftOpen: Boolean, rightOpen: Boolean, leftDisabled: Boolean, rightDisabled: Boolean });
const emit = defineEmits(["toggle-left", "toggle-right"]);
</script>

<template>
  <div class="window-chrome">
    <button type="button" class="window-chrome__toggle" :disabled="leftDisabled" :aria-label="leftOpen ? '收起左侧导航' : '展开左侧导航'" :title="leftOpen ? '收起左侧导航' : '展开左侧导航'" :aria-pressed="leftOpen" @click="emit('toggle-left')">
      <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16"/></svg>
    </button>
    <div class="window-chrome__drag"></div>
    <button type="button" class="window-chrome__toggle" :disabled="rightDisabled" :aria-label="rightOpen ? '收起右侧面板' : '展开右侧面板'" :title="rightOpen ? '收起右侧面板' : '展开右侧面板'" :aria-pressed="rightOpen" @click="emit('toggle-right')">
      <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M15 4v16"/></svg>
    </button>
  </div>
</template>

<style scoped>
.window-chrome { --wails-draggable: drag; display: flex; width: 100%; height: 38px; align-items: center; gap: 8px; padding: 0 13px 0 78px; border-bottom: 1px solid var(--h-border); background: var(--h-bg); }
.window-chrome__drag { flex: 1; height: 100%; }
.window-chrome__toggle { --wails-draggable: no-drag; display: grid; width: 29px; height: 29px; place-items: center; border: 0; border-radius: 7px; background: transparent; color: var(--h-text-secondary); cursor: pointer; }
.window-chrome__toggle:hover, .window-chrome__toggle[aria-pressed="true"] { background: var(--h-accent-soft); color: var(--h-accent); }
.window-chrome__toggle:disabled { opacity: .35; cursor: default; }
.window-chrome__toggle svg { width: 18px; height: 18px; fill: none; stroke: currentColor; stroke-width: 1.7; stroke-linecap: round; }
</style>
