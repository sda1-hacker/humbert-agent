import { createFeatureRegistry } from "./registry.js";

// sidebar 与 AppShell 共享页面定义。settingsEvents 把模块自己的管理事件映射到设置页，
// 页面内部的 Agent/Session 状态仍由原来的领域 Store 管理。
export const workspaceFeatures = createFeatureRegistry([
  {
    key: "tasks", title: "任务", description: "主动运行与定时计划",
    iconPath: "M7 3v3M17 3v3M5 5h14a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2Zm-2 5h18M8 12h3M8 16h7",
    load: () => import("../components/tasks/TasksWorkspaceView.vue"),
  },
  {
    key: "skills", title: "技能", description: "为 Agent 配置 Skills",
    iconPath: "M14.7 6.3a4 4 0 0 0-5 5L4 17v3h3l5.7-5.7a4 4 0 0 0 5-5l-2.4 2.4-3-3 2.4-2.4Z",
    load: () => import("../components/skills/SkillWorkspaceView.vue"),
    settingsEvents: { "manage-packages": "skills" },
  },
  {
    key: "connectors", title: "连接器", description: "MCP 与外部工具",
    iconPath: "M8 12h8M12 8v8M7 4h10a3 3 0 0 1 3 3v10a3 3 0 0 1-3 3H7a3 3 0 0 1-3-3V7a3 3 0 0 1 3-3Z",
    load: () => import("../components/mcp/MCPWorkspaceView.vue"),
    settingsEvents: { "manage-servers": "connectors" },
  },
]);
