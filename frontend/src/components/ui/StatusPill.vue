<script setup>
import { computed } from "vue";

const props = defineProps({
  label: {
    type: String,
    required: true,
  },
  tone: {
    type: String,
    default: "neutral",
    validator: (value) => ["neutral", "success", "warning", "danger", "info", "ok", "pending", "error", "muted"].includes(value),
  },
  dot: {
    type: Boolean,
    default: true,
  },
});

const normalizedTone = computed(() => ({
  ok: "success",
  pending: "info",
  error: "danger",
  muted: "neutral",
}[props.tone] ?? props.tone));
</script>

<template>
  <span class="h-status-pill" :class="`h-status-pill--${normalizedTone}`">
    <span v-if="dot" class="h-status-pill__dot" aria-hidden="true"></span>
    <slot>{{ label }}</slot>
  </span>
</template>
