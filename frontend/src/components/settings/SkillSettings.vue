<script setup>
import {
  computed,
  onErrorCaptured,
  ref,
  watch,
} from "vue";

import {
  IconSearch,
} from "@arco-design/web-vue/es/icon";

import ModelCatalog from "./models/ModelCatalog.vue";
import MultimediaSettings from "./models/MultimediaSettings.vue";
import ProviderSettings from "./models/ProviderSettings.vue";
import PermissionSettings from "./PermissionSettings.vue";
import ProactiveSettings from "./ProactiveSettings.vue";
import SandboxSettings from "./SandboxSettings.vue";
import SkillPackageSettings from "./SkillPackageSettings.vue";
import MCPSettings from "./MCPSettings.vue";
import AppPageHeader from "../ui/AppPageHeader.vue";
import UserProfileSettings from "./UserProfileSettings.vue";

const props = defineProps({
  initialKey: {
    type: String,
    default: "models",
  },
});

const emit = defineEmits([
  "close",
  "open-skills",
]);

const navigationGroups = [
  {
    key: "personal",
    title: "个人",
    items: [
      {
        key: "profile",
        title: "个人资料",
        description: "设置你在聊天中显示的名称和头像。",
        keywords: ["个人", "用户", "名称", "头像", "profile", "avatar"],
        glyph: "我",
      },
    ],
  },
  {
    key: "ai",
    title: "AI 与模型",
    items: [
      {
        key: "models",
        title: "模型",
        description: "管理可供 Agent 使用的模型、启用状态与请求参数。",
        keywords: ["模型", "model", "llm", "ai"],
        glyph: "M",
      },
      {
        key: "providers",
        title: "供应商",
        description: "配置 OpenAI、兼容服务与 Ollama 等模型供应商。",
        keywords: ["供应商", "provider", "openai", "ollama", "api"],
        glyph: "P",
      },
      {
        key: "multimedia",
        title: "多媒体",
        description: "配置图片理解模型，并查看当前真正支持的附件类型。",
        keywords: ["多媒体", "图片", "vision", "image", "附件", "pdf", "audio"],
        glyph: "图",
      },
    ],
  },
  {
    key: "capabilities",
    title: "Agent 能力",
    items: [
      {
        key: "skills",
        title: "技能",
        description: "管理本地 Skill Package 的安装与维护；Agent 的启用与停用在左侧「技能」工作区配置。",
        keywords: ["skill", "skills", "技能", "工作流", "prompt"],
        glyph: "K",
      },
      {
        key: "connectors",
        title: "连接器",
        description: "管理 stdio / Streamable HTTP MCP Server 与安全凭据。具体 Tool 的启用与停用在左侧「连接器」工作区按 Agent 配置。",
        keywords: ["mcp", "connector", "连接器", "tool", "stdio", "http", "credential", "token"],
        glyph: "C",
      },
    ],
  },
  {
    key: "assistant",
    title: "助手",
    items: [
      {
        key: "proactive",
        title: "主动助手",
        description: "配置心跳巡检、后台通知、免打扰和事件触发的 Agent 主动处理。",
        keywords: ["主动", "助手", "心跳", "通知", "heartbeat", "proactive", "automation"],
        glyph: "主",
      },
    ],
  },
  {
    key: "security",
    title: "安全",
    items: [
      {
        key: "sandbox",
        title: "安全",
        description: "控制 Humbert 的文件保护与联网范围。普通情况下保持安全沙盒开启即可。",
        keywords: ["安全", "沙盒", "隔离", "sandbox", "seatbelt", "bubblewrap", "windows"],
        glyph: "安",
      },
      {
        key: "permissions",
        title: "操作确认",
        description: "设置高风险操作是否需要确认，并管理已经记住的临时或长期授权。",
        keywords: ["确认", "授权", "权限", "审批", "permission", "approval"],
        glyph: "确",
      },
    ],
  },
];

const allItems = navigationGroups.flatMap((group) => group.items);

function validKey(value) {
  return allItems.some((item) => item.key === value)
      ? value
      : "models";
}

const activeKey = ref(validKey(props.initialKey));
const query = ref("");
const childRenderError = ref("");

watch(
    () => props.initialKey,
    (value) => {
      activeKey.value = validKey(value);
    },
);

watch(activeKey, () => {
  childRenderError.value = "";
});

onErrorCaptured((error, _instance, info) => {
  if (activeKey.value !== "skills" && activeKey.value !== "connectors") {
    return true;
  }

  childRenderError.value = `${error?.message ?? String(error)}${info ? `（${info}）` : ""}`;
  console.error(`[Settings/${activeKey.value}] 渲染失败`, error, info);
  return false;
});

