<script setup>
import { computed, ref, watch } from "vue";
import { Message } from "../../../utils/uiMessage.js";

import { useModelStore } from "../../../stores/models.js";
import SectionCard from "../../ui/SectionCard.vue";
import ModelCapabilityBadges from "../../models/ModelCapabilityBadges.vue";

const store = useModelStore();
const saving = ref(false);
const imageModelID = ref("");

const visionModels = computed(() => store.models.filter(
    (model) => model.enabled && model.capabilities?.vision,
));

const selectedModel = computed(() => store.modelByID(imageModelID.value));

watch(
    () => store.multimedia.imageModelID,
    (value) => {
      imageModelID.value = value ?? "";
    },
    {immediate: true},
);

async function save() {
  saving.value = true;
  try {
    await store.updateMultimediaConfig({imageModelID: imageModelID.value || ""});
    Message.success("多媒体模型配置已保存");
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <div class="multimedia-settings">
    <SectionCard
        title="图片理解"
        description="当 Agent 的 Chat 模型不支持图片输入时，视觉辅助模型先理解图片，再把观察结果交给 Chat 模型继续推理和调用工具。"
    >
      <a-form layout="vertical" class="multimedia-form">
        <a-form-item label="视觉辅助模型">
          <a-select
              v-model="imageModelID"
              allow-clear
              allow-search
              placeholder="未配置（要求 Chat 模型自身支持 Vision）"
          >
            <a-option v-for="model in visionModels" :key="model.id" :value="model.id">
              {{ model.displayName }} · {{ model.providerName }}
            </a-option>
          </a-select>
          <template #extra>
            候选模型来自“模型”设置中已启用且具有 Vision Capability 的模型。
          </template>
        </a-form-item>

        <div v-if="selectedModel" class="selected-model">
          <div>
            <strong>{{ selectedModel.displayName }}</strong>
            <span>{{ selectedModel.providerName }} · {{ selectedModel.modelName }}</span>
          </div>
          <ModelCapabilityBadges :capabilities="selectedModel.capabilities" compact/>
        </div>

        <a-alert v-if="visionModels.length === 0" type="warning">
          还没有可用的视觉辅助模型。请先在“模型”中添加模型，并将 Vision Capability 设为启用或确认自动识别结果。
        </a-alert>

        <div class="actions">
          <a-button type="primary" :loading="saving" @click="save">保存</a-button>
        </div>
      </a-form>
    </SectionCard>

    <SectionCard
        tone="subtle"
        title="当前附件支持范围"
        description="设置项只代表 Humbert 已经真正实现并验证过的输入链路。"
    >
      <dl class="support-grid">
        <div>
          <dt>图片</dt>
          <dd class="supported">已支持</dd>
          <p>校验真实格式后保存，调用模型时按需水合为 Base64 图片内容。</p></div>
        <div>
          <dt>文本与源码</dt>
          <dd class="supported">已支持</dd>
          <p>按 UTF-8 提取为受控文本片段，不依赖 Provider 的原生 Files API。</p></div>
        <div>
          <dt>PDF / Office</dt>
          <dd class="supported">已支持文本提取</dd>
          <p>支持 PDF、DOCX、XLSX、PPTX 的原生文本。扫描版 PDF 尚需 OCR；提取结果限 512 KiB。</p></div>
        <div>
          <dt>音频 / 视频</dt>
          <dd>暂不支持</dd>
          <p>模型的 Audio Capability 仅是能力元数据，目前不会接收音视频附件。</p></div>
      </dl>
    </SectionCard>
  </div>
</template>

<style scoped>
.multimedia-settings {
  display: grid;
  gap: 16px;
}

.multimedia-form {
  max-width: 720px;
}

.selected-model {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 14px;
  margin-bottom: 16px;
  border: 1px solid var(--h-border);
  border-radius: 10px;
  background: var(--h-surface);
}

.selected-model > div {
  display: grid;
  gap: 3px;
}

.selected-model span {
  color: var(--h-text-muted);
  font-size: 12px;
}

.actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}

.support-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  margin: 0;
}

.support-grid > div {
  padding: 14px;
  border: 1px solid var(--h-border);
  border-radius: 10px;
}

.support-grid dt {
  font-weight: 650;
}

.support-grid dd {
  margin: 4px 0;
  color: var(--h-text-muted);
  font-size: 12px;
}

.support-grid dd.supported {
  color: var(--h-success, #2f8f5b);
}

.support-grid p {
  margin: 0;
  color: var(--h-text-muted);
  font-size: 12px;
  line-height: 1.6;
}

@media (max-width: 760px) {
  .support-grid {
    grid-template-columns: 1fr;
  }
}
</style>
