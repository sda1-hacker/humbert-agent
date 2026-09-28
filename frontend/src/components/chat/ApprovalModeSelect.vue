<script setup>
import { computed, onMounted, ref } from 'vue';
import { IconSafe } from '@arco-design/web-vue/es/icon';
import { usePermissionStore } from '../../stores/permissions.js';
import { t } from '../../i18n/index.js';
import { Message } from '../../utils/uiMessage.js';
import { useMenuTooltip } from '../../utils/menuTooltip.js';

defineProps({ disabled: Boolean });
const permissions = usePermissionStore();
const loading = ref(true);
const { tooltipVisible, onTooltipVisibleChange, dismissTooltip, onMenuVisibleChange, onTriggerLeave } = useMenuTooltip();
const options = computed(() => [
  { value: 'risk', label: t('风险审批') },
  { value: 'full', label: t('完全操作') },
  { value: 'always', label: t('请求审批') },
]);

onMounted(async () => {
  try { await permissions.load(); }
  catch (error) { Message.error(error?.message ?? String(error)); }
  finally { loading.value = false; }
});

async function changeMode(next) {
  try { await permissions.changeMode(next); }
  catch (error) { Message.error(error?.message ?? String(error)); }
}
</script>

<template>
  <span class="approval-mode-control" @pointerdown.capture="dismissTooltip" @mouseleave="onTriggerLeave">
    <a-tooltip
      :popup-visible="tooltipVisible" @popup-visible-change="onTooltipVisibleChange"
      content="风险审批：常规操作自动执行，其余确认。完全操作：无需审批，可读写工作区外目录。请求审批：每次工具操作都询问。模式影响所有 Agent，目录范围从下一轮任务生效。"
    >
      <a-select
        :model-value="permissions.mode" :options="options" :disabled="disabled || loading || permissions.busy"
        :loading="loading || permissions.busy" size="small" placeholder="审批模式" aria-label="审批模式"
        class="approval-mode-select" @change="changeMode" @popup-visible-change="onMenuVisibleChange"
      >
        <template #prefix><IconSafe aria-hidden="true"/></template>
      </a-select>
    </a-tooltip>
  </span>
</template>

<style scoped>
/* 作为输入区的次要选项，默认弱化边框，悬停和键盘聚焦时再强调。 */
.approval-mode-control {
  display: inline-flex;
  flex: 0 0 auto;
}

/* Tooltip 会克隆触发元素，使用真实容器限定深层样式，避免 scope 属性丢失。 */
:deep(.approval-mode-select) {
  width: 132px;
  min-height: 32px;
  flex: 0 0 auto;
  padding: 0 8px;
  border-color: transparent !important;
  border-radius: 8px;
  background: transparent !important;
  font-family: var(--h-ui);
  font-size: 12px;
}

:deep(.approval-mode-select:hover),
:deep(.approval-mode-select.arco-select-view-focus) {
  border-color: transparent !important;
  background: var(--h-surface-hover) !important;
}

:deep(.approval-mode-select:focus-within) {
  outline: 2px solid var(--h-accent-border);
  outline-offset: 1px;
}

:deep(.arco-select-view-prefix),
:deep(.arco-select-view-suffix) {
  color: var(--h-text-muted);
}

:deep(.arco-select-view-value) {
  color: var(--h-text-secondary) !important;
}
</style>
