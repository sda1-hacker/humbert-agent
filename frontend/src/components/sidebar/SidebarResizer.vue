<script setup>
import {
  onUnmounted,
  ref,
} from "vue";

import {
  useLayoutStore,
} from "../../stores/layout.js";

const layoutStore =
    useLayoutStore();

const dragging =
    ref(false);

let startX = 0;

let startWidth = 0;

let previousCursor = "";

let previousUserSelect = "";

/**
 * 开始调整 Sidebar 宽度。
 *
 * pointer event 同时支持：
 *
 * - Mouse；
 * - Trackpad；
 * - Pen。
 *
 * 整个拖动过程只修改纯前端 Layout Store，
 * 不触碰 Agent/Session 等业务状态。
 */
function startDrag(event) {
  if (
      event.button !== undefined &&
      event.button !== 0
  ) {
    return;
  }

  event.preventDefault();

  dragging.value = true;

  startX = event.clientX;

  startWidth =
      layoutStore.sidebarWidth;

  previousCursor =
      document.body.style.cursor;

  previousUserSelect =
      document.body.style.userSelect;

  document.body.style.cursor =
      "col-resize";

  document.body.style.userSelect =
      "none";

  window.addEventListener(
      "pointermove",
      handlePointerMove,
  );

  window.addEventListener(
      "pointerup",
      stopDrag,
  );

  window.addEventListener(
      "pointercancel",
      stopDrag,
  );
}

/**
 * 拖动过程中根据水平位移实时调整宽度。
 */
function handlePointerMove(event) {
  if (!dragging.value) {
    return;
  }

  const delta =
      event.clientX - startX;

  layoutStore.setSidebarWidth(
      startWidth + delta,
  );
}

/**
 * 完成拖动并回收事件监听器。
 */
function stopDrag() {
  if (!dragging.value) {
    return;
  }

  dragging.value = false;

  document.body.style.cursor =
      previousCursor;

  document.body.style.userSelect =
      previousUserSelect;

  window.removeEventListener(
      "pointermove",
      handlePointerMove,
  );

  window.removeEventListener(
      "pointerup",
      stopDrag,
  );

  window.removeEventListener(
      "pointercancel",
      stopDrag,
  );
}

/**
 * 双击分隔线恢复默认宽度。
 */
function resetWidth() {
  layoutStore.resetSidebarWidth();
}

function handleKeydown(event) {
  if (event.key === "Home") {
    event.preventDefault();
    resetWidth();
    return;
  }
  if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
  event.preventDefault();
  layoutStore.setSidebarWidth(
      layoutStore.sidebarWidth + (event.key === "ArrowRight" ? 20 : -20),
  );
}

onUnmounted(() => {
  stopDrag();
});
</script>

<template>
  <div
      class="sidebar-resizer"
      :class="{
      'sidebar-resizer--dragging':
        dragging,
    }"
      role="separator"
      tabindex="0"
      aria-orientation="vertical"
      aria-label="调整侧边栏宽度"
      :aria-valuemin="220"
      :aria-valuemax="420"
      :aria-valuenow="layoutStore.sidebarWidth"
      @pointerdown="startDrag"
      @dblclick="resetWidth"
      @keydown="handleKeydown"
  ></div>
</template>

<style scoped>
.sidebar-resizer {
  position: relative;

  width: 5px;
  height: 100%;

  cursor: col-resize;

  background: transparent;

  touch-action: none;
}

.sidebar-resizer::before {
  content: "";

  position: absolute;

  top: 0;
  bottom: 0;
  left: 2px;

  width: 1px;

  background:
      var(--h-border);

  transition:
      background-color
      120ms ease;
}

.sidebar-resizer:hover::before,
.sidebar-resizer--dragging::before,
.sidebar-resizer:focus-visible::before {
  background:
      var(--h-accent);
}
.sidebar-resizer:focus-visible { outline: 2px solid var(--h-accent); outline-offset: -2px; }
</style>
