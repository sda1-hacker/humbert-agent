<script setup>
import { computed } from "vue";

const props = defineProps({
  capabilities: {
    type: Object,
    default: () => ({}),
  },
  compact: {
    type: Boolean,
    default: false,
  },
  onlyEnabled: {
    type: Boolean,
    default: true,
  },
});

const items = computed(() => {
  const catalog = [
    ["tools", "Tools"],
    ["vision", "Vision"],
    ["files", "Files"],
    ["reasoning", "Reasoning"],
    ["json", "JSON"],
    ["audio", "Audio"],
  ];
  return catalog
      .filter(([key]) => !props.onlyEnabled || Boolean(props.capabilities?.[key]))
      .map(([key, label]) => ({ key, label, enabled: Boolean(props.capabilities?.[key]) }));
});
</script>

<template>
  <div class="capability-badges" :class="{ 'capability-badges--compact': compact }">
    <span
        v-for="item in items"
        :key="item.key"
        class="capability-badge"
        :class="{ 'capability-badge--disabled': !item.enabled }"
    >
      {{ item.label }}
    </span>
    <span v-if="items.length === 0" class="capability-empty">无已声明能力</span>
  </div>
</template>

<style scoped>
.capability-badges {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}
.capability-badge {
  padding: 2px 6px;
  border: 1px solid var(--h-border);
  border-radius: 999px;
  background: var(--h-surface-soft, var(--h-surface));
  color: var(--h-text-secondary);
  font-size: 9px;
  line-height: 1.4;
}
.capability-badge--disabled {
  opacity: 0.45;
  text-decoration: line-through;
}
.capability-badges--compact {
  gap: 3px;
}
.capability-badges--compact .capability-badge {
  padding: 1px 5px;
  font-size: 8px;
}
.capability-empty {
  color: var(--h-text-muted);
  font-size: 9px;
}
</style>
