<script setup>
import {
  computed,
  defineAsyncComponent,
  onMounted,
  onUnmounted,
  ref,
  watch,
} from "vue";

import {
  Message,
} from "@arco-design/web-vue";

import WindowChrome
  from "../components/window/WindowChrome.vue";

import ConversationSidebar
  from "../components/sidebar/ConversationSidebar.vue";

import SidebarResizer
  from "../components/sidebar/SidebarResizer.vue";

import ChatView
  from "../components/chat/ChatView.vue";

// 设置、Skills 与 Connectors 都是低频一级页面。按需加载可避免它们的表单、详情组件
// 和领域 API 全部进入聊天首屏主包，同时不改变任何 Pinia/Runtime 生命周期。
const SettingsView =
    defineAsyncComponent(
        () => import(
            "../components/settings/SettingsView.vue"
            ),
    );

const SkillWorkspaceView =
    defineAsyncComponent(
        () => import(
            "../components/skills/SkillWorkspaceView.vue"
            ),
    );

const MCPWorkspaceView =
    defineAsyncComponent(
        () => import(
            "../components/mcp/MCPWorkspaceView.vue"
            ),
    );

const TasksWorkspaceView =
    defineAsyncComponent(
        () => import(
            "../components/tasks/TasksWorkspaceView.vue"
            ),
    );

// 工作区页包含文件树与安全文件预览，属于低频一级页面，因此保持按需加载，
// 不把目录浏览组件加入聊天首屏主包。
const WorkspaceView =
    defineAsyncComponent(
        () => import(
            "../components/workspace/WorkspaceView.vue"
            ),
    );

import {
  useAgentStore,
} from "../stores/agents.js";

import {
  useLayoutStore,
} from "../stores/layout.js";

import {
  useModelStore,
} from "../stores/models.js";

import {
  useRuntimeStore,
} from "../stores/runtime.js";

import {
  useSessionStore,
} from "../stores/sessions.js";

import {
  useTaskStore,
} from "../stores/tasks.js";

import {
  usePreferenceStore,
} from "../stores/preferences.js";

import {
  useProactiveStore,
} from "../stores/proactive.js";

import {
  useWorkspaceStore,
} from "../stores/workspace.js";

const layoutStore =
    useLayoutStore();

const modelStore =
    useModelStore();

const agentStore =
    useAgentStore();

const sessionStore =
    useSessionStore();

const runtimeStore =
    useRuntimeStore();

const taskStore =
    useTaskStore();

const preferenceStore =
    usePreferenceStore();

const proactiveStore =
    useProactiveStore();

const workspaceStore =
    useWorkspaceStore();

/**
 * 设置页是否处于打开状态。
 *
 * 设置现在是一个真正的一级 Application View，
 * 而不是覆盖在聊天页面上的 Drawer。
 *
 * 这样设计有几个好处：
 *
 * 1. 后续增加 Skills、MCP、Browser、Security 等复杂设置时，
 *    不会受 Drawer 宽度限制；
 * 2. 设置页拥有独立的信息架构，左侧导航可以稳定扩展；
 * 3. 打开设置不会改变 Agent / Session / Runtime Store，
 *    返回聊天后仍然保持用户之前的工作上下文；
 * 4. RuntimeStore 仍然由 AppShell 持有，因此即便用户在 Agent
 *    正在运行时进入设置页，后台 Runtime Event 也不会丢失。
 */
const settingsVisible =
    ref(false);

const settingsInitialKey =
    ref("models");

/**
 * 主工作区视图。
 *
 * chat       -> 对话
 * skills     -> Skills 中心
 * connectors -> MCP 连接器
 * tasks      -> 主动任务
 * workspace  -> Agent 工作区文件浏览
 *
 * Settings 仍然是覆盖整个业务区域的一级页面；关闭 Settings 后回到
 * 用户打开设置前所在的主工作区。
 */
