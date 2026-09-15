<script setup>
import {
  computed,
  ref,
  watch,
} from "vue";

import {
  Message,
} from "@arco-design/web-vue";

import {
  Dialogs,
} from "@wailsio/runtime";

import {
  IconDelete,
  IconEdit,
  IconFolder,
  IconPlus,
  IconSearch,
  IconSettings,
} from "@arco-design/web-vue/es/icon";

import {
  useAgentStore,
} from "../../stores/agents.js";

import {
  useRuntimeStore,
} from "../../stores/runtime.js";

import {
  useSessionStore,
} from "../../stores/sessions.js";

import ProjectModal
  from "./ProjectModal.vue";

import RenameSessionModal
  from "./RenameSessionModal.vue";

const expandedStorageKey =
    "humbert.sidebar.expanded-projects.v1";

const props =
    defineProps({
      activeView: {
        type: String,
        default: "chat",
      },
    });

const emit =
    defineEmits([
      "open-chat",
      "open-settings",
      "open-skills",
      "open-connectors",
    ]);

const agentStore =
    useAgentStore();

const sessionStore =
    useSessionStore();

const runtimeStore =
    useRuntimeStore();

const renameVisible =
    ref(false);

const renameTarget =
    ref(null);

const projectModalVisible =
    ref(false);

const projectTarget =
    ref(null);

/**
 * Project 展开状态属于纯 UI State。
 *
 * 存 localStorage，不进入后端配置。
 */
const expandedProjectIDs =
    ref(
        readExpandedProjects(),
    );

/**
 * 从 localStorage 恢复 Project 展开状态。
 */
function readExpandedProjects() {
  if (
      typeof window ===
      "undefined"
  ) {
    return new Set();
  }

  try {
    const raw =
        window.localStorage
            .getItem(
                expandedStorageKey,
            );

    if (!raw) {
      return new Set();
    }

    const parsed =
        JSON.parse(raw);

    if (!Array.isArray(parsed)) {
      return new Set();
    }

    return new Set(
        parsed.filter(
            (value) =>
                typeof value ===
                "string" &&
                value,
        ),
    );
  } catch {
    return new Set();
  }
}

/**
 * 保存 Project 展开状态。
 */
function persistExpandedProjects() {
  if (
      typeof window ===
      "undefined"
  ) {
    return;
  }

  try {
    window.localStorage.setItem(
        expandedStorageKey,
        JSON.stringify(
            Array.from(
                expandedProjectIDs
                    .value,
            ),
        ),
    );
  } catch {
    /**
     * localStorage 只影响 UI 偏好。
     *
     * 写入失败不能阻塞核心 Agent / Session 功能。
     */
  }
}

function isProjectExpanded(
    projectID,
) {
  return (
      expandedProjectIDs
          .value
          .has(projectID)
  );
}

/**
 * 展开一个 Project。
 *
 * Project 展开后需要确保对应 Session Metadata 已加载。
 */
async function expandProject(
    projectID,
) {
  const next =
      new Set(
          expandedProjectIDs.value,
      );

  next.add(projectID);

  expandedProjectIDs.value =
      next;

  persistExpandedProjects();

  try {
    await sessionStore
        .loadAgentSessions(
            projectID,
        );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  }
}

function collapseProject(
    projectID,
) {
  const next =
      new Set(
          expandedProjectIDs.value,
      );

  next.delete(projectID);

  expandedProjectIDs.value =
      next;

  persistExpandedProjects();
}

async function toggleProject(
    project,
) {
  if (
      isProjectExpanded(
          project.id,
      )
  ) {
    collapseProject(
        project.id,
    );

    return;
  }

  await expandProject(
      project.id,
  );
}

/**
 * 选择 Project。
 *
 * UI 中称为 Project，
 * Domain / Store 中仍然是 Agent。
 */
async function selectProject(
    project,
) {
  try {
    await expandProject(
        project.id,
    );

    if (
        agentStore.selectedID ===
        project.id &&
        sessionStore.agentID ===
        project.id
    ) {
      emit("open-chat");
      return;
    }

    agentStore.select(
        project.id,
    );

    /**
     * AppShell 原有 watch 仍然可以继续存在。
     *
     * Sidebar 主动 loadForAgent 是为了确保用户点击 Project 后
     * 当前视图立即具有确定状态。
     *
     * 重复读取只是 Session Metadata 查询，不会产生副作用。
     */
    await sessionStore
        .loadForAgent(
            project.id,
        );

    emit("open-chat");
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  }
}

