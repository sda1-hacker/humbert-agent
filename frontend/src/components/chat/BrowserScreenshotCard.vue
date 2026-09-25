<script setup>
import { onMounted, onUnmounted, ref, watch } from "vue";

import { readAttachment } from "../../api/sessions.js";
import { t } from "../../i18n/index.js";
import ImagePreviewDialog from "../ui/ImagePreviewDialog.vue";

const props = defineProps({
  sessionId: { type: String, default: "" },
  attachmentId: { type: String, default: "" },
  name: { type: String, default: "" },
});

const card = ref(null);
const imageURL = ref("");
const previewVisible = ref(false);
const loadState = ref("idle");
let observer;
let isVisible = false;

async function loadImage() {
  if (loadState.value !== "idle" || !props.sessionId || !props.attachmentId) return;
  loadState.value = "loading";
  const identity = `${props.sessionId}:${props.attachmentId}`;
  try {
    const attachment = await readAttachment(props.sessionId, props.attachmentId);
    const url = attachment?.base64Data ? `data:image/png;base64,${attachment.base64Data}` : "";
    if (identity !== `${props.sessionId}:${props.attachmentId}`) return;
    if (!url) throw new Error("Image preview unavailable");
    imageURL.value = url;
    loadState.value = "ready";
  } catch {
    if (identity === `${props.sessionId}:${props.attachmentId}`) loadState.value = "failed";
  }
}

watch(() => [props.sessionId, props.attachmentId], () => {
  imageURL.value = "";
  previewVisible.value = false;
  loadState.value = "idle";
  if (isVisible) void loadImage();
});

onMounted(() => {
  if (typeof IntersectionObserver === "undefined") {
    isVisible = true;
    void loadImage();
    return;
  }
  observer = new IntersectionObserver((entries) => {
    if (!entries.some((entry) => entry.isIntersecting)) return;
    isVisible = true;
    observer?.disconnect();
    void loadImage();
  }, { rootMargin: "320px" });
  if (card.value) observer.observe(card.value);
});
onUnmounted(() => observer?.disconnect());
</script>

<template>
  <div ref="card" class="browser-screenshot">
    <button
        v-if="imageURL"
        type="button"
        class="browser-screenshot__preview"
        :title="t('图片预览')"
        @click="previewVisible = true"
    >
      <img :src="imageURL" :alt="t('网页截图')" loading="lazy" />
    </button>
    <div v-else class="browser-screenshot__placeholder">
      {{ t(loadState === 'failed' ? '无法显示截图预览' : '正在读取预览…') }}
    </div>
    <div class="browser-screenshot__footer">
      <span :title="name">{{ name }}</span>
      <a v-if="imageURL" :href="imageURL" :download="name || 'browser-screenshot.png'">{{ t('下载') }}</a>
    </div>
    <ImagePreviewDialog v-model:visible="previewVisible" :src="imageURL" :name="t('网页截图')" />
  </div>
</template>

<style scoped>
.browser-screenshot { width: min(calc(100% - 20px), 680px); margin: 7px 0 0 20px; overflow: hidden; border: 1px solid var(--h-border); border-radius: 9px; background: var(--h-surface); }
.browser-screenshot__preview { display: block; width: 100%; padding: 0; border: 0; background: var(--h-bg); cursor: zoom-in; }
.browser-screenshot__preview img { display: block; width: 100%; max-height: 280px; object-fit: contain; }
.browser-screenshot__placeholder { display: grid; min-height: 110px; place-items: center; color: var(--h-text-muted); font-size: 12px; }
.browser-screenshot__footer { display: flex; min-width: 0; align-items: center; justify-content: space-between; gap: 12px; padding: 8px 12px; color: var(--h-text-muted); font-size: 11px; }
.browser-screenshot__footer span { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.browser-screenshot__footer button, .browser-screenshot__footer a { flex: 0 0 auto; padding: 2px 0; border: 0; background: none; color: var(--h-accent); cursor: pointer; font: inherit; text-decoration: none; }
</style>
