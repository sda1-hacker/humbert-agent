<script setup>
import {
  computed,
} from "vue";

import {
  useAgentStore,
} from "../../stores/agents.js";

import {
  useModelStore,
} from "../../stores/models.js";

import {
  usePreferenceStore,
} from "../../stores/preferences.js";

import {
  DEFAULT_AGENT_AVATAR,
} from "../../utils/avatar.js";

import MarkdownRenderer from "./MarkdownRenderer.vue";
import MessageAttachments from "./MessageAttachments.vue";
import MessageActions from "./MessageActions.vue";
import IdentityAvatar from "../ui/IdentityAvatar.vue";

const props =
    defineProps({
      /**
       * Session Message DTO。
       *
       * 当前主要展示：
       *
       * - user
       * - assistant
       *
       * system / tool 后续使用专用组件展示，
       * 不直接作为普通聊天气泡处理。
       */
      message: {
        type: Object,
        required: true,
      },
    });

const agentStore =
    useAgentStore();

const modelStore =
    useModelStore();

const preferenceStore =
    usePreferenceStore();

const userName = computed(() => preferenceStore.user?.name || "你");
const userAvatar = computed(() => preferenceStore.user?.avatar || "");
const agentAvatar = computed(() => agentStore.selectedAgent?.avatar || DEFAULT_AGENT_AVATAR);

/**
 * 当前 Assistant Message 真正使用的模型。
 *
 * model_id 来源于后端 RuntimeSnapshot 保存到 Message Metadata
 * 的实际执行记录，而不是让 LLM 自己声称模型身份。
 */
const messageModel =
    computed(() => {
      const modelID =
          props.message
              ?.metadata
              ?.model_id;

      if (!modelID) {
        return null;
      }

      return modelStore
          .modelByID(modelID);
    });

/**
 * Assistant 显示名称。
 *
 * 示例：
 *
 *   Humbert · qwen3.8-flash
 */
const assistantName =
    computed(() => {
      const agentName =
          agentStore
              .selectedAgent
              ?.name ||
          "Humbert";

      const modelName =
          messageModel.value
              ?.displayName;

      if (!modelName) {
        return agentName;
      }

      return (
          `${agentName} · ${modelName}`
      );
    });
</script>

<template>
  <!-- ==============================
       User
       ============================== -->
  <article
      v-if="
      message.role === 'user'
    "
      :data-entry-id="message.id"
      class="
      message-row
      message-row--user
    "
  >
    <div
        class="
        message-column
        message-column--user
      "
    >
      <div
          class="
          message-author
          message-author--user
        "
      >
        {{ userName }}
      </div>

      <div
          class="
          message-bubble
          message-bubble--user
        "
      >
        <div v-if="message.content" class="message-content message-content--user">{{ message.content }}</div>
        <MessageAttachments
            :session-id="message.sessionID"
            :attachments="message.attachments || []"
        />
      </div>

      <MessageActions
          :message-id="message.id"
          :content="message.content"
          :session-id="message.sessionID"
          allow-reuse
      />
    </div>
    <IdentityAvatar :src="userAvatar" :name="userName" :size="30" />
  </article>

  <!-- ==============================
       Assistant
       ============================== -->
  <article
      v-else-if="
      message.role === 'assistant'
    "
      :data-entry-id="message.id"
      class="
      message-row
      message-row--assistant
    "
  >
    <IdentityAvatar
        :src="agentAvatar"
        :name="agentStore.selectedAgent?.name || 'Humbert'"
        :size="30"
    />
    <div
        class="
        message-column
        message-column--assistant
      "
    >
      <div
          class="
          message-author
          message-author--assistant
        "
      >
        {{ assistantName }}
      </div>

      <div
          class="
          message-bubble
          message-bubble--assistant
        "
      >
        <div
            v-if="message.content"
            class="message-content"
        >
          <MarkdownRenderer :content="message.content" />
        </div>

        <div
            v-if="
            message.metadata
              ?.incomplete === true
          "
            class="
            message-incomplete
          "
        >
          回复未完整生成
        </div>
      </div>


      <MessageActions
          :message-id="message.id"
          :content="message.content"
          :session-id="message.sessionID"
      />
    </div>
  </article>
</template>

<style scoped>
/*
 * 一条消息占据整个聊天内容宽度。
 *
 * 具体靠左还是靠右由 message-row 决定，
 * 而不是通过气泡本身 margin hack。
 */
.message-row {
  display: flex;

  width: 100%;

  margin-bottom: 26px;

  align-items: flex-start;

  gap: 10px;
}

.message-row--assistant {
  justify-content: flex-start;
}

.message-row--user {
  justify-content: flex-end;
}

/*
 * 每一侧都是：
 *
 * 名称
 * ↓
 * Message Bubble
 */
.message-column {
  display: flex;

  max-width: min(
      72%,
      620px
  );

  flex-direction: column;

  min-width: 0;
}

.message-column--assistant {
  align-items: flex-start;
}

.message-column--user {
  align-items: flex-end;
}

/*
 * 用户与 AI 都显示名称。
 */
.message-author {
  margin-bottom: 7px;

  font-size: 11px;

  line-height: 1;

  user-select: none;
}

.message-author--assistant {
  color:
      var(--h-accent);
}

.message-author--user {
  color:
      var(--h-text-secondary);

  text-align: right;
}

/*
 * 用户消息使用轻量色块；Assistant 回答回归文档式正文，
 * 避免长回答被包进层层卡片。
 */
.message-bubble {
  max-width: 100%;

  padding:
      10px 14px;

  border-radius: 8px;

  font-size: 14px;

  line-height: 1.75;

  overflow-wrap: anywhere;

  word-break: break-word;
}

.message-bubble--assistant {
  padding: 0;

  border: 0;

  background: transparent;

  color:
      var(--h-text);
}

.message-bubble--user {
  border: 0;

  background:
      var(--h-accent-soft);

  color:
      var(--h-text);
}

.message-content {
  overflow-wrap: anywhere;

  word-break: break-word;
}

.message-content--user {
  white-space: pre-wrap;

  overflow-wrap: anywhere;

  word-break: break-word;
}

.message-incomplete {
  margin-top: 8px;

  color:
      var(--h-warning);

  font-size: 10px;
}

/*
 * 窗口比较窄时增加消息可用宽度。
 */
@media (
max-width: 900px
) {
  .message-column {
    max-width: 82%;
  }
}
</style>
