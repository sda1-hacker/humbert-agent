<script setup>
import {
  computed,
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
            class="workspace-preview__image-wrap"
        >
          <img
              :src="preview.dataURL"
              :alt="preview.name"
              class="workspace-preview__image"
          />
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

.workspace-preview__image-wrap {
  display: grid;
  min-height: 100%;
  place-items: center;
  padding: 26px;
  background-color: var(--h-surface);
  background-image:
      linear-gradient(45deg, color-mix(in srgb, var(--h-border) 52%, transparent) 25%, transparent 25%),
      linear-gradient(-45deg, color-mix(in srgb, var(--h-border) 52%, transparent) 25%, transparent 25%),
      linear-gradient(45deg, transparent 75%, color-mix(in srgb, var(--h-border) 52%, transparent) 75%),
      linear-gradient(-45deg, transparent 75%, color-mix(in srgb, var(--h-border) 52%, transparent) 75%);
  background-position: 0 0, 0 10px, 10px -10px, -10px 0;
  background-size: 20px 20px;
}

.workspace-preview__image {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
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
