<script setup>
import { computed, ref } from "vue";
import { IconPlus } from "@arco-design/web-vue/es/icon";
import { Message } from "../../utils/uiMessage.js";
import { useAgentStore } from "../../stores/agents.js";
import { useSessionStore } from "../../stores/sessions.js";
import IdentityAvatar from "../ui/IdentityAvatar.vue";
import { DEFAULT_AGENT_AVATAR } from "../../utils/avatar.js";

const agentStore = useAgentStore();
const sessionStore = useSessionStore();
const agent = computed(() => agentStore.selectedAgent);
const creating = ref(false);

async function createSession() {
  if (creating.value || !agent.value || sessionStore.agentID !== agent.value.id) return;
  creating.value = true;
  try {
    await sessionStore.createForAgent(agent.value.id);
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    creating.value = false;
  }
}
</script>

<template>
  <section class="chat-empty">
    <div class="chat-empty__content">
      <IdentityAvatar v-if="agent" class="chat-empty__avatar" :src="agent.avatar || DEFAULT_AGENT_AVATAR" :name="agent.name" :size="48"/>
      <h1>{{ agent?.name || $t('今天想从哪里开始？') }}</h1>
      <p v-if="agent">开启一段新对话，把你想做的事交给我。</p>
      <p v-else>先在左侧创建一个 Agent，再开始你的第一段对话。</p>
      <span v-if="agent?.modelDisplayName" class="chat-empty__model">{{ agent.modelDisplayName }}</span>
      <a-button class="chat-empty__start" type="primary" :loading="creating"
          :disabled="!agent || sessionStore.agentID !== agent.id" @click="createSession">
        <template #icon><IconPlus/></template>
        开始新对话
      </a-button>
    </div>
  </section>
</template>

<style scoped>
.chat-empty { display: grid; flex: 1; width: 100%; height: 100%; min-width: 0; min-height: 0; place-items: center; overflow: auto; padding: 40px 24px; }
.chat-empty__content { display: flex; width: min(480px, 100%); flex-direction: column; align-items: center; text-align: center; padding-bottom: clamp(0px, 8vh, 64px); }
.chat-empty__avatar { margin-bottom: 20px; }
.chat-empty h1 { width: 100%; margin: 0; color: var(--h-text); font: 500 clamp(26px, 3vw, 32px)/1.35 var(--h-ui); overflow-wrap: anywhere; text-wrap: balance; }
.chat-empty p { max-width: 360px; margin: 12px 0 0; color: var(--h-text-secondary); font: 14px/1.8 var(--h-ui); text-wrap: balance; }
.chat-empty__model { max-width: 100%; margin-top: 12px; color: var(--h-text-muted); font: 12px/1.5 var(--h-ui); overflow-wrap: anywhere; }
.chat-empty__start { height: 36px; margin-top: 28px; padding: 0 18px; }
@media (max-height: 480px) {
  .chat-empty { place-items: start center; padding: 28px 20px; }
  .chat-empty__content { padding-bottom: 0; }
}
</style>
