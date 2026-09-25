<script setup>
import { ref } from "vue";
import { Message } from "../../utils/uiMessage.js";
import { supportedLanguages } from "../../i18n/index.js";
import { usePreferenceStore } from "../../stores/preferences.js";
import { t } from "../../i18n/index.js";

const preferences = usePreferenceStore();
const saving = ref(false);

async function choose(value) {
  if (saving.value || preferences.user.language === value) return;
  saving.value = true;
  try {
    await preferences.updateLanguage(value);
    Message.success(t("语言已更新"));
  } catch (error) {
    Message.error(error?.message || t("保存语言失败"));
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <section class="language-settings">
    <p>选择 Humbert 的界面语言。切换后立即生效，并会在下次启动时保留。</p>
    <div class="language-options" role="group" aria-label="界面语言">
      <button
          v-for="option in supportedLanguages"
          :key="option.value"
          type="button"
          class="language-option"
          :class="{ 'language-option--active': preferences.user.language === option.value }"
          :aria-pressed="preferences.user.language === option.value"
          :disabled="saving"
          @click="choose(option.value)"
      >
        <span>{{ option.label }}</span>
        <span class="language-option__code">{{ option.value }}</span>
        <span v-if="preferences.user.language === option.value" class="language-option__check" aria-hidden="true">✓</span>
      </button>
    </div>
    <p class="language-settings__hint">Agent 的最终回答默认使用所选语言；用户在对话中明确指定其他语言时，以用户要求为准。模型返回的原始思考内容保留原文。</p>
  </section>
</template>

<style scoped>
.language-settings { max-width: 680px; color: var(--h-text-secondary); font-size: 13px; line-height: 1.65; }
.language-settings p { margin: 0 0 18px; }
.language-options { display: grid; gap: 8px; }
.language-option { display: flex; width: 100%; align-items: center; gap: 12px; padding: 12px 14px; border: 1px solid var(--h-border); border-radius: var(--h-radius-md); background: var(--h-surface); color: var(--h-text); cursor: pointer; text-align: left; font: inherit; }
.language-option:hover:not(:disabled) { background: var(--h-surface-hover); }
.language-option--active { border-color: var(--h-accent-border); background: var(--h-accent-soft); }
.language-option:disabled { cursor: wait; }
.language-option__code { margin-left: auto; color: var(--h-text-muted); font: 11px var(--h-mono); }
.language-option__check { color: var(--h-accent); }
.language-settings__hint { margin-top: 20px !important; color: var(--h-text-muted); font-size: 12px; }
</style>
