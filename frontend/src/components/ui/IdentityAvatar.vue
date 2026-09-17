<script setup>
import { computed, ref, watch } from "vue";

const props = defineProps({
  src: { type: String, default: "" },
  name: { type: String, default: "" },
  size: { type: Number, default: 30 },
});

const failed = ref(false);
watch(() => props.src, () => { failed.value = false; });

const initial = computed(() => {
  const value = String(props.name || "?").trim();
  return Array.from(value)[0]?.toUpperCase() || "?";
});
</script>

<template>
  <span
      class="identity-avatar"
      :style="{ width: `${size}px`, height: `${size}px`, fontSize: `${Math.max(10, Math.round(size * 0.38))}px` }"
      :title="name"
      aria-hidden="true"
  >
    <img v-if="src && !failed" :src="src" alt="" @error="failed = true" />
    <span v-else>{{ initial }}</span>
  </span>
</template>

<style scoped>
.identity-avatar {
  display: inline-grid;
  flex: 0 0 auto;
  place-items: center;
  overflow: hidden;
  border: 1px solid color-mix(in srgb, var(--h-accent) 20%, var(--h-border));
  border-radius: 50%;
  background: var(--h-accent-soft);
  color: var(--h-accent);
  font-weight: 650;
  line-height: 1;
  user-select: none;
}

.identity-avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
</style>
