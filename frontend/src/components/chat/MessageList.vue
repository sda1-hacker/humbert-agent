<script setup>
import {
  computed,
  nextTick,
  onMounted,
  onUnmounted,
  ref,
  watch,
} from "vue";

import {
  Message,
} from "@arco-design/web-vue";

import {
  IconDown,
} from "@arco-design/web-vue/es/icon";

import {
  useAgentStore,
} from "../../stores/agents.js";

import {
  useModelStore,
} from "../../stores/models.js";

import {
  useRuntimeStore,
} from "../../stores/runtime.js";

import {
  useSessionStore,
} from "../../stores/sessions.js";

import {
  buildConversationBlocks,
  metadataOf,
} from "../../utils/toolTrace.js";

import {
  DEFAULT_AGENT_AVATAR,
} from "../../utils/avatar.js";

import AssistantTurn
  from "./AssistantTurn.vue";

import LiveAssistantTurn
  from "./LiveAssistantTurn.vue";

import MessageItem
  from "./MessageItem.vue";


const emit = defineEmits([
  "open-workspace-file",
  "open-task",
]);

const viewport =
    ref(null);

const followLatest =
    ref(true);

/**
 * 流式回答期间的滚动请求只允许每个 Animation Frame 执行一次。
 *
 * 大模型可能每秒产生几十甚至上百个 Delta。如果每个 Delta 都调用 smooth scroll，
 * WebView 会同时维护多段尚未结束的滚动动画，既会造成消息列表上下抖动，也更容易
 * 触发 macOS/WebKit Overlay Scrollbar 的残影。
 */
let scrollFrameID =
    0;

const agentStore =
    useAgentStore();

const modelStore =
    useModelStore();

const runtimeStore =
    useRuntimeStore();

const sessionStore =
    useSessionStore();

const conversationBlocks =
    computed(() =>
        buildConversationBlocks(
            sessionStore.messages,
        ),
    );

const streaming =
    computed(() =>
        runtimeStore.streamingContent(
            sessionStore.selectedID,
        ),
    );

const running =
    computed(() =>
        runtimeStore.isSessionRunning(
            sessionStore.selectedID,
        ),
    );

const streamingModel =
    computed(() => {
      const modelID =
          runtimeStore.modelIDForSession(
              sessionStore.selectedID,
          );

      if (!modelID) {
        return null;
      }

      return modelStore.modelByID(
          modelID,
      );
    });

const currentAgentName =
    computed(() =>
        agentStore
            .selectedAgent
            ?.name ||
        "Humbert",
    );

const currentAgentAvatar =
    computed(() =>
        agentStore
            .selectedAgent
            ?.avatar ||
        DEFAULT_AGENT_AVATAR,
    );

const currentModelName =
    computed(() =>
        streamingModel.value
            ?.displayName ||
        agentStore
            .selectedAgent
            ?.modelDisplayName ||
        "",
    );

const currentApproval =
    computed(() =>
        runtimeStore.pendingApproval(
            sessionStore.selectedID,
        ),
    );

const currentApprovalResolving =
    computed(() =>
        currentApproval.value
            ? runtimeStore.isApprovalResolving(
                currentApproval.value.id,
            )
            : false,
    );

const currentApprovalError =
    computed(() =>
        currentApproval.value
            ? runtimeStore.approvalError(
                currentApproval.value.id,
            )
            : "",
    );

async function resolveCurrentApproval(decision) {
  const approvalID =
      currentApproval.value?.id;
  if (!approvalID) {
    return;
  }

  try {
    await runtimeStore.resolveToolApproval(
        approvalID,
        decision,
    );
  } catch (error) {
    Message.error(
        error?.message ?? String(error),
    );
  }
}

async function loadOlderMessages() {
  const element =
      viewport.value;
  const previousHeight =
      element?.scrollHeight ?? 0;
  const previousTop =
      element?.scrollTop ?? 0;

  try {
    const added =
        await sessionStore
            .loadOlderMessages();
    if (!added || !element) {
      return;
    }

    await nextTick();
    element.scrollTop =
        previousTop +
        element.scrollHeight -
        previousHeight;
    followLatest.value = false;
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  }
}

const currentLiveTools =
    computed(() =>
        runtimeStore.liveTools(
            sessionStore.selectedID,
        ),
    );

/**
 * 把历史 Assistant Turn 中的文件点击提升到 AppShell。
 *
 * 文件本身只携带相对路径；所属 Agent 来自当前 Session 的一级上下文，避免聊天组件
 * 自己直接修改全局工作区路由。
 */