/**
 * 为指定 Project 创建 Conversation。
 */
async function createConversation(
    project,
) {
  try {
    agentStore.select(
        project.id,
    );

    await expandProject(
        project.id,
    );

    await sessionStore
        .createForAgent(
            project.id,
        );

    emit("open-chat");
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  }
}

/**
 * 选择二级 Session。
 *
 * 如果目标 Session 属于另一个 Project，
 * 先切换 Project，再加载对应 Session。
 */
async function selectSession(
    project,
    session,
) {
  try {
    if (
        agentStore.selectedID !==
        project.id ||
        sessionStore.agentID !==
        project.id
    ) {
      agentStore.select(
          project.id,
      );

      await sessionStore
          .loadForAgent(
              project.id,
          );
    }

    await sessionStore.select(
        session.id,
    );

    emit("open-chat");
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  }
}

function openRenameSession(
    session,
) {
  renameTarget.value =
      session;

  renameVisible.value =
      true;
}

/**
 * 删除 Session。
 */
async function removeSession(
    session,
) {
  if (
      runtimeStore
          .isSessionRunning(
              session.id,
          )
  ) {
    Message.warning(
        "当前对话仍在生成回复，请先停止",
    );

    return;
  }

  const result =
      await Dialogs.Question({
        Title:
            "删除对话",

        Message:
            `确定删除「${session.title}」以及其中的全部消息吗？`,

        Buttons: [
          {
            Label:
                "删除",

            IsDefault:
                false,
          },
          {
            Label:
                "取消",

            IsDefault:
                true,
          },
        ],
      });

  if (
      result !== "删除"
  ) {
    return;
  }

  try {
    await sessionStore.remove(
        session.id,
    );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  }
}

/**
 * 打开创建 Project Modal。
 */
function openCreateProject() {
  projectTarget.value =
      null;

  projectModalVisible.value =
      true;
}

/**
 * 编辑已有 Project。
 */
function openEditProject(
    project,
) {
  projectTarget.value =
      project;

  projectModalVisible.value =
      true;
}

async function handleProjectCreated(
    project,
) {
  if (!project?.id) {
    return;
  }

  await expandProject(
      project.id,
  );

  /**
   * AgentStore.create 已经把新 Project 设为 selected。
   */
  await sessionStore
      .loadForAgent(
          project.id,
      );

  emit("open-chat");
}

function handleProjectDeleted(
    projectID,
) {
  sessionStore.forgetAgent(
      projectID,
  );

  collapseProject(
      projectID,
  );
}

/**
 * 当前搜索关键字。
 */
const searchKeyword =
    computed(() =>
        sessionStore.search
            .trim()
            .toLowerCase(),
    );

/**
 * 返回 Project 下需要显示的 Session。
 *
 * Project Name 自身命中搜索时显示它的全部 Session；
 * 否则只显示标题命中的 Session。
 */
function sessionsForProject(
    project,
) {
  const sessions =
      sessionStore
          .sessionsForAgent(
              project.id,
          );

  const keyword =
      searchKeyword.value;

  if (!keyword) {
    return sessions;
  }

  if (
      project.name
          .toLowerCase()
          .includes(keyword)
  ) {
    return sessions;
  }

  return sessions.filter(
      (session) =>
          session.title
              .toLowerCase()
              .includes(keyword),
  );
}

/**
 * Project Search 同时匹配：
 *
 *   Project Name
 *   Session Title
 */
const visibleProjects =
    computed(() => {
      const keyword =
          searchKeyword.value;

      if (!keyword) {
        return agentStore.items;
      }

      return agentStore.items.filter(
          (project) => {
            if (
                project.name
                    .toLowerCase()
                    .includes(
                        keyword,
                    )
            ) {
              return true;
            }

            return (
                sessionsForProject(
                    project,
                ).length > 0
            );
          },
      );
    });

/**
 * Project 是否有正在运行中的 Turn。
 */
