<script setup>
import {
  computed,
  onMounted,
  onUnmounted,
  ref,
  watch,
} from "vue";

import {
  formatBytes,
  formatWorkspaceTime,
} from "../../utils/workspace.js";

const props = defineProps({
  selectedEntry: {
    type: Object,
    default: null,
  },
  preview: {
    type: Object,
    default: null,
  },
  loading: {
    type: Boolean,
    default: false,
  },
});

const imageViewport = ref(null);
const imageSize = ref({ width: 0, height: 0 });
const viewportSize = ref({ width: 0, height: 0 });
const manualScale = ref(null);
let resizeObserver;

const fitScale = computed(() => {
  const { width, height } = imageSize.value;
  if (!width || !height || !viewportSize.value.width || !viewportSize.value.height) return 1;
  return Math.max(0.01, Math.min(1, (viewportSize.value.width - 40) / width, (viewportSize.value.height - 40) / height));
});
const imageScale = computed(() => manualScale.value ?? fitScale.value);
const imageStyle = computed(() => ({
  width: `${imageSize.value.width * imageScale.value}px`,
  height: `${imageSize.value.height * imageScale.value}px`,
}));

function updateViewportSize() {
  viewportSize.value = {
    width: imageViewport.value?.clientWidth ?? 0,
    height: imageViewport.value?.clientHeight ?? 0,
  };
}
function onImageLoad(event) {
  imageSize.value = {
    width: event.target.naturalWidth,
    height: event.target.naturalHeight,
  };
  updateViewportSize();
}
function zoom(factor) {
  manualScale.value = Math.min(4, Math.max(0.02, Number((imageScale.value * factor).toFixed(3))));
}
function onImageWheel(event) {
  event.preventDefault();
  zoom(event.deltaY < 0 ? 1.15 : 1 / 1.15);
}

watch(() => [props.preview?.path, props.preview?.dataURL], () => {
  manualScale.value = null;
});
onMounted(() => {
  resizeObserver = new ResizeObserver(updateViewportSize);
  if (imageViewport.value) resizeObserver.observe(imageViewport.value);
});
watch(imageViewport, (current, previous) => {
  if (previous) resizeObserver?.unobserve(previous);
  if (current) {
    resizeObserver?.observe(current);
    updateViewportSize();
  }
});
onUnmounted(() => resizeObserver?.disconnect());

const languageHint = computed(() => {
  const path = props.preview?.path ?? "";
  const index = path.lastIndexOf(".");

  return index >= 0
      ? path.slice(index + 1).toUpperCase()
      : "TEXT";
});
</script>

<template>
  <section class="workspace-preview">
    <header class="workspace-preview__header">
      <div class="workspace-preview__title">
        <strong>{{ preview?.name || selectedEntry?.name || "预览" }}</strong>

        <span
            v-if="preview?.path"
            :title="preview.path"
        >
          {{ preview.path }}
        </span>
      </div>

      <div
          v-if="preview"
          class="workspace-preview__meta"
      >
        <span>{{ preview.kind === "text" ? languageHint : preview.mimeType || preview.kind }}</span>
        <span>{{ formatBytes(preview.size) }}</span>
        <span>{{ formatWorkspaceTime(preview.modifiedAt) }}</span>
      </div>
    </header>

    <div class="workspace-preview__body">
      <div
          v-if="loading"
          class="workspace-preview__empty"
      >
        正在读取预览…
      </div>

      <div
          v-else-if="!selectedEntry"
          class="workspace-preview__empty workspace-preview__empty--hero"
      >
        <div class="workspace-preview__hero-icon">▤</div>
        <strong>选择一个文件查看内容</strong>
        <span>文本和常见图片会直接预览；其它二进制文件只展示元数据。</span>
      </div>

      <div
          v-else-if="selectedEntry.type === 'directory'"
          class="workspace-preview__empty"
      >
        这是一个目录，请在左侧展开查看其中的文件。
      </div>

      <template v-else-if="preview">
        <div
            v-if="preview.truncated"
            class="workspace-preview__warning"
        >
          {{ preview.kind === "image" ? "图片超过安全预览大小，只显示文件信息。" : "文件较大，当前只显示前 512 KB 预览，原文件没有被修改。" }}
        </div>

        <!--
          工作区内容是不可信输入，因此文本永远使用 <pre> 纯文本展示。
          这里刻意不使用 v-html，避免工作区中的 HTML / JavaScript 被当成应用页面执行。
        -->
        <pre
            v-if="preview.kind === 'text'"
            class="workspace-preview__text"
        >{{ preview.content }}</pre>

        <!--
          Data URL 只由后端对白名单图片格式生成；SVG 被后端作为文本处理，
          不会进入 img 分支，避免 SVG 内嵌脚本或外部资源被浏览器执行。
        -->
        <div
            v-else-if="preview.kind === 'image' && preview.dataURL"
            class="workspace-preview__image-shell"
        >
          <div class="workspace-preview__image-toolbar" aria-label="图片缩放">
            <button type="button" aria-label="缩小图片" title="缩小" @click="zoom(1 / 1.25)">−</button>
            <span aria-live="polite">{{ Math.round(imageScale * 100) }}%</span>
            <button type="button" aria-label="放大图片" title="放大" @click="zoom(1.25)">＋</button>
            <button type="button" class="workspace-preview__image-action" title="让图片适合预览区域" @click="manualScale = null">适合窗口</button>
            <button type="button" class="workspace-preview__image-action" title="按图片原始像素显示" @click="manualScale = 1">原始大小</button>
          </div>
          <div ref="imageViewport" class="workspace-preview__image-viewport" @wheel="onImageWheel">
            <div class="workspace-preview__image-wrap">
              <img
                  :src="preview.dataURL"
                  :alt="preview.name"
                  :style="imageStyle"
                  class="workspace-preview__image"
                  @load="onImageLoad"
              />
            </div>
          </div>
        </div>

        <div
            v-else
            class="workspace-preview__empty workspace-preview__empty--hero"
        >
          <div class="workspace-preview__hero-icon">◇</div>
          <strong>这个文件不提供内嵌预览</strong>
          <span>{{ preview.mimeType || "未知二进制类型" }} · {{ formatBytes(preview.size) }}</span>
        </div>
      </template>
    </div>
  </section>