function openWorkspaceFile(payload) {
  const path =
      typeof payload?.path === "string"
          ? payload.path
          : "";

  const agentID =
      sessionStore.agentID ||
      agentStore.selectedID;

  if (!path || !agentID) {
    return;
  }

  emit("open-workspace-file", {
    agentID,
    path,
  });
}

function modelNameForMessage(
    message,
) {
  const metadata =
      metadataOf(message);

  const modelID =
      typeof metadata.model_id ===
      "string"
          ? metadata.model_id
          : (
              typeof metadata.modelID ===
              "string"
                  ? metadata.modelID
                  : ""
          );

  if (modelID) {
    const model =
        modelStore.modelByID(
            modelID,
        );

    if (model?.displayName) {
      return model.displayName;
    }
  }

  return (
      agentStore
          .selectedAgent
          ?.modelDisplayName ||
      ""
  );
}

function handleScroll() {
  const element =
      viewport.value;

  if (!element) {
    return;
  }

  const distanceToBottom =
      element.scrollHeight -
      element.scrollTop -
      element.clientHeight;

  followLatest.value =
      distanceToBottom <= 96;
}

async function scrollToBottom(
    force = false,
) {
  if (
      !force &&
      !followLatest.value
  ) {
    return;
  }

  await nextTick();

  /**
   * 同一帧内可能同时收到：
   *
   * - assistant.delta；
   * - assistant.reasoning.delta；
   * - tool lifecycle；
   * - Session Message 更新。
   *
   * 这里只保留一次滚动写操作，避免连续 Layout/Reflow。
   */
  if (scrollFrameID !== 0) {
    return;
  }

  scrollFrameID =
      requestAnimationFrame(
          () => {
            scrollFrameID =
                0;

            const element =
                viewport.value;

            if (!element) {
              return;
            }

            /**
             * 流式输出期间禁止 smooth scroll。
             *
             * 直接设置 scrollTop 只有一个确定终态，不会让多个动画互相竞争；
             * 同时不再写 scrollLeft，彻底避免 WebKit 创建横向滚动活动。
             */
            element.scrollTop =
                element.scrollHeight;

            followLatest.value =
                true;
          },
      );
}

watch(
    () =>
        sessionStore.selectedID,

    () => {
      followLatest.value =
          true;

      void scrollToBottom(
          true,
      );
    },
);

watch(
    () =>
        sessionStore.messages
            .map(
                (message) =>
                    message.id,
            )
            .join("|"),

    () => {
      void scrollToBottom();
    },
);

watch(
    streaming,

    () => {
      void scrollToBottom();
    },
);

watch(
    currentLiveTools,

    () => {
      void scrollToBottom();
    },

    {deep: true},
);

onMounted(() => {
  void scrollToBottom(
      true,
  );
});

onUnmounted(() => {
  if (scrollFrameID !== 0) {
    cancelAnimationFrame(
        scrollFrameID,
    );

    scrollFrameID =
        0;
  }

});
</script>

<template>
  <section class="message-list">
    <div
        ref="viewport"
        class="message-viewport"
        @scroll.passive="
        handleScroll
      "
    >
      <div
          class="message-container"
      >
        <div
            v-if="sessionStore.messageHasMore"
            class="message-history-more"
        >
          <a-button
              size="small"
              type="text"
              :loading="sessionStore.loadingOlderMessages"
              @click="loadOlderMessages"
          >
            加载更早消息
          </a-button>
        </div>

        <div
            v-if="
            conversationBlocks.length ===
              0 &&
            !running &&
            !streaming
          "
            class="message-empty"
        >
          <div class="message-empty-eyebrow">READY</div>

          <div
              class="
              message-empty-title
            "
          >
            开始一段新对话
          </div>

          <p class="message-empty-description">
            写下目标或添加材料，{{ currentAgentName }} 会从当前上下文开始工作。
          </p>
        </div>

        <template
            v-for="
            block in
            conversationBlocks
          "
            :key="block.key"
        >
          <MessageItem
              v-if="
              block.type ===
              'message'
            "
              :message="
              block.message
            "
          />

          <AssistantTurn
              v-else-if="
              block.type ===
              'assistant-turn'
            "
              :message="
              block.message
            "
              :trace="
              block.trace
            "
              :agent-name="
              currentAgentName
            "
              :agent-avatar="currentAgentAvatar"
              :model-name="
              modelNameForMessage(
                block.message,
              )
            "
              @open-workspace-file="openWorkspaceFile"
              @open-task="emit('open-task', $event)"
          />
        </template>

        <!--
          实时 Turn：

          Humbert · Model

          思考过程
          ├─ Tool 1
          └─ Tool 2

          最终回答
        -->
        <LiveAssistantTurn
            v-if="
            running ||
            streaming
          "
            :agent-name="
            currentAgentName
          "
            :agent-avatar="currentAgentAvatar"
            :model-name="
            currentModelName
          "
            :content="
            streaming
          "
            :running="
            running
          "
            :tools="
            currentLiveTools
          "
            :approval="
            currentApproval
          "
            :approval-resolving="
            currentApprovalResolving
          "
            :approval-error="
            currentApprovalError
          "
            @approval-decision="
            resolveCurrentApproval
          "
        />

        <div
            class="
            message-bottom-space
          "
        ></div>
      </div>
    </div>

    <a-button
        v-if="!followLatest"
        class="
        message-to-bottom
      "
        size="small"
        type="secondary"
        @click="
        scrollToBottom(true)
      "
    >
      <template #icon>
        <IconDown/>
      </template>

      最新消息
    </a-button>
  </section>
