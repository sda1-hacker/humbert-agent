import test from 'node:test';
import assert from 'node:assert/strict';
import { useMenuTooltip } from './menuTooltip.js';

test('点击立即收起说明，菜单关闭后不因旧悬停事件重新显示', () => {
  const control = useMenuTooltip();
  control.onTooltipVisibleChange(true);
  assert.equal(control.tooltipVisible.value, true);
  control.dismissTooltip();
  assert.equal(control.tooltipVisible.value, false);
  control.onMenuVisibleChange(true);
  control.onTooltipVisibleChange(true);
  assert.equal(control.tooltipVisible.value, false);
  control.onMenuVisibleChange(false);
  control.onTooltipVisibleChange(true);
  assert.equal(control.tooltipVisible.value, false);
  control.onTriggerLeave();
  control.onTooltipVisibleChange(true);
  assert.equal(control.tooltipVisible.value, true);
});

test('键盘打开菜单同样收起说明，移动到菜单时不能触发延迟显示', () => {
  const control = useMenuTooltip();
  control.onTooltipVisibleChange(true);
  control.onMenuVisibleChange(true);
  assert.equal(control.tooltipVisible.value, false);
  control.onTriggerLeave();
  control.onTooltipVisibleChange(true);
  assert.equal(control.tooltipVisible.value, false);
});