const filteredGroups = computed(() => {
  const keyword = query.value.trim().toLowerCase();
  if (!keyword) return navigationGroups;

  return navigationGroups
      .map((group) => ({
        ...group,
        items: group.items.filter((item) => [
          item.title,
          item.description,
          ...item.keywords,
        ].join(" ").toLowerCase().includes(keyword)),
      }))
      .filter((group) => group.items.length > 0);
});

const activeItem = computed(() => (
    allItems.find((item) => item.key === activeKey.value) ?? allItems[0]
));

function selectItem(key) {
  activeKey.value = validKey(key);
}
</script>

<template>
  <section class="settings-view">
    <aside class="settings-sidebar">
      <div class="settings-sidebar__top">
        <button
            type="button"
            class="settings-back"
            aria-label="返回"
            title="返回"
            @click="emit('close')"
        >
          <svg class="settings-back__icon" viewBox="0 0 24 24" aria-hidden="true">
            <path d="M15 5L8 12L15 19"/>
          </svg>
        </button>

        <div class="settings-sidebar__title">设置</div>
      </div>

      <div class="settings-search">
        <a-input v-model="query" allow-clear placeholder="搜索设置">
          <template #prefix>
            <IconSearch/>
          </template>
        </a-input>
      </div>

      <nav class="settings-nav" aria-label="设置导航">
        <template v-for="group in filteredGroups" :key="group.key">
          <div class="settings-nav-label">{{ group.title }}</div>

          <button
              v-for="item in group.items"
              :key="item.key"
              type="button"
              class="settings-nav-item"
              :class="{ 'settings-nav-item--active': activeKey === item.key }"
              @click="selectItem(item.key)"
          >
            <span class="settings-nav-item__glyph" aria-hidden="true">
              {{ item.glyph }}
            </span>
            <span class="settings-nav-item__text">{{ item.title }}</span>
          </button>
        </template>

        <div v-if="filteredGroups.length === 0" class="settings-nav-empty">
          没有匹配的设置
        </div>
      </nav>

      <div class="settings-sidebar__hint">
        全局连接信息放在设置中；Agent 实际启用哪些 Skill / MCP Tool，则在左侧对应工作区管理。
      </div>
    </aside>

    <main class="settings-content">
      <div class="settings-content__scroll">
        <div class="settings-content__inner">
          <AppPageHeader
              eyebrow="HUMBERT"
              :title="activeItem.title"
              :description="activeItem.description"
          />

          <section class="settings-content__body">
            <UserProfileSettings v-if="activeKey === 'profile'"/>
            <ModelCatalog v-else-if="activeKey === 'models'"/>
            <ProviderSettings v-else-if="activeKey === 'providers'"/>
            <MultimediaSettings v-else-if="activeKey === 'multimedia'"/>
            <PermissionSettings v-else-if="activeKey === 'permissions'"/>
            <ProactiveSettings v-else-if="activeKey === 'proactive'"/>
            <SandboxSettings v-else-if="activeKey === 'sandbox'"/>

            <div
                v-else-if="activeKey === 'skills' || activeKey === 'connectors'"
                class="settings-child-host"
            >
              <div v-if="childRenderError" class="settings-child-error">
                <strong>{{ activeKey === 'skills' ? '技能页面渲染失败' : '连接器页面渲染失败' }}</strong>
                <span>{{ childRenderError }}</span>
                <button type="button" @click="childRenderError = ''">重新显示</button>
              </div>

              <SkillPackageSettings
                  v-else-if="activeKey === 'skills'"
                  @open-detail="emit('open-skills', $event)"
              />

              <MCPSettings v-else/>
            </div>
          </section>
        </div>
      </div>
    </main>
  </section>
</template>
<style scoped>
.settings-view {
  display: grid;

  grid-template-columns:
    252px minmax(0, 1fr);

  background: var(--h-bg);
}

/* =============================
   Sidebar
   ============================= */

.settings-sidebar {
  display: flex;

  min-width: 0;
  min-height: 0;

  flex-direction: column;

  overflow: hidden;

  border-right: 1px solid var(--h-border);

  background: var(--h-sidebar);
}

.settings-sidebar__top {
  display: flex;

  height: 72px;

  flex: 0 0 72px;

  align-items: center;

  gap: 12px;

  padding: 0 18px;
}

.settings-back {
  display: grid;

  width: 32px;
  height: 32px;

  flex: 0 0 32px;

  place-items: center;

  padding: 0;

  border: 0;

  border-radius: 8px;

  background: transparent;

  color: var(--h-text-muted);

  cursor: pointer;

  font-size: 18px;

  transition: background-color 120ms ease,
  color 120ms ease;
}

