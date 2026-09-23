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
  confirmAction,
} from "../../utils/confirm.js";

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
import { searchSessionMessages } from "../../api/sessions.js";

import AgentModal
  from "./AgentModal.vue";

import RenameSessionModal
  from "./RenameSessionModal.vue";

const expandedStorageKey =
    "humbert.sidebar.expanded-agents.v1";

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
      "open-tasks",
    ]);

const agentStore =
    useAgentStore();

const sessionStore =
    useSessionStore();

const contentResults = ref([]);
const contentSearching = ref(false);
const showArchived = ref(false);
let searchSequence = 0;
let searchTimer = null;

watch(() => sessionStore.search, (value) => {
  clearTimeout(searchTimer);
  const sequence = ++searchSequence;
  const query = value.trim();
  if (query.length < 2) { contentResults.value = []; contentSearching.value = false; return; }
  contentSearching.value = true;
  searchTimer = setTimeout(async () => {
    try {
      const results = await searchSessionMessages(query);
      if (sequence === searchSequence) contentResults.value = Array.isArray(results) ? results : [];
    } catch (error) {
      if (sequence === searchSequence) Message.error(error?.message ?? String(error));
    } finally {
      if (sequence === searchSequence) contentSearching.value = false;
    }
  }, 350);
});

async function openSearchResult(result) {
  const agent = agentStore.items.find((item) => item.id === result.agentID);
  if (!agent) return;
  try {
    await sessionStore.loadAgentSessions(agent.id, {force: true});
    const session = sessionStore.sessionsForAgent(agent.id).find((item) => item.id === result.sessionID);
    if (!session) return;
    if (!(await selectSession(agent, session))) return;
    sessionStore.jumpTargetID = result.entryID;
  } catch (error) { Message.error(error?.message ?? String(error)); }
}

async function archiveSession(session) {
  if (runtimeStore.isSessionRunning(session.id)) { Message.warning("请先停止当前对话"); return; }
  try { await sessionStore.setArchived(session.id, !session.archived); }
  catch (error) { Message.error(error?.message ?? String(error)); }
}

const runtimeStore =
    useRuntimeStore();

const renameVisible =
    ref(false);

const renameTarget =
    ref(null);

const agentModalVisible =
    ref(false);

const agentTarget =
    ref(null);

/**
 * Agent 展开状态属于纯 UI State。
 *
 * 存 localStorage，不进入后端配置。
 */
const expandedAgentIDs =
    ref(
        readExpandedAgents(),
    );

/**
 * 从 localStorage 恢复 Agent 展开状态。
 */