const mainView =
    ref("chat");

const skillsInitialSkillName =
    ref("");

const skillsViewRevision =
    ref(0);

/**
 * 主聊天业务区域使用三个 Grid Column：
 *
 * Sidebar
 * Resize Handle
 * Chat
 *
 * SettingsView 不使用这套 Grid。
 * 设置打开后会替换整个业务区域，获得完整可用宽度。
 */
const mainGridStyle =
    computed(() => ({
      gridTemplateColumns:
          `${layoutStore.sidebarWidth}px 5px minmax(0, 1fr)`,
    }));

/**
 * 当前 Agent 切换时自动加载该 Agent 的 Session。
 *
 * 该监听属于 Application 生命周期，因此即使当前显示 SettingsView，
 * Store 的数据一致性仍然能够保持。
 */
watch(
    () => agentStore.selectedID,

    async (
        current,
        previous,
    ) => {
      if (
          current === previous
      ) {
        return;
      }

      try {
        await sessionStore
            .loadForAgent(current);
      } catch (error) {
        Message.error(
            error?.message ??
            String(error),
        );
      }
    },
);

watch(
    () => proactiveStore.notificationSequence,
    () => {
      const notification = proactiveStore.notification;
      if (!notification) return;

      const message = notification.body
          ? `${notification.title}：${notification.body}`
          : notification.title;

      switch (notification.level) {
        case "success":
          Message.success(message);
          break;
        case "warning":
          Message.warning(message);
          break;
        case "error":
          Message.error(message);
          break;
        default:
          Message.info(message);
          break;
      }

      if (
          typeof document !== "undefined" &&
          document.hidden &&
          "Notification" in globalThis &&
          globalThis.Notification.permission === "granted"
      ) {
        try {
          new globalThis.Notification(notification.title, {
            body: notification.body || "",
          });
        } catch (error) {
          console.warn("[Proactive] 系统通知发送失败，已使用应用内通知", error);
        }
      }
    },
);

/**
 * 打开独立设置页面。
 *
 * 这里只改变前端 View State，不修改任何业务状态。
 */
function openSettings(initialKey = "models") {
  settingsInitialKey.value =
      typeof initialKey === "string"
          ? initialKey
          : "models";
  settingsVisible.value = true;
}

function openConnectorSettings() {
  openSettings("connectors");
}

/**
 * 打开 Skills 中心。
 *
 * skillName 非空时直接进入独立 Skill 详情页；用于从设置页中的
 * Skill 卡片跳转到完整工作区。
 */
function openSkills(skillName = "") {
  settingsVisible.value = false;
  mainView.value = "skills";
  skillsInitialSkillName.value =
      typeof skillName === "string"
          ? skillName
          : "";
  skillsViewRevision.value += 1;
}

function openConnectors() {
  settingsVisible.value = false;
  mainView.value = "connectors";
}

function openTasks() {
  settingsVisible.value = false;
  mainView.value = "tasks";
}

/**
 * 从左侧导航打开工作区。
 *
 * WorkspaceStore 会保留用户上一次主动选择的 Agent；只有第一次进入时才默认跟随
 * 当前聊天 Agent，因此浏览其它工作区不会偷偷切换聊天上下文。
 */
function openWorkspace() {
  settingsVisible.value = false;
  mainView.value = "workspace";
}

/**
 * 从聊天里的“本轮文件”直接跳到工作区预览。
 *
 * 这里由 AppShell 负责跨一级页面导航：先加载文件所属 Agent 的 Workspace，再打开相对路径，
 * 最后切换主视图。这样 Chat 组件不需要知道 WorkspaceView 的实现细节。
 */
async function openWorkspaceFile(payload) {
  const agentID = payload?.agentID ?? "";
  const path = payload?.path ?? "";
  if (!agentID || !path) {
    return;
  }

  try {
    await workspaceStore.load(agentID);
    await workspaceStore.openPath(path);
    settingsVisible.value = false;
    mainView.value = "workspace";
  } catch (error) {
    Message.error(
        error?.message ?? String(error),
    );
  }
}

