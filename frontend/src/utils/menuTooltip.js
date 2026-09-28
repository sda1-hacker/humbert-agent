import { ref } from 'vue';

// 悬停说明与点击菜单互斥；点击后等鼠标离开，再允许下一次悬停显示。
// 同时拦住 Arco 延迟到达的显示事件，避免菜单关闭时旧提示马上重新弹出。
export function useMenuTooltip() {
  const tooltipVisible = ref(false);
  let menuVisible = false;
  let dismissed = false;

  function onTooltipVisibleChange(visible) {
    tooltipVisible.value = Boolean(visible) && !menuVisible && !dismissed;
  }

  function dismissTooltip() {
    dismissed = true;
    tooltipVisible.value = false;
  }

  function onMenuVisibleChange(visible) {
    menuVisible = Boolean(visible);
    if (menuVisible) dismissTooltip();
  }

  function onTriggerLeave() {
    dismissed = false;
    tooltipVisible.value = false;
  }

  return { tooltipVisible, onTooltipVisibleChange, dismissTooltip, onMenuVisibleChange, onTriggerLeave };
}