function projectIsRunning(
    project,
) {
  return (
      sessionStore
          .sessionsForAgent(
              project.id,
          )
          .some(
              (session) =>
                  runtimeStore
                      .isSessionRunning(
                          session.id,
                      ),
          )
  );
}

/**
 * 搜索时需要展示匹配的二级 Session，
 * 因此搜索状态下临时认为 Project 是展开的。
 *
 * 不修改真实 expanded state。
 */
function shouldShowProjectSessions(
    project,
) {
  return (
      Boolean(
          searchKeyword.value,
      ) ||
      isProjectExpanded(
          project.id,
      )
  );
}

/**
 * Agent List 变化时把 Session Metadata 缓存起来。
 *
 * 只加载 Session List，不读取 Message Body，
 * 所以即使一个 Project 有很多历史聊天，
 * Sidebar 也不会一次性加载完整聊天内容。
 */
watch(
    () =>
        agentStore.items
            .map(
                (project) =>
                    project.id,
            )
            .join("|"),

    async () => {
      const failures = [];

      for (
          const project of
          agentStore.items
          ) {
        if (
            sessionStore
                .isAgentSessionsLoaded(
                    project.id,
                )
        ) {
          continue;
        }

        try {
          await sessionStore
              .loadAgentSessions(
                  project.id,
              );
        } catch (error) {
          failures.push(error);
        }
      }

      if (
          failures.length > 0
      ) {
        Message.error(
            "部分项目的对话列表加载失败",
        );
      }
    },

    {
      immediate: true,
    },
);

/**
 * 当前 Project 始终自动展开。
 *
 * 用户仍然可以之后手动折叠。
 */
watch(
    () =>
        agentStore.selectedID,

    (projectID) => {
      if (!projectID) {
        return;
      }

      void expandProject(
          projectID,
      );
    },

    {
      immediate: true,
    },
);
</script>

