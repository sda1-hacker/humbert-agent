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
  <div class="window-chrome"></div>
</template>

<style scoped>
.window-chrome {
  --wails-draggable: drag;

  width: 100%;
  height: 38px;

  flex: 0 0 38px;

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
</style>