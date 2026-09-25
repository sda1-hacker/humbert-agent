<script setup>
import { Message } from "../../utils/uiMessage.js";

import {
  IconCopy,
  IconEdit,
} from "@arco-design/web-vue/es/icon";

import {
  useSessionStore,
} from "../../stores/sessions.js";
import { addPersonalMemoryFromMessage } from "../../api/preferences.js";
import { ref } from "vue";

const props =
    defineProps({
      content: {
        type: String,
        default: "",
      },
      sessionID: {
        type: String,
        default: "",
      },
      messageId: { type: String, default: "" },
      allowReuse: {
        type: Boolean,
        default: false,
      },
    });

const sessionStore =
    useSessionStore();
const memoryVisible = ref(false);
const memoryText = ref("");

function quoteContent() {
  if (!props.sessionID) return;
  const selected = window.getSelection()?.toString().trim() || "";
  const quote = selected && props.content.includes(selected) ? selected : props.content;
  const existing = sessionStore.draftForSession(props.sessionID);
  sessionStore.setDraft(props.sessionID, `${existing ? existing + '\n\n' : ''}> ${quote.replaceAll('\n', '\n> ')}\n\n`);
  document.querySelector('.composer-textarea textarea')?.focus();
}

function proposeMemory() {
  const selected = window.getSelection()?.toString().trim() || "";
  memoryText.value = (selected && props.content.includes(selected) ? selected : props.content).slice(0, 300);
  memoryVisible.value = true;
}

async function saveMemory() {
  const value = memoryText.value.trim();
  if (!value) { Message.warning('请填写要保存的记忆'); return false; }
  try { await addPersonalMemoryFromMessage(value, props.sessionID, props.messageId); Message.success('已保存为跨会话个人记忆'); return true; }
  catch (error) { Message.error(error?.message ?? String(error)); return false; }
}

async function copyContent() {
  if (!props.content) {
    return;
  }
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(
          props.content,
      );
    } else {
      let textarea = null;
      try {
        textarea = document.createElement(
            "textarea",
        );
        textarea.value = props.content;
        textarea.style.position = "fixed";
        textarea.style.opacity = "0";
        document.body.appendChild(textarea);
        textarea.select();
        if (!document.execCommand("copy")) {
          throw new Error("浏览器拒绝复制");
        }
      } finally {
        textarea?.remove();
      }
    }
    Message.success("已复制");
  } catch {
    Message.error("复制失败，请手动选择文本");
  }
}

function reuseContent() {
  if (
      !props.allowReuse ||
      !props.sessionID ||
      !props.content
  ) {
    return;
  }
  sessionStore.setDraft(
      props.sessionID,
      props.content,
  );
  requestAnimationFrame(() => {
    document
        .querySelector(
            ".composer-textarea textarea",
        )
        ?.focus();
  });
  Message.success("已填入输入框");
}
</script>

<template>
  <div
      v-if="content"
      class="message-actions"
  >
    <button
        type="button"
        class="message-action"
        title="复制消息"
        @click="copyContent"
    >
      <IconCopy />
      <span>复制</span>
    </button>

    <button
        v-if="allowReuse"
        type="button"
        class="message-action"
        title="把这条消息填回输入框"
        @click="reuseContent"
    >
      <IconEdit />
      <span>再次编辑</span>
    </button>
    <button type="button" class="message-action" title="引用选中文本或整条消息" @click="quoteContent">引用</button>
    <button type="button" class="message-action" title="确认保存到跨会话个人记忆" @click="proposeMemory">记住</button>
    <a-modal v-model:visible="memoryVisible" title="保存个人记忆" :on-before-ok="saveMemory">
      <p>请确认或修改要长期记住的内容。以后可在设置中编辑或删除。</p>
      <a-textarea v-model="memoryText" :max-length="300" :auto-size="{minRows: 2, maxRows: 5}" />
    </a-modal>
  </div>
</template>

<style scoped>
.message-actions {
  display: flex;
  gap: 3px;
  margin-top: 5px;
  opacity: 0.62;
}

.message-action {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 5px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--h-text-secondary);
  cursor: pointer;
  font: inherit;
  font-size: 10px;
  line-height: 1.4;
}

.message-action:hover {
  background: var(--h-surface-hover);
  color: var(--h-text);
}
</style>
