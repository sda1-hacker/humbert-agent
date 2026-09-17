<script setup>
import {
  Message,
} from "@arco-design/web-vue";

import {
  useAgentStore,
} from "../../stores/agents.js";

import {
  useSessionStore,
} from "../../stores/sessions.js";

const agentStore =
    useAgentStore();

const sessionStore =
    useSessionStore();

async function createSession() {
  try {
    await sessionStore.create();
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  }
}
</script>

<template>
  <section class="chat-empty">
    <div class="chat-empty__content">
      <div class="chat-empty__eyebrow">NEW CONVERSATION</div>

      <h1>今天想从哪里开始？</h1>

      <p v-if="agentStore.selectedID">
        {{ agentStore.selectedAgent?.name || "Humbert" }} 已准备好。描述目标、贴入材料，或直接提出第一个问题。
      </p>

      <p v-else>
        先在左侧创建一个 Agent，再开始你的第一段对话。
      </p>

      <a-button
          type="primary"
          :disabled="!agentStore.selectedID"
          @click="createSession"
      >
        开始新对话
      </a-button>
    </div>
  </section>
</template>

<style scoped>
.chat-empty {
  display: grid;

  height: 100%;

  min-height: 0;

  flex: 1;

  place-items: center;

  padding: clamp(36px, 8vw, 104px);
}

.chat-empty__content {
  width: min(620px, 100%);

  transform: translateY(-5vh);
}

.chat-empty__eyebrow {
  margin-bottom: 14px;

  color: var(--h-accent);

  font-family: var(--h-ui);

  font-size: 10px;

  font-weight: 500;

  letter-spacing: 0.12em;
}

.chat-empty h1 {
  max-width: 560px;

  margin: 0;

  color: var(--h-text);

  font-size: clamp(32px, 4.2vw, 54px);

  font-weight: 500;

  letter-spacing: -0.025em;

  line-height: 1.14;
}

.chat-empty p {
  max-width: 530px;

  margin: 20px 0 28px;

  color: var(--h-text-secondary);

  font-size: 14px;

  line-height: 1.8;
}

@media (max-width: 760px) {
  .chat-empty {
    place-items: start;

    padding: 44px 26px;
  }

  .chat-empty__content {
    transform: none;
  }
}
</style>
