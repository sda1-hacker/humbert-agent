<script setup>
defineProps({
  eyebrow: {
    type: String,
    default: "",
  },
  title: {
    type: String,
    required: true,
  },
  description: {
    type: String,
    default: "",
  },
});
</script>

<template>
  <header class="h-page-header">
    <div class="h-page-header__copy">
      <div v-if="eyebrow" class="h-page-header__eyebrow">{{ eyebrow }}</div>
      <h1 class="h-page-header__title">{{ title }}</h1>
      <p v-if="description" class="h-page-header__description">{{ description }}</p>
    </div>

    <div v-if="$slots.aside || $slots.actions" class="h-page-header__side">
      <div v-if="$slots.aside" class="h-page-header__aside">
        <slot name="aside" />
      </div>
      <div v-if="$slots.actions" class="h-page-header__actions">
        <slot name="actions" />
      </div>
    </div>
  </header>
</template>
