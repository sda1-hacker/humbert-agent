<script setup>
import { computed, onMounted, ref } from 'vue';
import { IconDown, IconLoading, IconSafe } from '@arco-design/web-vue/es/icon';
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
const currentLabel = computed(() => options.value.find(option => option.value === permissions.mode)?.label || t('审批模式'));

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
      <a-dropdown trigger="click" position="top" :disabled="disabled || loading || permissions.busy" @select="changeMode" @popup-visible-change="onMenuVisibleChange">
        <button type="button" class="approval-mode-select" :disabled="disabled || loading || permissions.busy" :aria-label="t('审批模式')" aria-haspopup="menu">
          <IconLoading v-if="loading || permissions.busy" aria-hidden="true"/>
          <IconSafe v-else aria-hidden="true"/>
          <span>{{ currentLabel }}</span>
          <IconDown class="approval-mode-arrow" aria-hidden="true"/>
        </button>
        <template #content>
          <a-doption v-for="option in options" :key="option.value" :value="option.value" :class="{ 'approval-mode-option--selected': option.value === permissions.mode }">{{ option.label }}</a-doption>
        </template>
      </a-dropdown>
    </a-tooltip>
  </span>
</template>

<style scoped>
/* 作为输入区的次要选项，默认弱化边框，悬停和键盘聚焦时再强调。 */
.approval-mode-control {
  display: inline-flex;
  flex: 0 0 auto;
}

.approval-mode-select {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  width: auto;
  min-height: 32px;
  flex: 0 0 auto;
  padding: 0 6px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--h-text-secondary);
  font: 13px/1.4 var(--h-ui);
  white-space: nowrap;
  cursor: pointer;
}

.approval-mode-select:hover:not(:disabled) {
  background: var(--h-surface-hover);
}

.approval-mode-select:focus-visible {
  outline: 2px solid var(--h-accent-border);
  outline-offset: 1px;
}

.approval-mode-select .arco-icon {
  flex: 0 0 auto;
  color: var(--h-text-muted);
  font-size: 14px;
}

.approval-mode-select .approval-mode-arrow {
  margin-left: 2px;
  font-size: 11px;
}

.approval-mode-select:disabled {
  opacity: .5;
  cursor: not-allowed;
}

.approval-mode-option--selected {
  color: var(--h-accent);
  font-weight: 500;
}
</style>
