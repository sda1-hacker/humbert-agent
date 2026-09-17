<script setup>
import { onMounted, ref, watch } from "vue";
import { Message } from "@arco-design/web-vue";
import { readAttachment } from "../../api/sessions.js";
import ImagePreviewDialog from "../ui/ImagePreviewDialog.vue";

const props = defineProps({
  sessionId: { type: String, required: true },
  attachments: { type: Array, default: () => [] },
});

const imageSources = ref({});
const loadingImages = ref({});
const preview = ref({ visible: false, src: "", name: "" });
const imageLoadPromises = new Map();

function humanSize(value) {
  const bytes = Number(value || 0);
  if (!Number.isFinite(bytes) || bytes <= 0) return "";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`;
}

function base64ToBlob(base64, mimeType) {
  const binary = atob(base64 || "");
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return new Blob([bytes], { type: mimeType || "application/octet-stream" });
}

async function loadImage(attachment) {
  const id = attachment?.id;
  if (!id || imageSources.value[id]) return;
  if (imageLoadPromises.has(id)) return imageLoadPromises.get(id);

  const pending = (async () => {
    loadingImages.value = { ...loadingImages.value, [id]: true };
    try {
      const result = await readAttachment(props.sessionId, id);
      imageSources.value = {
        ...imageSources.value,
        [id]: `data:${attachment.mimeType || "image/*"};base64,${result?.base64Data || ""}`,
      };
    } catch (error) {
      Message.error(error?.message || String(error));
    } finally {
      imageLoadPromises.delete(id);
      const next = { ...loadingImages.value };
      delete next[id];
      loadingImages.value = next;
    }
  })();
  imageLoadPromises.set(id, pending);
  return pending;
}

async function downloadAttachment(attachment) {
  try {
    const result = await readAttachment(props.sessionId, attachment.id);
    const blob = base64ToBlob(result?.base64Data, attachment.mimeType);
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = attachment.name || "attachment";
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(url);
  } catch (error) {
    Message.error(error?.message || String(error));
  }
}

async function previewImage(attachment) {
  await loadImage(attachment);
  const src = imageSources.value[attachment?.id];
  if (!src) return;
  preview.value = { visible: true, src, name: attachment?.name || "图片预览" };
}

async function loadVisibleImages() {
  await Promise.all(
      props.attachments
          .filter((item) => item?.kind === "image")
          .map((item) => loadImage(item)),
  );
}

onMounted(() => void loadVisibleImages());
watch(() => props.attachments, () => void loadVisibleImages(), { deep: true });
</script>

<template>
  <div v-if="attachments.length" class="message-attachments">
    <div
        v-for="attachment in attachments"
        :key="attachment.id"
        class="attachment"
        :class="{ 'attachment--image': attachment.kind === 'image' }"
    >
      <button
          v-if="attachment.kind === 'image'"
          type="button"
          class="attachment-image-button"
          :title="`预览 ${attachment.name}`"
          @click="previewImage(attachment)"
      >
        <img
            v-if="imageSources[attachment.id]"
            :src="imageSources[attachment.id]"
            :alt="attachment.name"
            class="attachment-image"
        />
        <span v-else class="attachment-loading">读取图片…</span>
      </button>

      <button
          v-if="attachment.kind === 'image'"
          type="button"
          class="attachment-download"
          :title="`下载 ${attachment.name}`"
          @click="downloadAttachment(attachment)"
      >下载</button>

      <button
          v-else
          type="button"
          class="attachment-file"
          @click="downloadAttachment(attachment)"
      >
        <span class="attachment-file__icon">📎</span>
        <span class="attachment-file__body">
          <span class="attachment-file__name">{{ attachment.name }}</span>
          <span class="attachment-file__meta">
            {{ attachment.mimeType || "文件" }}<template v-if="humanSize(attachment.sizeBytes)"> · {{ humanSize(attachment.sizeBytes) }}</template>
          </span>
        </span>
      </button>
    </div>
    <ImagePreviewDialog
        v-model:visible="preview.visible"
        :src="preview.src"
        :name="preview.name"
    />
  </div>
</template>

<style scoped>
.message-attachments {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 9px;
}

.attachment-image-button,
.attachment-file {
  border: 1px solid var(--h-border);
  background: var(--h-bg);
  color: var(--h-text);
  cursor: pointer;
}

.attachment-image-button {
  display: block;
  max-width: 280px;
  padding: 0;
  overflow: hidden;
  border-radius: 9px;
}

.attachment--image { position: relative; }
.attachment-download { position: absolute; right: 7px; bottom: 7px; padding: 3px 7px; border: 1px solid rgb(255 255 255 / 45%); border-radius: 5px; background: rgb(20 24 30 / 72%); color: #fff; font-size: 10px; cursor: pointer; opacity: 0; transition: opacity 120ms ease; }
.attachment--image:hover .attachment-download, .attachment-download:focus-visible { opacity: 1; }

.attachment-image {
  display: block;
  width: auto;
  max-width: 280px;
  max-height: 240px;
  object-fit: contain;
}

.attachment-loading {
  display: block;
  padding: 24px 30px;
  color: var(--h-text-secondary);
  font-size: 12px;
}

.attachment-file {
  display: flex;
  max-width: 320px;
  min-width: 190px;
  align-items: center;
  gap: 9px;
  padding: 9px 11px;
  border-radius: 9px;
  text-align: left;
}

.attachment-file:hover,
.attachment-image-button:hover {
  border-color: var(--h-border-strong);
}

.attachment-file__icon { flex: 0 0 auto; }
.attachment-file__body { display: flex; min-width: 0; flex-direction: column; }
.attachment-file__name { overflow: hidden; font-size: 12px; text-overflow: ellipsis; white-space: nowrap; }
.attachment-file__meta { margin-top: 2px; color: var(--h-text-secondary); font-size: 10px; }
</style>
