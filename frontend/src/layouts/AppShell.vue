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

const FirstRunGuide = defineAsyncComponent(
    () => import("../components/onboarding/FirstRunGuide.vue"),
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

const ContextPanel = defineAsyncComponent(
    () => import("../components/workspace/ContextPanel.vue"),
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
import { useContextPanelStore } from "../stores/contextPanel.js";

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

const bootstrapReady = ref(false);
const bootstrapError = ref("");
const needsFirstRun = computed(() =>
  bootstrapReady.value && !agentStore.items.some((agent) =>
    modelStore.enabledModels.some((model) => model.id === agent.modelID),
  ),
);

const dismissedTaskNotificationID = ref("");
const taskNotification = computed(() => {
  const value = proactiveStore.notification;
  return value?.taskID && value.id !== dismissedTaskNotificationID.value ? value : null;
});

const workspaceStore =
    useWorkspaceStore();
const contextPanel = useContextPanelStore();
const viewportWidth = ref(typeof window === "undefined" ? 1320 : window.innerWidth);

function updateViewportWidth() {
  viewportWidth.value = window.innerWidth;
}

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
 * 主聊天业务区域有左侧会话导航和右侧工作区。
 *
 * Sidebar
 * Resize Handle
 * Chat
 * Context resize handle + Context panel (chat 时可见)
 *
 * 两侧均占据 Grid 列，不覆盖聊天。窄窗口打开右侧时收起左侧。
 * SettingsView 不使用这套 Grid。
 * 设置打开后会替换整个业务区域，获得完整可用宽度。
 */
const mainGridStyle =
    computed(() => ({
      gridTemplateColumns: [
        ...(layoutStore.sidebarOpen ? [`${layoutStore.sidebarWidth}px`, "5px"] : []),
        "minmax(0, 1fr)",
        ...(mainView.value === "chat" && contextPanel.open
          ? ["5px", `${contextPanel.width}px`] : []),
      ].join(" "),
    }));

const contextVisible = computed(() => mainView.value === "chat" && contextPanel.open);
let resizingContext = false;

function beginContextResize(event) {
  if (event.button !== 0) return;
  resizingContext = true;
  event.currentTarget.setPointerCapture(event.pointerId);
}

function moveContextResize(event) {
  if (resizingContext) setContextWidth(window.innerWidth - event.clientX);
}

function endContextResize() {
  resizingContext = false;
}

function keyContextResize(event) {
  if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
  event.preventDefault();
  setContextWidth(contextPanel.width + (event.key === "ArrowLeft" ? 20 : -20));
}

function ensureRoomForPanel() {
  if (layoutStore.sidebarOpen && viewportWidth.value - layoutStore.sidebarWidth - contextPanel.width - 10 < 420) {
    layoutStore.setSidebarOpen(false);
  }
  contextPanel.setWidth(Math.min(contextPanel.width, Math.max(380, viewportWidth.value - 425)));
}

function setContextWidth(value) {
  if (layoutStore.sidebarOpen && viewportWidth.value - layoutStore.sidebarWidth - value - 10 < 420) {
    layoutStore.setSidebarOpen(false);
  }
  const leftWidth = layoutStore.sidebarOpen ? layoutStore.sidebarWidth + 5 : 0;
  contextPanel.setWidth(Math.min(value, Math.max(380, viewportWidth.value - leftWidth - 425)));
}

function toggleRightPanel() {
  if (!contextPanel.open) ensureRoomForPanel();
  contextPanel.setOpen(!contextPanel.open);
}

function toggleLeftPanel() {
  if (!layoutStore.sidebarOpen && contextVisible.value && viewportWidth.value - layoutStore.sidebarWidth - contextPanel.width - 10 < 420) {
    contextPanel.setOpen(false);
  }
  layoutStore.toggleSidebar();
}

watch([viewportWidth, contextVisible], () => {
  if (contextVisible.value) ensureRoomForPanel();
}, { immediate: true });

function showWorkspacePanel() {
  settingsVisible.value = false;
  mainView.value = "chat";
  ensureRoomForPanel();
  contextPanel.setOpen(true);
}

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
          const desktopNotification = new globalThis.Notification(notification.title, {
            body: notification.body || "",
          });
          if (notification.taskID) desktopNotification.onclick = () => { void openTask(notification.taskID); };
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

async function openTask(taskID) {
  if (!taskID) return;
  try {
    await taskStore.refresh();
    if (!taskStore.items.some((task) => task.id === taskID)) {
      Message.warning("任务已不存在或已归档");
      return;
    }
    await taskStore.select(taskID);
    openTasks();
  } catch (error) { Message.error(error?.message ?? String(error)); }
}

/**
 * 聊天文件入口始终打开右侧工作区。历史消息所属 Agent 不同时先切换 Agent。
 */
async function openWorkspaceFile(payload) {
  const agentID = payload?.agentID ?? "";
  const path = payload?.path ?? "";
  if (!agentID || !path) {
    return;
  }

  try {
    await workspaceStore.load(agentID);
    if (agentID !== agentStore.selectedID) {
      agentStore.select(agentID);
      await sessionStore.loadForAgent(agentID);
    }
    showWorkspacePanel();
    await workspaceStore.openPath(path);
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

async function bootstrap() {
  bootstrapError.value = "";
  try {
    await Promise.all([modelStore.load(), agentStore.load()]);
    bootstrapReady.value = true;
    await Promise.all([
      preferenceStore.load().catch((error) => {
        console.warn("[Preferences] 用户资料加载失败，继续使用默认身份", error);
      }),
      taskStore.load(),
      proactiveStore.load().catch((error) => {
        console.warn("[Proactive] 主动助手状态加载失败", error);
      }),
    ]);
    await sessionStore.loadForAgent(agentStore.selectedID);
  } catch (error) {
    if (!bootstrapReady.value) bootstrapError.value = error?.message ?? String(error);
    Message.error(error?.message ?? String(error));
  }
}

onMounted(() => {
  updateViewportWidth();
  runtimeStore.initialiseEvents();
  taskStore.initialiseEvents();
  proactiveStore.initialiseEvents();
  workspaceStore.initialiseEvents();
  window.addEventListener("resize", updateViewportWidth);
  void bootstrap();
});

onUnmounted(() => {
  runtimeStore.disposeEvents();
  taskStore.disposeEvents();
  proactiveStore.disposeEvents();
  workspaceStore.disposeEvents();
  window.removeEventListener("resize", updateViewportWidth);
});
</script>

<template>
  <div class="app-shell">
    <WindowChrome
        :left-open="layoutStore.sidebarOpen"
        :right-open="contextVisible"
        :left-disabled="settingsVisible || !bootstrapReady || needsFirstRun"
        :right-disabled="settingsVisible || mainView !== 'chat' || !bootstrapReady || needsFirstRun"
        @toggle-left="toggleLeftPanel"
        @toggle-right="toggleRightPanel"
    />
    <div v-if="taskNotification" class="task-notification" role="status">
      <div><strong>{{ taskNotification.title }}</strong><p>{{ taskNotification.body }}</p></div>
      <a-button size="small" @click="openTask(taskNotification.taskID)">查看任务</a-button>
      <a-button size="small" type="text" @click="dismissedTaskNotificationID = taskNotification.id">关闭</a-button>
    </div>

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

    <div v-else-if="bootstrapError" class="app-shell__bootstrap">
      <p>读取本地配置失败：{{ bootstrapError }}</p>
      <a-button @click="bootstrap">重试</a-button>
    </div>

    <div v-else-if="!bootstrapReady" class="app-shell__bootstrap">正在加载本地配置…</div>

    <FirstRunGuide
        v-else-if="needsFirstRun"
        @open-settings="openSettings"
        @complete="openChat"
    />

    <div
        v-else
        class="app-shell__main"
        :style="mainGridStyle"
    >
      <ConversationSidebar
          v-if="layoutStore.sidebarOpen"
          :active-view="mainView"
          @open-chat="openChat"
          @open-skills="openSkills"
          @open-connectors="openConnectors"
          @open-tasks="openTasks"
          @open-settings="openSettings"
      />

      <SidebarResizer v-if="layoutStore.sidebarOpen" />

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

      <ChatView
          v-else
          @open-workspace-file="openWorkspaceFile"
          @open-task="openTask"
      />
      <template v-if="contextVisible">
        <div
            class="app-shell__context-resizer"
            role="separator"
            tabindex="0"
            aria-label="调整右侧工作区宽度"
            aria-orientation="vertical"
            :aria-valuemin="380"
            :aria-valuemax="800"
            :aria-valuenow="contextPanel.width"
            @pointerdown="beginContextResize"
            @pointermove="moveContextResize"
            @pointerup="endContextResize"
            @pointercancel="endContextResize"
            @keydown="keyContextResize"
        ></div>
        <ContextPanel
        />
      </template>
    </div>
  </div>
</template>

<style scoped>
.task-notification { position: fixed; right: 20px; bottom: 20px; z-index: 1000; display: flex; align-items: center; gap: 10px; max-width: min(520px, calc(100vw - 40px)); padding: 12px; border: 1px solid var(--h-border); border-radius: 10px; background: var(--h-surface); box-shadow: 0 10px 30px rgba(0,0,0,.14); }
.task-notification div { min-width: 0; }
.task-notification p { margin: 4px 0 0; max-height: 4.5em; overflow: hidden; color: var(--h-text-muted); font-size: 12px; }
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
.app-shell__settings,
.app-shell__bootstrap {
  width: 100%;
  height: 100%;

  min-width: 0;
  min-height: 0;

  overflow: hidden;
}

.app-shell__bootstrap { display: grid; place-content: center; gap: 12px; color: var(--h-text-muted); }

.app-shell__main {
  display: grid;
  position: relative;
}
.app-shell__context-resizer { z-index: 2; width: 5px; cursor: col-resize; background: var(--h-border); }
.app-shell__context-resizer:hover, .app-shell__context-resizer:focus-visible { background: var(--h-accent-border); outline: none; }
</style>