function readExpandedAgents() {
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
 * 保存 Agent 展开状态。
 */
function persistExpandedAgents() {
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
                expandedAgentIDs
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

function isAgentExpanded(
    agentID,
) {
  return (
      expandedAgentIDs
          .value
          .has(agentID)
  );
}

/**
 * 展开一个 Agent。
 *
 * Agent 展开后需要确保对应 Session Metadata 已加载。
 */
async function expandAgent(
    agentID,
) {
  const next =
      new Set(
          expandedAgentIDs.value,
      );

  next.add(agentID);

  expandedAgentIDs.value =
      next;

  persistExpandedAgents();

  try {
    await sessionStore
        .loadAgentSessions(
            agentID,
        );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  }
}

function collapseAgent(
    agentID,
) {
  const next =
      new Set(
          expandedAgentIDs.value,
      );

  next.delete(agentID);

  expandedAgentIDs.value =
      next;

  persistExpandedAgents();
}

async function toggleAgent(
    agent,
) {
  if (
      isAgentExpanded(
          agent.id,
      )
  ) {
    collapseAgent(
        agent.id,
    );

    return;
  }

  await expandAgent(
      agent.id,
  );
}

/**
 * 选择 Agent。
 *
 * 选择后同步加载该 Agent 的 Session。
 */
async function selectAgent(
    agent,
) {
  try {
    await expandAgent(
        agent.id,
    );

    if (
        agentStore.selectedID ===
        agent.id &&
        sessionStore.agentID ===
        agent.id
    ) {
      emit("open-chat");
      return;
    }

    agentStore.select(
        agent.id,
    );

    /**
     * AppShell 原有 watch 仍然可以继续存在。
     *
     * Sidebar 主动 loadForAgent 是为了确保用户点击 Agent 后
     * 当前视图立即具有确定状态。
     *
     * 重复读取只是 Session Metadata 查询，不会产生副作用。
     */
    await sessionStore
        .loadForAgent(
            agent.id,
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
 * 为指定 Agent 创建 Conversation。
 */
async function createConversation(
    agent,
) {
  try {
    agentStore.select(
        agent.id,
    );

    await expandAgent(
        agent.id,
    );

    await sessionStore
        .createForAgent(
            agent.id,
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
 * 如果目标 Session 属于另一个 Agent，
 * 先切换 Agent，再加载对应 Session。
 */
async function selectSession(
    agent,
    session,
) {
  try {
    if (
        agentStore.selectedID !==
        agent.id ||
        sessionStore.agentID !==
        agent.id
    ) {
      agentStore.select(
          agent.id,
      );

      await sessionStore
          .loadForAgent(
              agent.id,
          );
    }

    await sessionStore.select(
        session.id,
    );

    emit("open-chat");
    return true;
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
    return false;
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

  const confirmed =
      await confirmAction({
        title:
            "删除对话",

        message:
            `确定删除「${session.title}」以及其中的全部消息吗？如果它属于任务，对应的运行历史也会一并删除；连续任务下次运行时会创建新的对话。`,

        confirmText:
            "删除",
        danger: true,
      });

  if (!confirmed) {
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
 * 打开创建 Agent Modal。
 */
function openCreateAgent() {
  agentTarget.value =
      null;

  agentModalVisible.value =
      true;
}

/**
 * 编辑已有 Agent。
 */
function openEditAgent(
    agent,
) {
  agentTarget.value =
      agent;

  agentModalVisible.value =
      true;
}

async function handleAgentCreated(
    agent,
) {
  if (!agent?.id) {
    return;
  }

  await expandAgent(
      agent.id,
  );

  /**
   * AgentStore.create 已经把新 Agent 设为 selected。
   */
  await sessionStore
      .loadForAgent(
          agent.id,
      );

  emit("open-chat");
}

function handleAgentDeleted(
    agentID,
) {
  sessionStore.forgetAgent(
      agentID,
  );

  collapseAgent(
      agentID,
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
 * 返回 Agent 下需要显示的 Session。
 *
 * Agent Name 自身命中搜索时显示它的全部 Session；
 * 否则只显示标题命中的 Session。
 */
function sessionsForAgent(
    agent,
) {
  const sessions =
      sessionStore
          .sessionsForAgent(
              agent.id,
          ).filter((session) => showArchived.value || !session.archived);

  const keyword =
      searchKeyword.value;

  if (!keyword) {
    return sessions;
  }

  if (
      agent.name
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
 * Agent Search 同时匹配：
 *
 *   Agent Name
 *   Session Title
 */
const visibleAgents =
    computed(() => {
      const keyword =
          searchKeyword.value;

      if (!keyword) {
        return agentStore.items;
      }

      return agentStore.items.filter(
          (agent) => {
            if (
                agent.name
                    .toLowerCase()
                    .includes(
                        keyword,
                    )
            ) {
              return true;
            }

            return (
                sessionsForAgent(
                    agent,
                ).length > 0
            );
          },
      );
    });

/**
 * Agent 是否有正在运行中的 Turn。
 */
function agentIsRunning(
    agent,
) {
  return (
      sessionStore
          .sessionsForAgent(
              agent.id,
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
 * 因此搜索状态下临时认为 Agent 是展开的。
 *
 * 不修改真实 expanded state。
 */
function shouldShowAgentSessions(
    agent,
) {
  return (
      Boolean(
          searchKeyword.value,
      ) ||
      isAgentExpanded(
          agent.id,
      )
  );
}

/**
 * Agent List 变化时把 Session Metadata 缓存起来。
 *
 * 只加载 Session List，不读取 Message Body，
 * 所以即使一个 Agent 有很多历史聊天，
 * Sidebar 也不会一次性加载完整聊天内容。
 */
watch(
    () =>
        agentStore.items
            .map(
                (agent) =>
                    agent.id,
            )
            .join("|"),

    async () => {
      const failures = [];

      for (
          const agent of
          agentStore.items
          ) {
        if (
            sessionStore
                .isAgentSessionsLoaded(
                    agent.id,
                )
        ) {
          continue;
        }

        try {
          await sessionStore
              .loadAgentSessions(
                  agent.id,
              );
        } catch (error) {
          failures.push(error);
        }
      }

      if (
          failures.length > 0
      ) {
        Message.error(
            "部分 Agent 的对话列表加载失败",
        );
      }
    },

    {
      immediate: true,
    },
);

/**
 * 当前 Agent 始终自动展开。
 *
 * 用户仍然可以之后手动折叠。
 */
watch(
    () =>
        agentStore.selectedID,

    (agentID) => {
      if (!agentID) {
        return;
      }

      void expandAgent(
          agentID,
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
      <div class="sidebar-brand">
        <span class="sidebar-title">工作台</span>
        <span class="sidebar-kicker">AGENTS &amp; SESSIONS</span>
      </div>

      <a-tooltip
          content="新建 Agent"
      >
        <a-button
            type="text"
            shape="circle"
            @click="
            openCreateAgent
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
        aria-label="主导航"
    >
      <button
          type="button"
          class="sidebar-primary-entry"
          :class="{
            'sidebar-primary-entry--active': props.activeView === 'tasks',
          }"
          @click="emit('open-tasks')"
      >
        <svg
            class="sidebar-primary-entry__icon"
            viewBox="0 0 24 24"
            aria-hidden="true"
        >
          <path d="M7 3v3M17 3v3M5 5h14a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2Zm-2 5h18M8 12h3M8 16h7"/>
        </svg>

        <span class="sidebar-primary-entry__text">
          <strong>任务</strong>
          <small>主动运行与定时计划</small>
        </span>
      </button>

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
    <div class="sidebar-search sidebar-search--sessions">
      <a-input
          v-model="
          sessionStore.search
        "
          allow-clear
          placeholder="搜索 Agent 或对话"
      >
        <template #prefix>
          <IconSearch/>
        </template>
      </a-input>
    </div>

    <div v-if="searchKeyword.length >= 2" class="sidebar-content-results">
      <small>{{ contentSearching ? '正在搜索消息…' : `消息正文 · ${contentResults.length} 条结果` }}</small>
      <button v-for="result in contentResults" :key="`${result.sessionID}:${result.entryID}`" type="button" class="sidebar-content-result" @click="openSearchResult(result)">
        <strong>{{ result.title }}{{ result.archived ? '（已归档）' : '' }}</strong>
        <span>{{ result.snippet }}</span>
      </button>
    </div>
    <button type="button" class="sidebar-archive-toggle" @click="showArchived = !showArchived">{{ showArchived ? '隐藏已归档会话' : '显示已归档会话' }}</button>

    <!-- Agent -> Session Tree -->
    <div class="agent-tree">
      <div
          v-if="
          agentStore.items.length ===
          0
        "
          class="sidebar-empty"
      >
        <div>
          还没有 Agent
        </div>

        <a-button
            class="
            sidebar-empty-create
          "
            type="text"
            @click="
            openCreateAgent
          "
        >
          <template #icon>
            <IconPlus/>
          </template>

          创建第一个 Agent
        </a-button>
      </div>

      <div
          v-else-if="
          visibleAgents.length ===
          0
        "
          class="sidebar-empty"
      >
        没有找到匹配的 Agent 或对话
      </div>

      <section
          v-for="
          agent in
          visibleAgents
        "
          :key="agent.id"
          class="agent-group"
      >
        <div
            class="agent-row"
            :class="{
            'agent-row--active':
              props.activeView === 'chat' &&
              agentStore.selectedID ===
              agent.id,
          }"
        >
          <!-- 展开/折叠 -->
          <button
              type="button"
              class="agent-toggle"
              :aria-label="
              isAgentExpanded(
                agent.id,
              )
                ? '折叠 Agent'
                : '展开 Agent'
            "
              @click.stop="
              toggleAgent(
                agent,
              )
            "
          >
            <span
                class="agent-chevron"
                :class="{
                'agent-chevron--open':
                  isAgentExpanded(
                    agent.id,
                  ) ||
                  Boolean(
                    searchKeyword,
                  ),
              }"
            >
              ›
            </span>
          </button>

          <!-- 选择 Agent -->
          <button
              type="button"
              class="agent-main"
              @click="
              selectAgent(
                agent,
              )
            "
          >
            <IconFolder
                class="agent-icon"
            />

            <span
                class="
                agent-text
              "
            >
              <span
                  class="
                  agent-name
                "
              >
                {{ agent.name }}
              </span>

              <span
                  class="
                  agent-model
                "
              >
                {{
                  agent
                      .modelDisplayName ||
                  "未配置模型"
                }}
                <span v-if="agent.subagentEnabled" class="agent-collaboration-mark">· 可协作</span>
              </span>
            </span>

            <span
                v-if="
                agentIsRunning(
                  agent,
                )
              "
                class="
                agent-running
              "
                title="Agent 中有正在运行的对话"
            ></span>
          </button>

          <!-- Agent Actions -->
          <div class="agent-actions">
            <a-tooltip
                content="新建对话"
            >
              <a-button
                  type="text"
                  size="mini"
                  @click.stop="
                  createConversation(
                    agent,
                  )
                "
              >
                <template #icon>
                  <IconPlus/>
                </template>
              </a-button>
            </a-tooltip>

            <a-tooltip
                content="Agent 设置"
            >
              <a-button
                  type="text"
                  size="mini"
                  @click.stop="
                  openEditAgent(
                    agent,
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
            shouldShowAgentSessions(
              agent,
            )
          "
            class="agent-sessions"
        >
          <button
              type="button"
              class="
              create-session-item
            "
              @click="
              createConversation(
                agent,
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
                  agent.id
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
              sessionsForAgent(
                agent,
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
              sessionsForAgent(
                agent,
              )
            "
              :key="session.id"
              class="session-row"
              :class="{
              'session-row--active':
                props.activeView === 'chat' &&
                agentStore
                  .selectedID ===
                  agent.id &&
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
                  agent,
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
                  :title="session.archived ? '恢复会话' : '归档会话'"
                  @click.stop="archiveSession(session)"
              >
                {{ session.archived ? '恢复' : '归档' }}
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

    <AgentModal
        v-model:visible="
        agentModalVisible
      "
        :agent="
        agentTarget
      "
        @created="
        handleAgentCreated
      "
        @deleted="
        handleAgentDeleted
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

  height: 64px;

  flex: 0 0 64px;

  align-items: center;

  justify-content: space-between;

  padding: 0 12px 0 18px;
}

.sidebar-brand {
  display: flex;

  min-width: 0;

  flex-direction: column;

  gap: 2px;
}

.sidebar-title {
  color: var(--h-text);

  font-size: 16px;

  font-weight: 500;
}

.sidebar-kicker {
  color: var(--h-text-muted);

  font-family: var(--h-ui);

  font-size: 8px;

  letter-spacing: 0.11em;
}

.sidebar-search {
  flex: 0 0 auto;

  padding: 0 12px 10px;
}

.sidebar-search--sessions {
  margin-top: 10px;
}

.sidebar-content-results {
  max-height: 230px;
  overflow: auto;
  padding: 0 12px 8px;
}

.sidebar-content-results small { color: var(--h-text-muted); }
.sidebar-content-result { display: flex; width: 100%; flex-direction: column; gap: 3px; padding: 7px; border: 0; border-radius: 6px; background: transparent; color: var(--h-text-secondary); text-align: left; cursor: pointer; }
.sidebar-content-result:hover { background: var(--h-bg-hover); }
.sidebar-content-result strong { color: var(--h-text); font-size: 12px; }
.sidebar-content-result span { overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; font-size: 11px; }
.sidebar-archive-toggle { margin: 0 12px 8px; border: 0; background: transparent; color: var(--h-text-muted); cursor: pointer; text-align: left; font-size: 11px; }

.sidebar-primary-nav {
  flex: 0 0 auto;
  padding: 0 8px 6px;
}

.sidebar-primary-entry {
  display: flex;
  width: 100%;
  min-width: 0;
  min-height: 36px;
  align-items: center;
  gap: 9px;
  padding: 5px 10px 5px 12px;
  cursor: pointer;
  border: 0;
  border-left: 2px solid transparent;
  border-radius: 0 6px 6px 0;
  background: transparent;
  color: var(--h-text-secondary);
  font: inherit;
  text-align: left;
}

.sidebar-primary-entry:hover {
  background: var(--h-surface-hover);
}

.sidebar-primary-entry--active {
  border-left-color: var(--h-accent);
  background: transparent;
  color: var(--h-text);
}

.sidebar-primary-entry--active .sidebar-primary-entry__icon {
  color: var(--h-accent);
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
  font-size: 12px;
  font-weight: 500;
}

.sidebar-primary-entry__text small {
  display: none;
}

.sidebar-divider {
  height: 1px;

  flex: 0 0 1px;

  margin: 2px 12px 0;

  background: var(--h-border);
}

/*
 * =========================================================
 * Agent Tree
 * =========================================================
 *
 * 只有这一块滚动。
 *
 * Header / Search / Settings 永远固定。
 */

.agent-tree {
  min-height: 0;

  flex: 1;

  overflow-x: hidden;
  overflow-y: auto;

  padding: 10px 8px 14px;
}

.agent-group +
.agent-group {
  margin-top: 3px;
}

.agent-row {
  display: flex;

  width: 100%;

  min-width: 0;

  align-items: center;

  min-height: 46px;

  border: 0;
  border-left: 2px solid transparent;

  border-radius: 0 6px 6px 0;

  background: transparent;
}

.agent-row:hover {
  background: var(--h-surface-hover);
}

.agent-row--active {
  border-left-color: var(--h-accent);

  background: transparent;
}

.agent-toggle {
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

.agent-chevron {
  display: inline-block;

  font-size: 17px;

  line-height: 1;

  transition: transform 120ms ease;
}

.agent-chevron--open {
  transform: rotate(90deg);
}

.agent-main {
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

.agent-icon {
  flex: 0 0 auto;

  color: var(--h-text-muted);

  font-size: 14px;
}

.agent-text {
  display: flex;

  min-width: 0;

  flex: 1;

  flex-direction: column;

  gap: 1px;
}

.agent-name {
  overflow: hidden;

  color: var(--h-text);

  font-size: 12px;

  font-weight: 500;

  text-overflow: ellipsis;

  white-space: nowrap;
}

.agent-model {
  overflow: hidden;

  color: var(--h-text-muted);

  font-size: 9px;

  text-overflow: ellipsis;

  white-space: nowrap;
}

.agent-collaboration-mark {
  color: var(--h-accent);
  font-weight: 600;
}

.agent-running {
  width: 6px;
  height: 6px;

  flex: 0 0 6px;

  margin-right: 3px;

  border-radius: 50%;

  background: var(--h-success);
}

.agent-actions {
  display: none;

  flex: 0 0 auto;

  align-items: center;

  padding-right: 3px;
}

.agent-row:hover
.agent-actions,
.agent-row--active
.agent-actions {
  display: flex;
}

/*
 * =========================================================
 * Session Level
 * =========================================================
 */

.agent-sessions {
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

  border: 0;
  border-left: 2px solid transparent;

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
  border-left-color: var(--h-accent);

  background: transparent;
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