.settings-back:hover {
  background: var(--h-surface-hover);

  color: var(--h-accent-hover);
}

.settings-back__icon {
  width: 18px;
  height: 18px;

  fill: none;

  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.8;
}

.settings-sidebar__title {
  color: var(--h-text);

  font-size: 20px;

  font-weight: 500;

  letter-spacing: 0.01em;
}

.settings-search {
  padding: 0 14px 18px;
}

.settings-search :deep(.arco-input-wrapper) {
  height: 38px;

  padding: 0 12px;

  border-color: transparent !important;

  border-radius: 6px;

  background: var(--h-surface) !important;
}

.settings-search :deep(.arco-input-wrapper:hover),
.settings-search :deep(.arco-input-wrapper.arco-input-focus) {
  border-color: var(--h-border-strong) !important;

  background: var(--h-surface) !important;
}

.settings-search :deep(.arco-input-prefix) {
  color: var(--h-text-muted);
}

.settings-nav-label {
  padding: 0 20px 8px;

  color: var(--h-text-muted);

  font-size: 10px;

  font-weight: 500;

  letter-spacing: 0.08em;

  text-transform: uppercase;
}

.settings-nav {
  display: flex;

  flex-direction: column;

  gap: 4px;

  padding: 0 10px;
}

.settings-nav .settings-nav-label {
  padding: 14px 10px 5px;
}

.settings-nav .settings-nav-label:first-child {
  padding-top: 0;
}

.settings-nav-item {
  display: flex;

  width: 100%;
  height: 42px;

  align-items: center;

  gap: 11px;

  padding: 0 12px 0 14px;

  border: 0;
  border-left: 2px solid transparent;

  border-radius: 0 6px 6px 0;

  background: transparent;

  color: var(--h-text-secondary);

  cursor: pointer;

  text-align: left;

  transition: background-color 120ms ease,
  color 120ms ease;
}

.settings-nav-item:hover {
  background: var(--h-surface-hover);

  color: var(--h-text);
}

.settings-nav-item--active {
  border-left-color: var(--h-accent);

  background: transparent;

  color: var(--h-text);
}

.settings-nav-item__glyph {
  display: grid;

  width: 22px;
  height: 22px;

  flex: 0 0 22px;

  place-items: center;

  border: 0;

  color: var(--h-accent);

  font-family: var(--h-mono);

  font-size: 9px;

  font-weight: 500;
}

.settings-nav-item--active
.settings-nav-item__glyph {
  color: var(--h-accent);
}

.settings-nav-item__text {
  overflow: hidden;

  font-size: 13px;

  font-weight: 400;

  text-overflow: ellipsis;

  white-space: nowrap;
}

.settings-nav-empty {
  padding: 18px 12px;

  color: var(--h-text-muted);

  font-size: 11px;

  text-align: center;
}

.settings-sidebar__hint {
  margin-top: auto;

  padding: 16px 20px 20px;

  color: var(--h-text-muted);

  font-size: 9px;

  line-height: 1.65;
}

/* =============================
   Content
   ============================= */

.settings-content {
  min-width: 0;
  min-height: 0;

  overflow: hidden;

  background: var(--h-bg);
}

.settings-content__scroll {
  width: 100%;
  height: 100%;

  overflow-x: hidden;
  overflow-y: auto;
}

.settings-content__inner {
  width: min(var(--h-content-wide), 100%);
  margin: 0 auto;
  padding: var(--h-page-top) 48px var(--h-page-bottom);
}

.settings-content__body {
  min-width: 0;
}


.settings-child-host {
  min-width: 0;
}

.settings-child-error {
  display: flex;
  flex-direction: column;
  gap: 8px;
  border: 1px solid var(--h-danger-border);
  border-radius: 10px;
  background: var(--h-danger-soft);
  padding: 14px;
  color: var(--h-text-secondary);
  font-size: 12px;
}

.settings-child-error strong {
  color: var(--h-danger);
  font-size: 14px;
}

.settings-child-error button {
  align-self: flex-start;
  border: 1px solid var(--h-border);
  border-radius: 6px;
  background: var(--h-surface);
  padding: 5px 10px;
  color: var(--h-text);
  cursor: pointer;
}

@media (
max-width: 900px
) {
  .settings-view {
    grid-template-columns:
      224px minmax(0, 1fr);
  }

  .settings-content__inner {
    padding: var(--h-page-top) var(--h-page-gutter) var(--h-page-bottom);
  }
}
</style>
