<template>
  <!--
    macOS 原生 Traffic Lights 会绘制在这一区域上方。

    WindowChrome 只承担两个职责：

    1. 给 Traffic Lights 留出空间；
    2. 作为 Wails Window Drag Region。

    特别注意：
    这里不画 Sidebar 分隔线。
    Sidebar 从 TitleBar 下方才开始。
  -->
  <div class="window-chrome">
    <div class="window-chrome__identity" aria-hidden="true">
      <span class="window-chrome__name">Humbert</span>
      <span class="window-chrome__mode">Local agent workspace</span>
    </div>
  </div>
</template>

<style scoped>
.window-chrome {
  --wails-draggable: drag;

  display: flex;

  width: 100%;
  height: 38px;

  flex: 0 0 38px;

  align-items: center;

  padding: 0 16px 0 78px;

  /*
   * 整个 macOS TitleBar 统一使用 Humbert 页面背景色。
   */
  background:
      var(--h-bg);

  /*
   * 这里只保留下边界，
   * 不出现任何竖向分割。
   */
  border-bottom:
      1px solid
      var(--h-border);
}

.window-chrome__identity {
  display: flex;

  min-width: 0;

  align-items: baseline;

  gap: 9px;

  color: var(--h-text);
}

.window-chrome__name {
  font-size: 12px;

  font-weight: 500;
}

.window-chrome__mode {
  color: var(--h-text-muted);

  font-family: var(--h-ui);

  font-size: 9px;

  letter-spacing: 0.08em;

  text-transform: uppercase;
}

@media (max-width: 560px) {
  .window-chrome__mode {
    display: none;
  }
}
</style>
