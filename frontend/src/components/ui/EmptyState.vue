<script setup>
defineProps({
  title: {
    type: String,
    required: true,
  },
  description: {
    type: String,
    default: "",
  },
  compact: {
    type: Boolean,
    default: false,
  },
});
</script>

<template>
  <div class="h-empty-state" :class="{ 'h-empty-state--compact': compact }">
    <div v-if="$slots.icon" class="h-empty-state__icon">
      <slot name="icon" />
    </div>
    <strong class="h-empty-state__title">{{ title }}</strong>
    <p v-if="description" class="h-empty-state__description">{{ description }}</p>
    <div v-if="$slots.actions" class="h-empty-state__actions">
      <slot name="actions" />
    </div>
  </div>
</template>
