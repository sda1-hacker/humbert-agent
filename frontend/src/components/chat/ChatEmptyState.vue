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
  <section
      class="
      flex
      h-full
      min-h-0
      flex-1
      flex-col
      items-center
      justify-center
      pb-20
    "
  >
    <div
        class="
        grid
        h-20
        w-20
        place-items-center
        rounded-full
        border
        border-[var(--h-border-strong)]
        bg-[var(--h-surface)]
      "
    >
      <span
          class="
          font-serif
          text-[31px]
          text-[var(--h-accent)]
        "
      >
        H
      </span>
    </div>

    <h1
        class="
        mt-6
        text-[20px]
        font-normal
        text-[var(--h-text-secondary)]
      "
    >
      今天想聊点什么？
    </h1>

    <p
        class="
        mt-2
        text-[12px]
        text-[var(--h-text-muted)]
      "
    >
      {{
        agentStore
            .selectedAgent
            ?.name ||
        "Humbert"
      }}
    </p>

    <a-button
        class="mt-6"
        type="secondary"
        :disabled="
        !agentStore.selectedID
      "
        @click="createSession"
    >
      开始新对话
    </a-button>
  </section>
</template>