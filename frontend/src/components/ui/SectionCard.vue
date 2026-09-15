<script setup>
import {
  computed,
  useSlots,
} from "vue";

const props = defineProps({
  title: {
    type: String,
    default: "",
  },
  description: {
    type: String,
    default: "",
  },
  tone: {
    type: String,
    default: "default",
    validator: (value) => ["default", "subtle", "warning"].includes(value),
  },
  compact: {
    type: Boolean,
    default: false,
  },
});

const slots = useSlots();

const hasHeader = computed(() => (
  Boolean(props.title) ||
  Boolean(props.description) ||
  Boolean(slots.header) ||
  Boolean(slots.actions)
));
</script>

<template>
  <section
      class="h-section-card"
      :class="[
        `h-section-card--${tone}`,
        { 'h-section-card--compact': compact },
      ]"
  >
    <header v-if="hasHeader" class="h-section-card__header">
      <slot name="header">
        <div class="h-section-card__copy">
          <h2 v-if="title" class="h-section-card__title">{{ title }}</h2>
          <p v-if="description" class="h-section-card__description">{{ description }}</p>
        </div>
      </slot>

      <div v-if="$slots.actions" class="h-section-card__actions">
        <slot name="actions" />
      </div>
    </header>

    <div class="h-section-card__body">
      <slot />
    </div>

    <footer v-if="$slots.footer" class="h-section-card__footer">
      <slot name="footer" />
    </footer>
  </section>
</template>
