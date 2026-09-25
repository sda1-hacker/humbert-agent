<script setup>
import {
  ref,
  watch,
} from "vue";

import { Message } from "../../utils/uiMessage.js";

import {
  useSessionStore,
} from "../../stores/sessions.js";

const props =
    defineProps({
      session: {
        type: Object,
        default: null,
      },
    });

const visible =
    defineModel(
        "visible",
        {
          type: Boolean,
          default: false,
        },
    );

const emit =
    defineEmits([
      "renamed",
    ]);

const sessionStore =
    useSessionStore();

const title =
    ref("");

const saving =
    ref(false);

/**
 * 每次打开 Modal 时读取最新 Session 标题。
 *
 * 不缓存旧值，防止连续编辑不同 Session 时出现标题串台。
 */
watch(
    [
      () => visible.value,
      () => props.session?.id,
    ],

    ([opened]) => {
      if (!opened) {
        return;
      }

      title.value =
          props.session?.title ?? "";
    },

    {
      immediate: true,
    },
);

async function save() {
  if (
      !props.session?.id
  ) {
    return;
  }

  const normalized =
      title.value.trim();

  if (!normalized) {
    Message.warning(
        "会话名称不能为空",
    );

    return;
  }

  if (
      normalized ===
      props.session.title
  ) {
    visible.value = false;

    return;
  }

  saving.value = true;

  try {
    await sessionStore.rename(
        props.session.id,
        normalized,
    );

    visible.value = false;

    emit("renamed");

    Message.success(
        "会话名称已修改",
    );
  } catch (error) {
    Message.error(
        error?.message ??
        String(error),
    );
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <a-modal
      v-model:visible="visible"
      title="重命名对话"
      :width="420"
      :mask-closable="!saving"
      :esc-to-close="!saving"
  >
    <a-input
        v-model="title"
        maxlength="200"
        placeholder="请输入对话名称"
        @keydown.enter.prevent="
        save
      "
    />

    <template #footer>
      <a-space>
        <a-button
            :disabled="saving"
            @click="
            visible = false
          "
        >
          取消
        </a-button>

        <a-button
            type="primary"
            :loading="saving"
            @click="save"
        >
          保存
        </a-button>
      </a-space>
    </template>
  </a-modal>
</template>