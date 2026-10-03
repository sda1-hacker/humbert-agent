<script setup>
import { IconCodeSandbox } from '@arco-design/web-vue/es/icon';
import { ref } from 'vue';

defineProps({ skill: { type: Object, required: true }, editable: Boolean, focusable: { type: Boolean, default: true } });
const focused = ref(false);
</script>

<template>
  <a-tooltip :content="skill.description || skill.name" :popup-visible="focused ? true : undefined" position="top" :content-style="{ maxWidth: 'min(360px, calc(100vw - 32px))', maxHeight: 'min(300px, calc(100vh - 80px))', overflow: 'auto', font: '13px/1.6 var(--h-ui)', textAlign: 'left', whiteSpace: 'normal' }">
    <span class="skill-reference" :class="{ 'skill-reference--editable': editable }" :tabindex="editable || !focusable ? -1 : 0" :aria-label="skill.alias || skill.name" @focus="focused = true" @blur="focused = false">
      <IconCodeSandbox aria-hidden="true"/>
      <span class="skill-reference__label">{{ skill.alias || skill.name }}</span>
    </span>
  </a-tooltip>
</template>

<style scoped>
.skill-reference { display: inline-flex; max-width: 100%; align-items: center; gap: 4px; padding: 0 3px; border-radius: 4px; color: var(--h-accent); font: 500 .94em/1.5 var(--h-ui); vertical-align: baseline; cursor: help; white-space: nowrap; }
.skill-reference .arco-icon { flex: 0 0 auto; font-size: 1.05em; align-self: center; }
.skill-reference__label { overflow: hidden; text-overflow: ellipsis; text-decoration: underline dotted; text-underline-offset: 3px; }
.skill-reference:hover, .skill-reference:focus-visible { background: var(--h-accent-soft); }
.skill-reference--editable { background: var(--h-accent-soft); padding: 1px 5px; }
</style>