function openChat() {
  settingsVisible.value = false;
  mainView.value = "chat";
}

/**
 * 关闭设置页面并回到打开设置前的主工作区。
 *
 * mainView 不在这里重置，因此从 Skills 中心打开设置后关闭，
 * 仍会回到 Skills；从聊天打开则回到聊天。
 */
function closeSettings() {
  settingsVisible.value = false;
}

async function openTaskSession(payload) {
  const agentID = payload?.agentID ?? "";
  const sessionID = payload?.sessionID ?? "";
  if (!agentID || !sessionID) {
    return;
  }
  try {
    agentStore.select(agentID);
    await sessionStore.loadForAgent(agentID);
    await sessionStore.select(sessionID);
    openChat();
  } catch (error) {
    Message.error(error?.message ?? String(error));
  }
}

onMounted(async () => {
  runtimeStore.initialiseEvents();
  taskStore.initialiseEvents();
  proactiveStore.initialiseEvents();
  workspaceStore.initialiseEvents();

  try {
    await Promise.all([
      modelStore.load(),

      agentStore.load(),

      preferenceStore.load().catch((error) => {
        console.warn("[Preferences] 用户资料加载失败，继续使用默认身份", error);
      }),

      taskStore.load(),

      proactiveStore.load().catch((error) => {
        console.warn("[Proactive] 主动助手状态加载失败", error);
      }),
    ]);

    await sessionStore
        .loadForAgent(
            agentStore.selectedID,
        );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  }
});

onUnmounted(() => {
  runtimeStore.disposeEvents();
  taskStore.disposeEvents();
  proactiveStore.disposeEvents();
  workspaceStore.disposeEvents();
});
</script>

<template>
  <div class="app-shell">
    <WindowChrome />

    <!--
      Settings 是一级页面，不再使用 Drawer。

      v-if / v-else 保证聊天页面和设置页面不会同时占据布局空间，
      同时 Pinia / Runtime 的 Application State 不受影响。
    -->
    <SettingsView
        v-if="settingsVisible"
        class="app-shell__settings"
        :initial-key="settingsInitialKey"
        @close="closeSettings"
        @open-skills="openSkills"
    />

    <div
        v-else
        class="app-shell__main"
        :style="mainGridStyle"
    >
      <ConversationSidebar
          :active-view="mainView"
          @open-chat="openChat"
          @open-skills="openSkills"
          @open-connectors="openConnectors"
          @open-tasks="openTasks"
          @open-workspace="openWorkspace"
          @open-settings="openSettings"
      />

      <SidebarResizer />

      <MCPWorkspaceView
          v-if="mainView === 'connectors'"
          @manage-servers="openConnectorSettings"
      />

      <TasksWorkspaceView
          v-else-if="mainView === 'tasks'"
          @open-session="openTaskSession"
      />

      <SkillWorkspaceView
          v-else-if="mainView === 'skills'"
          :key="skillsViewRevision"
          :initial-skill-name="skillsInitialSkillName"
          @manage-packages="openSettings('skills')"
      />

      <WorkspaceView
          v-else-if="mainView === 'workspace'"
      />

      <ChatView
          v-else
          @open-workspace-file="openWorkspaceFile"
      />
    </div>
  </div>
</template>

<style scoped>
.app-shell {
  display: grid;

  width: 100%;
  height: 100%;

  min-width: 0;
  min-height: 0;

  grid-template-rows:
    38px minmax(0, 1fr);

  overflow: hidden;

  background:
      var(--h-bg);
}

.app-shell__main,
.app-shell__settings {
  width: 100%;
  height: 100%;

  min-width: 0;
  min-height: 0;

  overflow: hidden;
}

.app-shell__main {
  display: grid;
}
</style>