</template>

<style scoped>
.message-list {
  position: relative;

  width: 100%;
  height: 100%;

  max-width: 100%;

  min-width: 0;
  min-height: 0;

  flex: 1 1 auto;

  /**
   * 这里不使用 container-type / contain:size。
   *
   * 当前组件没有任何 @container 查询，原来的 Size Containment 只会额外创建
   * Layout/Scroll Containing Context。在 Wails 的 WebKit WebView 中，它和内部
   * overflow scroll layer 组合后容易留下 Overlay Scrollbar 的横向残影。
   */
  overflow: hidden;

  background: var(--h-bg);
}

.message-viewport {
  position: relative;

  width: 100%;
  height: 100%;

  max-width: 100%;

  min-width: 0;
  min-height: 0;

  /**
   * 消息列表只允许垂直滚动。
   *
   * 使用 hidden 而不是 clip，兼容不同 WebView 版本；任何 Markdown、代码块、
   * Tool 参数造成的超宽内容都必须在自己的组件内部处理，绝不能让消息视口产生
   * 横向 Scrollbar。
   */
  overflow-x: hidden;
  overflow-y: auto;

  overscroll-behavior: contain;

  touch-action: pan-y;

  /**
   * 浏览器自己的 Scroll Anchoring 会在 Streaming DOM 高度变化时尝试“保持锚点”，
   * 而 Humbert 已经由 scrollToBottom() 管理跟随最新消息。关闭自动锚定可以避免
   * 两套滚动策略互相抢位置造成上下抖动。
   */
  overflow-anchor: none;

  scroll-behavior: auto;

  /**
   * 为垂直 Scrollbar 预留稳定空间，消息增长到出现滚动条时不会改变正文宽度。
   */
  scrollbar-gutter: stable;
}

/**
 * main.css 中存在全局 *::-webkit-scrollbar { height: 6px }。
 *
 * 对 MessageList 单独覆盖横向尺寸，确保 macOS/WebKit 即使短暂判断出了横向
 * overflow，也没有可绘制的水平 Scrollbar Track/Thumb。这正是截图里那条圆角灰线
 * 的视觉来源。
 */
.message-viewport::-webkit-scrollbar {
  width: 6px;
  height: 0 !important;
}

.message-viewport::-webkit-scrollbar:horizontal,
.message-viewport::-webkit-scrollbar-track:horizontal,
.message-viewport::-webkit-scrollbar-thumb:horizontal {
  display: none;

  height: 0 !important;

  background: transparent;
}

.message-container {
  width: 100%;
  max-width: 860px;

  min-width: 0;
  min-height: 100%;

  margin: 0 auto;

  padding: 42px 34px 0;
}

.message-container > * {
  max-width: 100%;
  min-width: 0;
}

.message-history-more {
  display: flex;
  justify-content: center;
  min-height: 36px;
  margin-bottom: 10px;
}

.message-empty {
  display: flex;

  min-height: 360px;

  align-items: center;
  justify-content: center;

  flex-direction: column;
}

.message-empty-eyebrow {
  color: var(--h-accent);

  font-family: var(--h-ui);

  font-size: 10px;

  font-weight: 500;

  letter-spacing: 0.12em;
}

.message-empty-title {
  margin-top: 12px;

  color: var(--h-text);

  font-size: 26px;

  font-weight: 500;
}

.message-empty-description {
  max-width: 420px;

  margin: 10px 0 0;

  color: var(--h-text-muted);

  font-size: 12px;

  line-height: 1.7;

  text-align: center;
}

.message-bottom-space {
  height: 26px;
}

.message-to-bottom {
  position: absolute;

  left: 50%;
  bottom: 12px;

  transform: translateX(-50%);

  border-color: var(--h-border-strong) !important;

  background: var(--h-surface) !important;

  box-shadow: none !important;
}

@media (
max-width: 900px
) {
  .message-container {
    padding-right: 22px;

    padding-left: 22px;
  }
}
</style>