<template>
  <aside class="conversation-sidebar">
    <!-- 固定顶部 -->
    <header class="sidebar-header">
      <span class="sidebar-title">
        项目
      </span>

      <a-tooltip
          content="新建项目"
      >
        <a-button
            type="text"
            shape="circle"
            @click="
            openCreateProject
          "
        >
          <template #icon>
            <IconPlus/>
          </template>
        </a-button>
      </a-tooltip>
    </header>

    <nav
        class="sidebar-primary-nav"
        aria-label="工作区导航"
    >
      <button
          type="button"
          class="sidebar-primary-entry"
          :class="{
            'sidebar-primary-entry--active': props.activeView === 'skills',
          }"
          @click="emit('open-skills')"
      >
        <svg
            class="sidebar-primary-entry__icon"
            viewBox="0 0 24 24"
            aria-hidden="true"
        >
          <path d="M14.7 6.3a4 4 0 0 0-5 5L4 17v3h3l5.7-5.7a4 4 0 0 0 5-5l-2.4 2.4-3-3 2.4-2.4Z"/>
        </svg>

        <span class="sidebar-primary-entry__text">
          <strong>技能</strong>
          <small>为 Agent 配置 Skills</small>
        </span>
      </button>


      <button
          type="button"
          class="sidebar-primary-entry"
          :class="{
            'sidebar-primary-entry--active': props.activeView === 'connectors',
          }"
          @click="emit('open-connectors')"
      >
        <svg
            class="sidebar-primary-entry__icon"
            viewBox="0 0 24 24"
            aria-hidden="true"
        >
          <path d="M8 12h8M12 8v8M7 4h10a3 3 0 0 1 3 3v10a3 3 0 0 1-3 3H7a3 3 0 0 1-3-3V7a3 3 0 0 1 3-3Z"/>
        </svg>

        <span class="sidebar-primary-entry__text">
          <strong>连接器</strong>
          <small>MCP 与外部工具</small>
        </span>
      </button>
    </nav>



    <div class="sidebar-divider"></div>

    <!-- Search -->
    <div class="sidebar-search" style="margin-top: 10px">
      <a-input
          v-model="
          sessionStore.search
        "
          allow-clear
          placeholder="搜索项目或对话"
      >
        <template #prefix>
          <IconSearch/>
        </template>
      </a-input>
    </div>

    <!-- Project -> Session Tree -->
    <div class="project-tree">
      <div
          v-if="
          agentStore.items.length ===
          0
        "
          class="sidebar-empty"
      >
        <div>
          还没有项目
        </div>

        <a-button
            class="
            sidebar-empty-create
          "
            type="text"
            @click="
            openCreateProject
          "
        >
          <template #icon>
            <IconPlus/>
          </template>

          创建第一个项目
        </a-button>
      </div>

      <div
          v-else-if="
          visibleProjects.length ===
          0
        "
          class="sidebar-empty"
      >
        没有找到匹配的项目或对话
      </div>

      <section
          v-for="
          project in
          visibleProjects
        "
          :key="project.id"
          class="project-group"
      >
        <div
            class="project-row"
            :class="{
            'project-row--active':
              props.activeView === 'chat' &&
              agentStore.selectedID ===
              project.id,
          }"
        >
          <!-- 展开/折叠 -->
          <button
              type="button"
              class="project-toggle"
              :aria-label="
              isProjectExpanded(
                project.id,
              )
                ? '折叠项目'
                : '展开项目'
            "
              @click.stop="
              toggleProject(
                project,
              )
            "
          >
            <span
                class="project-chevron"
                :class="{
                'project-chevron--open':
                  isProjectExpanded(
                    project.id,
                  ) ||
                  Boolean(
                    searchKeyword,
                  ),
              }"
            >
              ›
            </span>
          </button>

          <!-- 选择 Project -->
          <button
              type="button"
              class="project-main"
              @click="
              selectProject(
                project,
              )
            "
          >
            <IconFolder
                class="project-icon"
            />

            <span
                class="
                project-text
              "
            >
              <span
                  class="
                  project-name
                "
              >
                {{ project.name }}
              </span>

              <span
                  class="
                  project-model
                "
              >
                {{
                  project
                      .modelDisplayName ||
                  "未配置模型"
                }}
              </span>
            </span>

            <span
                v-if="
                projectIsRunning(
                  project,
                )
              "
                class="
                project-running
              "
                title="项目中有正在运行的对话"
            ></span>
          </button>

          <!-- Project Actions -->
          <div class="project-actions">
            <a-tooltip
                content="新建对话"
            >
              <a-button
                  type="text"
                  size="mini"
                  @click.stop="
                  createConversation(
                    project,
                  )
                "
              >
                <template #icon>
                  <IconPlus/>
                </template>
              </a-button>
            </a-tooltip>

            <a-tooltip
                content="项目设置"
            >
              <a-button
                  type="text"
                  size="mini"
                  @click.stop="
                  openEditProject(
                    project,
                  )
                "
              >
                <template #icon>
                  <IconEdit/>
                </template>
              </a-button>
            </a-tooltip>
          </div>
        </div>

        <!-- 二级 Session -->
        <div
            v-if="
            shouldShowProjectSessions(
              project,
            )
          "
            class="project-sessions"
        >
          <button
              type="button"
              class="
              create-session-item
            "
              @click="
              createConversation(
                project,
              )
            "
          >
            <IconPlus/>

            <span>
              新建对话
            </span>
          </button>

          <div
              v-if="
              sessionStore
                .loadingAgents[
                  project.id
                ]
            "
              class="
              session-placeholder
            "
          >
            正在加载…
          </div>

          <div
              v-else-if="
              sessionsForProject(
                project,
              ).length === 0
            "
              class="
              session-placeholder
            "
          >
            {{
              searchKeyword
                  ? "没有匹配的对话"
                  : "还没有对话"
            }}
          </div>

          <div
              v-for="
              session in
              sessionsForProject(
                project,
              )
            "
              :key="session.id"
              class="session-row"
              :class="{
              'session-row--active':
                props.activeView === 'chat' &&
                agentStore
                  .selectedID ===
                  project.id &&
                sessionStore
                  .selectedID ===
                  session.id,
            }"
          >
            <button
                type="button"
                class="session-main"
                @click="
                selectSession(
                  project,
                  session,
                )
              "
            >
              <span
                  class="
                  session-name
                "
              >
                {{ session.title }}
              </span>

              <span
                  v-if="
                  runtimeStore
                    .isSessionRunning(
                      session.id,
                    )
                "
                  class="
                  session-running
                "
              ></span>
            </button>

            <div
                class="
                session-actions
              "
            >
              <a-button
                  type="text"
                  size="mini"
                  @click.stop="
                  openRenameSession(
                    session,
                  )
                "
              >
                <template #icon>
                  <IconEdit/>
                </template>
              </a-button>

              <a-button
                  type="text"
                  size="mini"
                  status="danger"
                  @click.stop="
                  removeSession(
                    session,
                  )
                "
              >
                <template #icon>
                  <IconDelete/>
                </template>
              </a-button>
            </div>
          </div>
        </div>
      </section>
    </div>

    <!-- 固定底部 -->
    <footer class="sidebar-footer">
      <button
          type="button"
          class="settings-entry"
          @click="
          emit(
            'open-settings',
          )
        "
      >
        <IconSettings/>

        <span>
          设置
        </span>
      </button>
    </footer>

    <RenameSessionModal
        v-model:visible="
        renameVisible
      "
        :session="
        renameTarget
      "
    />

    <ProjectModal
        v-model:visible="
        projectModalVisible
      "
        :project="
        projectTarget
      "
        @created="
        handleProjectCreated
      "
        @deleted="
        handleProjectDeleted
      "
    />
  </aside>
