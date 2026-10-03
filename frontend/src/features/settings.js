import { createFeatureRegistry } from "./registry.js";

// 标题与搜索词沿用现有设置；组件按需加载，不把模块 API/Store 注入页面宿主。
const groups = [
  {
    key: "general",
    title: "通用",
    items: [{
        key: "language",
        load: () => import("../components/settings/LanguageSettings.vue"),
        title: "语言",
        description: "选择界面与 Agent 默认回答使用的语言。",
        keywords: ["语言", "language", "locale", "日本語", "한국어"],
        glyph: "L",
    }],
  },
  {
    key: "personal",
    title: "个人",
    items: [
      {
        key: "profile",
        load: () => import("../components/settings/UserProfileSettings.vue"),
        title: "个人资料",
        description: "设置你在聊天中显示的名称和头像。",
        keywords: ["个人", "用户", "名称", "头像", "profile", "avatar"],
        glyph: "U",
      },
      {
        key: "data",
        load: () => import("../components/settings/DataSettings.vue"),
        title: "数据与备份",
        description: "导出并校验个人数据备份。",
        keywords: ["数据", "备份", "恢复", "backup", "restore"],
        glyph: "D",
      },
      {
        key: "archived",
        forwardEvents: { "open-session": "open-session" },
        load: () => import("../components/settings/ArchivedSessionsSettings.vue"),
        title: "归档会话",
        description: "查看、解除归档或删除已归档的对话。",
        keywords: ["归档", "会话", "对话", "恢复", "删除", "archive", "session"],
        glyph: "H",
      },
    ],
  },
  {
    key: "ai",
    title: "AI 与模型",
    items: [
      {
        key: "models",
        load: () => import("../components/settings/models/ModelCatalog.vue"),
        title: "模型",
        description: "管理可供 Agent 使用的模型、启用状态与请求参数。",
        keywords: ["模型", "model", "llm", "ai"],
        glyph: "M",
      },
      {
        key: "providers",
        load: () => import("../components/settings/models/ProviderSettings.vue"),
        title: "供应商",
        description: "配置 OpenAI、兼容服务与 Ollama 等模型供应商。",
        keywords: ["供应商", "provider", "openai", "ollama", "api"],
        glyph: "P",
      },
      {
        key: "multimedia",
        load: () => import("../components/settings/models/MultimediaSettings.vue"),
        title: "多媒体",
        description: "配置视觉辅助模型，并查看当前真正支持的附件类型。",
        keywords: ["多媒体", "图片", "vision", "image", "附件", "pdf", "audio"],
        glyph: "V",
      },
    ],
  },
  {
    key: "capabilities",
    title: "Agent 能力",
    items: [
      {
        key: "skills",
        forwardEvents: { "open-detail": "open-skills" },
        recoverableError: "技能页面渲染失败",
        load: () => import("../components/settings/SkillPackageSettings.vue"),
        title: "技能",
        description: "管理本地 Skill Package 的安装与维护；Agent 的启用与停用在左侧「技能」工作区配置。",
        keywords: ["skill", "skills", "技能", "工作流", "prompt"],
        glyph: "S",
      },
      {
        key: "connectors",
        recoverableError: "连接器页面渲染失败",
        load: () => import("../components/settings/MCPSettings.vue"),
        title: "连接器",
        description: "管理 stdio / Streamable HTTP MCP Server 与安全凭据。具体 Tool 的启用与停用在左侧「连接器」工作区按 Agent 配置。",
        keywords: ["mcp", "connector", "连接器", "tool", "stdio", "http", "credential", "token"],
        glyph: "C",
      },
    ],
  },
  {
    key: "security",
    title: "安全",
    items: [
      {
        key: "sandbox",
        load: () => import("../components/settings/SandboxSettings.vue"),
        title: "安全",
        description: "控制 Humbert 的文件保护与联网范围。普通情况下保持安全沙盒开启即可。",
        keywords: ["安全", "沙盒", "隔离", "sandbox", "seatbelt", "bubblewrap", "windows"],
        glyph: "G",
      },
      {
        key: "permissions",
        load: () => import("../components/settings/PermissionSettings.vue"),
        title: "操作确认",
        description: "设置高风险操作是否需要确认，并管理已经记住的临时或长期授权。",
        keywords: ["确认", "授权", "权限", "审批", "permission", "approval"],
        glyph: "A",
      },
    ],
  },
];

export const settingsFeatures = createFeatureRegistry(groups.flatMap((group) => group.items));
export const settingsGroups = groups.map((group) => ({ ...group, items: group.items.map((item) => settingsFeatures.get(item.key)) }));