</template>

<style scoped>
.workspace-preview {
  display: grid;
  min-width: 0;
  min-height: 0;
  grid-template-rows: auto minmax(0, 1fr);
  background: var(--h-bg);
}

.workspace-preview__header {
  display: flex;
  min-height: 50px;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  padding: 9px 18px;
  border-bottom: 1px solid var(--h-border);
}

.workspace-preview__title {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.workspace-preview__title strong {
  color: var(--h-text);
  font-size: 13px;
  font-weight: 500;
}

.workspace-preview__title span {
  overflow: hidden;
  color: var(--h-text-muted);
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.workspace-preview__meta {
  display: flex;
  gap: 10px;
  color: var(--h-text-muted);
  font-family: var(--h-ui);
  font-size: 9px;
  white-space: nowrap;
}

.workspace-preview__body {
  position: relative;
  min-width: 0;
  min-height: 0;
  overflow: auto;
}

.workspace-preview__warning {
  margin: 12px 16px 0;
  padding: 8px 11px;
  border: 1px solid color-mix(in srgb, var(--h-warning) 24%, var(--h-border));
  border-radius: var(--h-radius-sm);
  background: var(--h-warning-soft);
  color: var(--h-warning);
  font-size: 11px;
}

.workspace-preview__text {
  min-height: 100%;
  margin: 0;
  padding: 18px 20px 40px;
  overflow: auto;
  background: transparent;
  color: var(--h-text-secondary);
  font: 12px/1.68 var(--h-mono);
  tab-size: 4;
  white-space: pre;
}

.workspace-preview__image-shell {
  display: grid;
  height: 100%;
  min-height: 0;
  grid-template-rows: auto minmax(0, 1fr);
}

.workspace-preview__image-toolbar {
  display: flex;
  min-height: 40px;
  align-items: center;
  justify-content: center;
  flex-wrap: wrap;
  gap: 5px;
  padding: 5px 8px;
  border-bottom: 1px solid var(--h-border);
  background: var(--h-bg);
}

.workspace-preview__image-toolbar button {
  min-width: 26px;
  height: 26px;
  padding: 0 6px;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-sm);
  background: var(--h-surface);
  color: var(--h-text-secondary);
  cursor: pointer;
}

.workspace-preview__image-toolbar button:hover {
  border-color: var(--h-accent-border);
  color: var(--h-accent);
}

.workspace-preview__image-toolbar button:focus-visible {
  outline: 2px solid var(--h-accent);
  outline-offset: 2px;
}

.workspace-preview__image-toolbar span {
  min-width: 40px;
  color: var(--h-text-muted);
  font: 11px var(--h-ui);
  text-align: center;
}

.workspace-preview__image-toolbar .workspace-preview__image-action {
  font: 10px var(--h-ui);
  white-space: nowrap;
}

.workspace-preview__image-viewport {
  min-width: 0;
  min-height: 0;
  overflow: auto;
  background-color: var(--h-surface);
  background-image:
      linear-gradient(45deg, color-mix(in srgb, var(--h-border) 52%, transparent) 25%, transparent 25%),
      linear-gradient(-45deg, color-mix(in srgb, var(--h-border) 52%, transparent) 25%, transparent 25%),
      linear-gradient(45deg, transparent 75%, color-mix(in srgb, var(--h-border) 52%, transparent) 75%),
      linear-gradient(-45deg, transparent 75%, color-mix(in srgb, var(--h-border) 52%, transparent) 75%);
  background-position: 0 0, 0 10px, 10px -10px, -10px 0;
  background-size: 20px 20px;
}

.workspace-preview__image-wrap {
  display: flex;
  width: max-content;
  min-width: 100%;
  min-height: 100%;
  align-items: center;
  justify-content: center;
  box-sizing: border-box;
  padding: 20px;
}

.workspace-preview__image {
  display: block;
  flex: none;
}

.workspace-preview__empty {
  display: grid;
  height: 100%;
  min-height: 180px;
  place-items: center;
  align-content: center;
  padding: 24px;
  color: var(--h-text-muted);
  font-size: 12px;
  text-align: center;
}

.workspace-preview__empty--hero {
  gap: 8px;
}

.workspace-preview__empty--hero strong {
  color: var(--h-text);
  font-size: 14px;
  font-weight: 500;
}

.workspace-preview__empty--hero span {
  max-width: 420px;
  line-height: 1.7;
}

.workspace-preview__hero-icon {
  color: var(--h-border-strong);
  font-size: 30px;
}
</style>