</template>

<style scoped>
.conversation-sidebar {
  display: flex;

  width: 100%;
  height: 100%;

  min-width: 0;
  min-height: 0;

  flex-direction: column;

  overflow: hidden;

  background: var(--h-sidebar);
}

/*
 * =========================================================
 * Header
 * =========================================================
 */

.sidebar-header {
  display: flex;

  height: 56px;

  flex: 0 0 56px;

  align-items: center;

  justify-content: space-between;

  padding: 0 12px 0 16px;
}

.sidebar-title {
  color: var(--h-text-secondary);

  font-size: 14px;

  font-weight: 500;
}

.sidebar-search {
  flex: 0 0 auto;

  padding: 0 12px 10px;
}

.sidebar-primary-nav {
  flex: 0 0 auto;
  padding: 0 8px 8px;
}

.sidebar-primary-entry {
  display: flex;
  width: 100%;
  min-width: 0;
  min-height: 46px;
  align-items: center;
  gap: 9px;
  padding: 6px 10px;
  cursor: pointer;
  border: 1px solid transparent;
  border-radius: 8px;
  background: transparent;
  color: var(--h-text-secondary);
  font: inherit;
  text-align: left;
}

.sidebar-primary-entry:hover {
  background: var(--h-surface-hover);
}

.sidebar-primary-entry--active {
  border-color: var(--h-border);
  background: var(--h-surface);
  color: var(--h-text);
}

.sidebar-primary-entry__icon {
  width: 17px;
  height: 17px;
  flex: 0 0 17px;
  fill: none;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.7;
}

.sidebar-primary-entry__text {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 1px;
}

.sidebar-primary-entry__text strong {
  color: inherit;
  font-size: 11px;
  font-weight: 500;
}

