<script setup>
import { computed, onUnmounted, ref, watch } from "vue";
import { discoverModels } from "../../../api/models.js";
import { useModelStore } from "../../../stores/models.js";
import { t } from "../../../i18n/index.js";

const props = defineProps({ visible: Boolean, providerId: String });
const emit = defineEmits(["update:visible"]);
const store = useModelStore();
const names = ref([]), chosen = ref([]), query = ref(""), error = ref(""), failures = ref([]);
const loading = ref(false), saving = ref(false);
let serial = 0;
const added = computed(() => new Set(store.models.filter(model => model.providerID === props.providerId).map(model => model.modelName)));
const filtered = computed(() => names.value.filter(name => name.toLowerCase().includes(query.value.toLowerCase())));
async function load() {
  const token = ++serial;
  loading.value = true; error.value = "";
  try { const result = await discoverModels(props.providerId); if (token === serial) names.value = result || []; }
  catch (e) { if (token === serial) error.value = e?.message || String(e); }
  finally { if (token === serial) loading.value = false; }
}
watch(() => props.visible, visible => {
  serial++;
  if (visible) { names.value = []; chosen.value = []; query.value = ""; failures.value = []; void load(); }
});
onUnmounted(() => serial++);
async function save() {
  if (saving.value || !chosen.value.length) return false;
  saving.value = true;
  try {
    failures.value = await store.importModels(props.providerId, chosen.value);
    chosen.value = failures.value.map(value => value.name);
    return failures.value.length === 0;
  } finally { saving.value = false; }
}
</script>

<template>
  <a-modal :visible="visible" :title="t('读取模型')" :width="640" :modal-style="{ maxWidth: 'calc(100vw - 32px)', maxHeight: '90vh', display: 'inline-flex', flexDirection: 'column' }" :body-style="{ minHeight: 0, overflowY: 'auto' }" :mask-closable="false" :esc-to-close="!saving" :on-before-cancel="() => !saving" :on-before-ok="save" :ok-loading="saving" :ok-button-props="{ disabled: loading || !chosen.length }" :ok-text="t('添加选中模型')" :cancel-text="t('取消')" @update:visible="emit('update:visible', $event)">
    <div class="discovery">
      <p>读取服务端实际提供的模型。窗口与能力优先使用预设，未知模型以保守窗口添加，使用前请核对参数。</p>
      <a-input v-model="query" allow-clear placeholder="搜索模型" />
      <a-alert v-if="error" type="error">{{ error }} <a-button type="text" :loading="loading" @click="load">重试</a-button></a-alert>
      <a-spin v-if="loading" />
      <p v-else-if="!names.length && !error">服务没有返回可用模型，也可以使用自定义模型。</p>
      <a-checkbox-group v-model="chosen" :disabled="saving" class="model-options">
        <a-checkbox v-for="name in filtered" :key="name" :value="name" :disabled="added.has(name)"><code>{{ name }}</code><span v-if="added.has(name)"> · {{ t('已添加') }}</span></a-checkbox>
      </a-checkbox-group>
      <p>{{ t('已选择 {count} 个模型', { count: chosen.length }) }}</p>
      <a-alert v-if="failures.length" type="warning"><p>部分模型添加失败，已成功的模型会保留。可以重试剩余项。</p><p v-for="failure in failures" :key="failure.name">{{ failure.name }}：{{ failure.message }}</p></a-alert>
    </div>
  </a-modal>
</template>

<style scoped>
.discovery { display: grid; gap: 16px; min-width: 0; }
.discovery p { margin: 0; color: var(--h-text-muted); font-size: 12px; line-height: 1.65; }
.model-options { display: grid; gap: 12px; min-width: 0; }
.model-options :deep(.arco-checkbox-label) { white-space: normal; overflow-wrap: anywhere; }
.model-options code { font: 12px/1.6 var(--h-mono); }
</style>
