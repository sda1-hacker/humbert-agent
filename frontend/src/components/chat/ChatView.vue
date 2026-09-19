<script setup>
import {
  useSessionStore,
} from "../../stores/sessions.js";

import ChatEmptyState
  from "./ChatEmptyState.vue";

import ComposerBar
  from "./ComposerBar.vue";

import MessageList
  from "./MessageList.vue";

const emit = defineEmits([
  "open-workspace-file",
]);

const sessionStore =
    useSessionStore();
</script>

<template>
  <!--
    ChatView 使用标准 Flex Column，而不是 Grid。

    这里非常关键：

      MessageList
          flex: 1
          min-height: 0

      ComposerBar
          flex-shrink: 0

    这样无论消息有多少条，Composer 都不会被内容顶出窗口。
    MessageList 只会占用剩余空间，并在内部产生滚动。
  -->
  <main class="chat-view">
    <template
        v-if="sessionStore.selectedID"
    >
      <MessageList
          @open-workspace-file="emit('open-workspace-file', $event)"
      />

      <ComposerBar/>
    </template>

    <ChatEmptyState
        v-else
        class="chat-view__empty"
    />
  </main>
</template>

<style scoped>
.chat-view {
  display: flex;

  width: 100%;
  height: 100%;

  min-width: 0;
  min-height: 0;

  flex-direction: column;

  overflow: hidden;

  background: var(--h-bg);
}

.chat-view__empty {
  flex: 1;

  min-height: 0;
}
</style>