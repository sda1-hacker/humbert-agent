<script setup>
import {
  Message,
} from "@arco-design/web-vue";

import {
  IconCopy,
  IconEdit,
} from "@arco-design/web-vue/es/icon";

import {
  useSessionStore,
} from "../../stores/sessions.js";

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
      allowReuse: {
        type: Boolean,
        default: false,
      },
    });

const sessionStore =
    useSessionStore();

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
