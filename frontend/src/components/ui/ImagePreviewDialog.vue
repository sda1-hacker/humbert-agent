<script setup>
import { computed, ref, watch } from "vue";

const props = defineProps({
  visible: { type: Boolean, default: false },
  src: { type: String, default: "" },
  name: { type: String, default: "图片预览" },
});

const emit = defineEmits(["update:visible"]);
const scale = ref(1);

watch(() => props.visible, (visible) => {
  if (visible) scale.value = 1;
});

const imageStyle = computed(() => ({ transform: `scale(${scale.value})` }));

function zoom(delta) {
  scale.value = Math.min(4, Math.max(0.5, Number((scale.value + delta).toFixed(2))));
}

function handleWheel(event) {
  event.preventDefault();
  zoom(event.deltaY < 0 ? 0.15 : -0.15);
}
</script>

<template>
  <a-modal
      :visible="visible"
      :title="name || '图片预览'"
      :width="'min(92vw, 1120px)'"
      :footer="false"
      modal-class="image-preview-modal"
      @cancel="emit('update:visible', false)"
  >
    <div class="image-preview-toolbar">
      <button type="button" aria-label="缩小" @click="zoom(-0.2)">−</button>
      <span>{{ Math.round(scale * 100) }}%</span>
      <button type="button" aria-label="放大" @click="zoom(0.2)">＋</button>
      <button type="button" @click="scale = 1">适合窗口</button>
    </div>
    <div class="image-preview-stage" @wheel="handleWheel">
      <img v-if="src" :src="src" :alt="name" :style="imageStyle" />
    </div>
  </a-modal>
</template>

<style scoped>
.image-preview-toolbar { display: flex; align-items: center; justify-content: center; gap: 8px; margin-bottom: 10px; }
.image-preview-toolbar button { min-width: 30px; height: 28px; padding: 0 9px; border: 1px solid var(--h-border); border-radius: 6px; background: var(--h-surface); color: var(--h-text); cursor: pointer; }
.image-preview-toolbar span { min-width: 48px; color: var(--h-text-muted); font-size: 11px; text-align: center; }
.image-preview-stage { display: grid; width: 100%; height: min(72vh, 760px); place-items: center; overflow: auto; border-radius: 9px; background: color-mix(in srgb, var(--h-bg) 85%, #000); }
.image-preview-stage img { display: block; max-width: 100%; max-height: 100%; object-fit: contain; transform-origin: center; transition: transform 100ms ease; }
</style>