.sidebar-primary-entry__text small {
  overflow: hidden;
  color: var(--h-text-muted);
  font-size: 8px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sidebar-divider {
  height: 1px;

  flex: 0 0 1px;

  margin: 2px 12px 0;

  background: var(--h-border);
}

/*
 * =========================================================
 * Project Tree
 * =========================================================
 *
 * 只有这一块滚动。
 *
 * Header / Search / Settings 永远固定。
 */

.project-tree {
  min-height: 0;

  flex: 1;

  overflow-x: hidden;
  overflow-y: auto;

  padding: 10px 8px 14px;
}

.project-group +
.project-group {
  margin-top: 3px;
}

.project-row {
  display: flex;

  width: 100%;

  min-width: 0;

  align-items: center;

  min-height: 46px;

  border: 1px solid transparent;

  border-radius: 8px;

  background: transparent;
}

.project-row:hover {
  background: var(--h-surface-hover);
}

.project-row--active {
  border-color: var(--h-border);

  background: var(--h-surface);
}

.project-toggle {
  display: grid;

  width: 24px;
  height: 40px;

  flex: 0 0 24px;

  place-items: center;

  padding: 0;

  cursor: pointer;

  border: 0;

  outline: none;

  background: transparent;

  color: var(--h-text-muted);
}

.project-chevron {
  display: inline-block;

  font-size: 17px;

  line-height: 1;

  transition: transform 120ms ease;
}

.project-chevron--open {
  transform: rotate(90deg);
}

.project-main {
  display: flex;

  min-width: 0;

  flex: 1;

  align-items: center;

  gap: 8px;

  align-self: stretch;

  padding: 0;

  cursor: pointer;

  border: 0;

  outline: none;

  background: transparent;

  text-align: left;
}

.project-icon {
  flex: 0 0 auto;

  color: var(--h-text-muted);

  font-size: 14px;
}

.project-text {
  display: flex;

  min-width: 0;

  flex: 1;

  flex-direction: column;

  gap: 1px;
}

.project-name {
  overflow: hidden;

  color: var(--h-text);

  font-size: 12px;

  font-weight: 500;

  text-overflow: ellipsis;

  white-space: nowrap;
}

.project-model {
  overflow: hidden;

  color: var(--h-text-muted);

  font-size: 9px;

  text-overflow: ellipsis;

  white-space: nowrap;
}

.project-running {
  width: 6px;
  height: 6px;

  flex: 0 0 6px;

  margin-right: 3px;

  border-radius: 50%;

  background: var(--h-success);
}

.project-actions {
  display: none;

  flex: 0 0 auto;

  align-items: center;

  padding-right: 3px;
}

.project-row:hover
.project-actions,
.project-row--active
.project-actions {
  display: flex;
}

/*
 * =========================================================
 * Session Level
 * =========================================================
 */

.project-sessions {
  position: relative;

  margin: 2px 0 6px 23px;

  padding-left: 10px;

  border-left: 1px solid var(--h-border);
}

.create-session-item,
.session-row {
  width: 100%;

  min-width: 0;

  border-radius: 7px;
}

.create-session-item {
  display: flex;

  height: 30px;

  align-items: center;

  gap: 7px;

  padding: 0 8px;

  cursor: pointer;

  border: 1px solid transparent;

  outline: none;

  background: transparent;

  color: var(--h-text-muted);

  font-size: 10px;

  text-align: left;
}

.create-session-item:hover {
  background: var(--h-surface-hover);

  color: var(--h-text-secondary);
}

.session-row {
  display: flex;

  height: 34px;

  align-items: center;

  border: 1px solid transparent;

  background: transparent;
}

.session-row:hover {
  background: var(--h-surface-hover);
}

.session-row--active {
  border-color: var(--h-border);

  background: var(--h-surface);
}

.session-main {
  display: flex;

  min-width: 0;

  flex: 1;

  align-items: center;

  gap: 6px;

  align-self: stretch;

  padding: 0 6px 0 10px;

  cursor: pointer;

  border: 0;

  outline: none;

  background: transparent;

  color: var(--h-text-secondary);

  text-align: left;
}

.session-name {
  min-width: 0;

  flex: 1;

  overflow: hidden;

  font-size: 11px;

  text-overflow: ellipsis;

  white-space: nowrap;
}

.session-running {
  width: 5px;
  height: 5px;

  flex: 0 0 5px;

  border-radius: 50%;

  background: var(--h-success);
}

.session-actions {
  display: none;

  flex: 0 0 auto;

  align-items: center;

  padding-right: 2px;
}

.session-row:hover
.session-actions {
  display: flex;
}

.session-placeholder {
  padding: 8px 10px;

  color: var(--h-text-muted);

  font-size: 10px;
}

/*
 * =========================================================
 * Empty
 * =========================================================
 */

.sidebar-empty {
  padding: 34px 16px;

  color: var(--h-text-muted);

  font-size: 11px;

  line-height: 1.7;

  text-align: center;
}

.sidebar-empty-create {
  margin-top: 8px;
}

/*
 * =========================================================
 * Footer
 * =========================================================
 */

.sidebar-footer {
  flex: 0 0 auto;

  padding: 8px;

  border-top: 1px solid var(--h-border);
}

.settings-entry {
  display: flex;

  width: 100%;
  height: 36px;

  align-items: center;

  gap: 9px;

  padding: 0 10px;

  cursor: pointer;

  border: 1px solid transparent;

  border-radius: 8px;

  outline: none;

  background: transparent;

  color: var(--h-text-secondary);

  font-size: 11px;

  text-align: left;
}

.settings-entry:hover {
  background: var(--h-surface-hover);
}
</style>