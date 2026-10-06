<script setup>
import { computed, nextTick, ref, watch } from "vue";
import { IconArrowUp, IconPlus, IconStop } from "@arco-design/web-vue/es/icon";
import { Message } from "../../utils/uiMessage.js";
import { useAgentStore } from "../../stores/agents.js";
import { useModelStore } from "../../stores/models.js";
import { useRuntimeStore } from "../../stores/runtime.js";
import { useSessionStore } from "../../stores/sessions.js";
import { useSkillStore } from "../../stores/skills.js";
import { t } from "../../i18n/index.js";
import { parseSkillCommand, matchingEnabledSkills, insertSkillReference } from "../../utils/skillCommand.js";
import {
  MAX_ATTACHMENTS, isImageAttachment, readComposerAttachments, validateComposerAttachments,
  attachmentCapabilityError as checkAttachmentCapabilities,
} from "../../utils/composerAttachments.js";
import ImagePreviewDialog from "../ui/ImagePreviewDialog.vue";
import ApprovalModeSelect from "./ApprovalModeSelect.vue";
import SkillComposerInput from "./SkillComposerInput.vue";
import SkillReference from "./SkillReference.vue";
import ComposerContextMenu from "./ComposerContextMenu.vue";

const agentStore = useAgentStore();
const modelStore = useModelStore();
const runtimeStore = useRuntimeStore();
const sessionStore = useSessionStore();
const fileInput = ref(null);
const imagePreview = ref({ visible: false, src: "", name: "" });
const sending = ref(false);
const switchingModel = ref(false);
// 文本草稿归 Session Store；附件原件仅保存在输入区内存，发送成功前不当成会话事实。
const attachmentDrafts = ref({});
// 清空/发送会使此前的读取失效；切换会话不失效，读取完成后仍回到它原来的会话草稿。
const attachmentResets = new Map();
const draft = computed({
  get: () => sessionStore.draftForSession(sessionStore.selectedID),
  set: value => sessionStore.setDraft(sessionStore.selectedID, value),
});
const attachments = computed({
  get: () => attachmentDrafts.value[sessionStore.selectedID] || [],
  set: value => {
    if (sessionStore.selectedID) setAttachmentDraft(sessionStore.selectedID, value);
  },
});
const running = computed(() => runtimeStore.isSessionRunning(sessionStore.selectedID));
const selectedModelID = computed(() => agentStore.selectedAgent?.modelID || "");
const selectedAgentName = computed(() => agentStore.selectedAgent?.name || "Humbert");
const canSend = computed(() =>
  // Agent 切换尚未加载完会话时，不允许使用上一个 Agent 的选中会话发送。
  sessionStore.agentID === agentStore.selectedID && !sessionStore.loading &&
  Boolean(sessionStore.selectedSession) && Boolean(selectedModelID.value) &&
  (draft.value.trim() !== "" || attachments.value.length > 0) && !running.value && !sending.value);

const skillStore = useSkillStore();
const composerInput = ref(null);
const skillCommand = ref(null);
const skillIndex = ref(0);
const skillMenuOpen = computed(() => Boolean(skillCommand.value));
const skillMatches = computed(() => matchingEnabledSkills(skillStore.items, agentStore.selectedAgent?.enabledSkills, skillCommand.value?.query));

function textareaElement() { return composerInput.value?.input; }

// 输入组件先保存 canonical 草稿，再读取包含原子标签的逻辑光标。
// 中文组词期间不打开菜单，只有确认组词后才解析命令与光标位置。
function syncSkillCommand(event) {
  const input = textareaElement();
  if (!input || event?.isComposing || sessionStore.loading || sessionStore.agentID !== agentStore.selectedID) return;
  const command = input.selectionStart === input.selectionEnd ? parseSkillCommand(input.value, input.selectionStart) : null;
  const opening = command && !skillCommand.value;
  skillCommand.value = command;
  skillIndex.value = 0;
  if (opening && !skillStore.loading) void skillStore.load().catch(() => {}); // 错误在菜单内展示并提供重试。
}

async function chooseSkill(skill) {
  const input = textareaElement();
  const command = input && parseSkillCommand(draft.value, input.selectionStart);
  // 菜单打开后 Agent 的配置可能改变，提交选择时再次检查当前范围，不能自动启用技能。
  if (!command || !skillMatches.value.some(item => item.name === skill.name)) return;
  const inserted = insertSkillReference(draft.value, command, skill.name);
  draft.value = inserted.text;
  skillCommand.value = null;
  await nextTick();
  input.focus();
  input.setSelectionRange(inserted.cursor, inserted.cursor);
}

function handleComposerKey(event) {
  if (event.isComposing || event.keyCode === 229) return;
  if (event.key === "Enter" && (event.ctrlKey || event.metaKey || event.altKey)) return;
  if (event.key === "Tab" && event.shiftKey) return;
  if (skillMenuOpen.value && ["ArrowDown", "ArrowUp", "Enter", "Tab", "Escape"].includes(event.key)) {
    if (event.key === "Escape") { event.preventDefault(); skillCommand.value = null; return; }
    if (event.key === "Enter" && event.shiftKey) return;
    if (event.key === "Tab" && (!skillMatches.value.length || skillStore.loading || skillStore.loadError)) return;
    event.preventDefault();
    const count = skillMatches.value.length;
    if (!count || skillStore.loading || skillStore.loadError) return;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      skillIndex.value = (skillIndex.value + (event.key === "ArrowDown" ? 1 : -1) + count) % count;
      void nextTick(() => document.getElementById(`composer-skill-${skillIndex.value}`)?.scrollIntoView({ block: "nearest" }));
    } else void chooseSkill(skillMatches.value[skillIndex.value]);
    return;
  }
  if (event.key === "Enter" && !event.shiftKey && !event.ctrlKey && !event.metaKey && !event.altKey) handleEnter(event);
}

watch(skillMatches, () => { skillIndex.value = 0; });
watch(() => [sessionStore.selectedID, agentStore.selectedID], () => {
  skillCommand.value = null;
  if (agentStore.selectedAgent && !skillStore.loaded && !skillStore.loading) void skillStore.load().catch(() => {});
}, { immediate: true });

/** 模型切换只提交 Agent 配置；上下文菜单监听共享模型变化，负责刷新自己的预算展示。 */
async function switchModel(modelID) {
  if (!modelID || modelID === selectedModelID.value) return;
  const model = modelStore.modelByID(modelID);
  switchingModel.value = true;
  try {
    await agentStore.switchSelectedModel(modelID);
    Message.success(`已切换到 ${model?.displayName || modelID}`);
  } catch (error) {
    Message.error(error?.message || String(error));
  } finally {
    switchingModel.value = false;
  }
}

function attachmentCapabilityError(items) {
  return checkAttachmentCapabilities(items,
    modelStore.modelByID(agentStore.selectedAgent?.modelID || ""),
    modelStore.modelByID(modelStore.multimedia.imageModelID || ""));
}
function formatAttachmentSize(value) {
  const bytes = Number(value || 0);
  if (bytes < 1024) return `${bytes} B`;
  return bytes < 1024 * 1024 ? `${(bytes / 1024).toFixed(1)} KiB` : `${(bytes / 1024 / 1024).toFixed(1)} MiB`;
}
function setAttachmentDraft(sessionID, items) {
  attachmentDrafts.value = { ...attachmentDrafts.value, [sessionID]: items };
}
function openAttachmentPicker() {
  if (sessionStore.selectedID && !running.value && !sending.value) fileInput.value?.click();
}
async function selectAttachments(event) {
  const input = event?.target;
  const files = Array.from(input?.files || []);
  if (input) input.value = "";
  await addAttachments(files);
}

/**
 * 选择与粘贴共用批量读取。先冻结目标 Session，再等待 FileReader，不能在等待后用当前
 * selectedID 写回，否则切到 B 会把 A 的附件放进 B。多批读取合并前再次检查当前总量。
 * 发送清空草稿时增加版本，阻止迟到读取把已经提交的附件恢复回来。
 */
async function addAttachments(files) {
  const sessionID = sessionStore.selectedID;
  if (!sessionID || !files.length || running.value || sending.value) return;
  const existing = attachmentDrafts.value[sessionID] || [];
  if (existing.length + files.length > MAX_ATTACHMENTS) {
    Message.warning(`单条消息最多允许 ${MAX_ATTACHMENTS} 个附件`);
    return;
  }
  const metadata = files.map(file => ({ name: file.name, mimeType: file.type, sizeBytes: file.size }));
  const error = attachmentCapabilityError([...existing, ...metadata]);
  if (error) { Message.warning(error); return; }
  const version = attachmentResets.get(sessionID) || 0;
  try {
    const added = await readComposerAttachments(files, existing);
    if ((attachmentResets.get(sessionID) || 0) !== version) return;
    const current = attachmentDrafts.value[sessionID] || [];
    validateComposerAttachments(added, current);
    setAttachmentDraft(sessionID, [...current, ...added]);
  } catch (error) {
    Message.error(error?.message || String(error));
  }
}

async function pasteAttachments(event) {
  if (!sessionStore.selectedID || running.value || sending.value) return;
  const files = Array.from(event?.clipboardData?.items || [])
    .filter(item => item.kind === "file" && String(item.type || "").toLowerCase().startsWith("image/"))
    .map(item => item.getAsFile()).filter(Boolean);
  if (!files.length) return;
  event.preventDefault();
  await addAttachments(files);
}
function previewDraftImage(attachment) {
  if (isImageAttachment(attachment) && attachment?.base64Data) {
    imagePreview.value = { visible: true, src: `data:${attachment.mimeType || "image/png"};base64,${attachment.base64Data}`, name: attachment.name || "图片预览" };
  }
}
function removeAttachment(index) {
  attachments.value = attachments.value.filter((_, current) => current !== index);
}

/**
 * 发送时冻结文字、附件和 Session。Runtime Store 负责执行和终态重新读取；输入区只处理
 * 草稿清空及启动失败恢复。/skill 尚未完成选择时保留在输入区，不作为普通任务发送。
 */
async function send() {
  if (!canSend.value) return;
  const sessionID = sessionStore.selectedID;
  const content = sessionStore.draftForSession(sessionID).trim();
  if (parseSkillCommand(content)) { syncSkillCommand(); return; }
  const pending = attachments.value.map(item => ({ ...item }));
  const error = attachmentCapabilityError(pending);
  if (error) { Message.warning(error); return; }
  sessionStore.clearDraft(sessionID);
  attachmentResets.set(sessionID, (attachmentResets.get(sessionID) || 0) + 1);
  setAttachmentDraft(sessionID, []);
  sending.value = true;
  try {
    await runtimeStore.send(sessionID, content,
      pending.map(({ name, mimeType, base64Data }) => ({ name, mimeType, base64Data })));
  } catch (error) {
    // 等待期间用户可能写了下一条草稿。恢复失败输入时保留新内容，且始终写回原 Session。
    const current = sessionStore.draftForSession(sessionID);
    sessionStore.setDraft(sessionID, current ? `${content}\n${current}` : content);
    setAttachmentDraft(sessionID, [...pending, ...(attachmentDrafts.value[sessionID] || [])]);
    if (!runtimeStore.terminalError(sessionID)) Message.error(error?.message || String(error));
  } finally {
    sending.value = false;
  }
}
function handleEnter(event) {
  // 中文输入法确认候选词也会产生 Enter，不能把组词操作误当成发送。
  if (event.isComposing || event.keyCode === 229) return;
  event.preventDefault();
  void send();
}
async function stop() {
  try { await runtimeStore.cancel(sessionStore.selectedID); }
  catch (error) { Message.error(error?.message || String(error)); }
}
</script>

<template>
  <!--
    Composer 是 ChatView 的正常第二部分。
    不使用：
      position: absolute
      position: fixed
    因此不会再覆盖或者被 MessageList 推出屏幕。
  -->
  <footer class="composer" @focusout="event => !event.currentTarget.contains(event.relatedTarget) && (skillCommand = null)">
    <div class="composer-inner">
      <!-- 菜单只显示当前 Agent 已启用的元数据；正文和资源仍在后端按需加载。 -->
      <section v-if="skillMenuOpen" class="skill-menu" :aria-label="t('选择技能')">
        <header class="skill-menu__header">
          <span>{{ $t('当前 Agent 的技能') }}</span><span class="skill-menu__count">{{ skillMatches.length }}</span>
          <button type="button" class="skill-menu__close" :aria-label="t('关闭技能菜单')" @click="skillCommand = null">×</button>
        </header>
        <div v-if="skillStore.loading" class="skill-menu__status" role="status">{{ $t('正在加载…') }}</div>
        <div v-else-if="skillStore.loadError" class="skill-menu__status" role="alert">
          <p>{{ $t('无法读取技能，请重试。') }}</p>
          <button type="button" class="skill-menu__retry" @click="skillStore.load().catch(() => {})">{{ $t('重试') }}</button>
        </div>
        <div v-else-if="!skillMatches.length" class="skill-menu__status" role="status">
          {{ skillCommand.query ? $t('没有匹配的技能') : $t('当前 Agent 没有可用技能，请先在技能页面启用。') }}
        </div>
        <div v-else id="composer-skills" class="skill-menu__list" role="listbox" :aria-label="t('选择技能')">
          <button v-for="(skill, index) in skillMatches" :id="`composer-skill-${index}`" :key="skill.name" type="button" role="option" :aria-selected="index === skillIndex" class="skill-menu__item" :class="{ 'skill-menu__item--active': index === skillIndex }" @mousedown.prevent @click="chooseSkill(skill)">
            <span class="skill-menu__identity"><SkillReference :skill="skill" :focusable="false"/></span>
            <span class="skill-menu__description">{{ skill.description }}</span>
          </button>
        </div>
        <footer class="skill-menu__hint">{{ $t('↑↓ 选择 · Enter / Tab 使用 · Esc 关闭') }}</footer>
      </section>
      <input
          ref="fileInput"
          type="file"
          multiple
          class="composer-file-input"
          @change="selectAttachments"
      />
      <div v-if="attachments.length" class="composer-attachments">
        <div
            v-for="(attachment, index) in attachments"
            :key="`${attachment.name}-${attachment.sizeBytes}-${index}`"
            class="composer-attachment"
        >
          <button
              v-if="attachment.mimeType.startsWith('image/')"
              type="button"
              class="composer-attachment__preview"
              :title="`预览 ${attachment.name}`"
              @click="previewDraftImage(attachment)"
          >
            <img :src="`data:${attachment.mimeType};base64,${attachment.base64Data}`" :alt="attachment.name"/>
          </button>
          <span v-else class="composer-attachment__icon">📎</span>
          <span class="composer-attachment__body">
            <span class="composer-attachment__name">{{ attachment.name }}</span>
            <span class="composer-attachment__size">{{ formatAttachmentSize(attachment.sizeBytes) }}</span>
          </span>
          <button
              type="button"
              class="composer-attachment__remove"
              :aria-label="`移除 ${attachment.name}`"
              @click="removeAttachment(index)"
          >×
          </button>
        </div>
      </div>
      <SkillComposerInput
          :key="sessionStore.selectedID"
          ref="composerInput"
          v-model="draft"
          :skills="skillStore.items"
          :disabled="
          !sessionStore.selectedID
        "
          :placeholder="sessionStore.selectedID ? t('给 {name} 发送消息…', { name: selectedAgentName }) : t('请先创建一个对话')"
          :input-attrs="{ 'aria-label': t('消息输入'), 'aria-autocomplete': 'list', 'aria-expanded': skillMenuOpen, 'aria-controls': skillMenuOpen ? 'composer-skills' : undefined, 'aria-activedescendant': skillMenuOpen && !skillStore.loading && !skillStore.loadError && skillMatches.length ? `composer-skill-${skillIndex}` : undefined }"
          @input="(_value, event) => syncSkillCommand(event)"
          @paste="pasteAttachments"
          @keydown="handleComposerKey"
          @keyup="event => ['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key) && syncSkillCommand(event)"
          @click="syncSkillCommand"
          @focus="syncSkillCommand"
          @compositionend="syncSkillCommand"
      />
      <ImagePreviewDialog
          v-model:visible="imagePreview.visible"
          :src="imagePreview.src"
          :name="imagePreview.name"
      />
      <div
          class="composer-toolbar"
      >
        <div class="composer-context-area">
          <button
              type="button"
              class="composer-attach-button"
              :disabled="!sessionStore.selectedID || running || sending || attachments.length >= MAX_ATTACHMENTS"
              :title="t('添加图片或文件')"
              :aria-label="t('添加图片或文件')"
              @click="openAttachmentPicker"
          >
            <IconPlus aria-hidden="true"/>
          </button>
          <!--
            Context 环形进度只展示 ContextEngine 的估算值，不自己重新计算 Token。
            手动操作与自动压缩使用同一个摘要入口。
          -->
          <ComposerContextMenu />
          <ApprovalModeSelect :disabled="running || sending" />
          <span class="composer-hint">{{ $t('输入 /skill 选择技能 · Shift+Enter 换行') }}</span>
        </div>
        <div
            class="composer-actions"
        >
          <!--
            主页面真实 Model Switcher。
            读取共享 Pinia ModelStore，
            所以设置中新建 Model 后会直接出现在这里。
          -->
          <a-select
              :model-value="
              selectedModelID
            "
              :loading="
              modelStore.loading ||
              switchingModel
            "
              :disabled="
              !agentStore.selectedAgent
            "
              allow-search
              placeholder="选择模型"
              aria-label="选择模型"
              size="small"
              class="composer-model"
              @change="
              switchModel
            "
          >
            <a-option
                v-for="
                model in
                modelStore.enabledModels
              "
                :key="model.id"
                :value="model.id"
            >
              {{ model.displayName }}
              ·
              {{ model.providerName }}
            </a-option>
          </a-select>
          <!-- 发送与停止共用同一按钮，运行时切换动作并保留可访问的说明。 -->
          <a-button
              type="primary"
              class="composer-send-button"
              :class="{ 'composer-send-button--stop': running }"
              :status="running ? 'danger' : undefined"
              :loading="sending && !running"
              :disabled="!running && !canSend"
              :aria-label="running ? $t('停止生成') : $t('发送消息')"
              :title="running ? $t('停止生成') : $t('发送消息')"
              @click="running ? stop() : send()"
          >
            <template #icon>
              <IconStop v-if="running"/>
              <IconArrowUp v-else class="composer-send-icon" aria-hidden="true"/>
            </template>
          </a-button>
        </div>
      </div>
    </div>
  </footer>
</template>
<style scoped>
.composer {
  flex: 0 0 auto;
  width: 100%;
  padding: 8px 28px 16px;
  background: var(--h-bg);
}
.composer-inner {
  position: relative;
  /* 按聊天面板的实际宽度响应布局，右侧文件面板展开时也能正确换行。 */
  container-type: inline-size;
  width: 100%;
  max-width: 792px;
  margin: 0 auto;
  padding: 14px 12px 10px;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-lg);
  background: var(--h-surface);
  font-family: var(--h-ui);
  transition: border-color 160ms ease, background-color 160ms ease;
}
.composer-inner:hover {
  border-color: var(--h-border-strong);
}
.composer-inner:focus-within {
  border-color: var(--h-accent-border);
}
/* 技能菜单随输入区宽度布局，不增加页面侧栏；列表单独滚动，保留输入和发送位置。 */
.skill-menu {
  display: flex;
  flex-direction: column;
  position: absolute;
  z-index: 20;
  inset: auto 0 calc(100% + 8px);
  overflow: hidden;
  /* 为最多六行草稿、换行的工具栏和窗口顶栏留出空间，长目录在菜单内滚动。 */
  max-height: min(320px, calc(100dvh - 380px));
  border: 1px solid var(--h-border-strong);
  border-radius: var(--h-radius-md);
  background: var(--h-surface);
  color: var(--h-text);
  font: 13px/1.5 var(--h-ui);
  text-align: left;
}
.skill-menu__header { display: flex; flex-shrink: 0; align-items: center; gap: 8px; padding: 10px 14px; border-bottom: 1px solid var(--h-border); font-weight: 500; }
.skill-menu__count { color: var(--h-text-muted); font-size: 12px; font-variant-numeric: tabular-nums; }
.skill-menu__close { margin-left: auto; width: 28px; height: 28px; border: 0; border-radius: 4px; background: transparent; color: var(--h-text-muted); font-size: 20px; cursor: pointer; }
.skill-menu__close:hover { background: var(--h-surface-hover); }
.skill-menu__list { min-height: 0; max-height: min(280px, 34vh); overflow-y: auto; padding: 4px; }
.skill-menu__item { display: flex; width: 100%; flex-direction: column; gap: 4px; padding: 10px; border: 0; border-radius: 4px; background: transparent; color: inherit; font: inherit; text-align: left; cursor: pointer; }
.skill-menu__item:hover { background: var(--h-surface-hover); }
.skill-menu__item--active { background: var(--h-accent-soft); }
.skill-menu__identity { display: flex; align-items: baseline; flex-wrap: wrap; gap: 4px 12px; overflow-wrap: anywhere; }
.skill-menu__identity strong { font-weight: 500; }
.skill-menu__identity code { color: var(--h-text-muted); font: 12px var(--h-mono); }
.skill-menu__description { display: -webkit-box; overflow: hidden; -webkit-line-clamp: 2; -webkit-box-orient: vertical; color: var(--h-text-secondary); font-size: 12px; overflow-wrap: anywhere; }
.skill-menu__status { overflow-y: auto; padding: 20px 14px; color: var(--h-text-secondary); }
.skill-menu__status p { margin: 0 0 8px; }
.skill-menu__retry { border: 0; background: transparent; color: var(--h-accent); font: inherit; cursor: pointer; }
.skill-menu__hint { flex-shrink: 0; padding: 8px 14px; border-top: 1px solid var(--h-border); color: var(--h-text-muted); font-size: 12px; }
.skill-menu button:focus-visible { outline: 2px solid var(--h-accent); outline-offset: -2px; }
.composer-hint { min-width: 0; margin-left: 8px; color: var(--h-text-muted); font: 11px/1.5 var(--h-ui); overflow-wrap: anywhere; }
.composer-file-input {
  display: none;
}
.composer-attachments {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 0 2px 12px;
}
.composer-attachment {
  display: flex;
  max-width: 260px;
  align-items: center;
  gap: 7px;
  padding: 7px 9px;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-md);
  background: var(--h-bg);
}
.composer-attachment__body {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-direction: column;
}
.composer-attachment__preview {
  width: 38px;
  height: 38px;
  flex: 0 0 38px;
  padding: 0;
  overflow: hidden;
  border: 0;
  border-radius: 5px;
  background: var(--h-surface-soft, var(--h-bg));
  cursor: zoom-in;
}
.composer-attachment__preview img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.composer-attachment__name {
  overflow: hidden;
  color: var(--h-text);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.composer-attachment__size {
  color: var(--h-text-secondary);
  font-size: 12px;
}
.composer-attachment__remove,
.composer-attach-button {
  border: 0;
  background: transparent;
  color: var(--h-text-secondary);
  cursor: pointer;
}
.composer-attachment__remove:hover,
.composer-attach-button:hover:not(:disabled) {
  color: var(--h-accent);
}
.composer-attach-button {
  display: inline-flex;
  width: 32px;
  height: 32px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  padding: 0;
  border-radius: 8px;
  font-family: inherit;
  font-size: 12px;
  white-space: nowrap;
  transition: background 140ms ease, color 140ms ease;
}
.composer-attach-button .arco-icon {
  font-size: 18px;
}
.composer-attach-button:hover:not(:disabled) {
  background: var(--h-surface-hover);
}
.composer-attach-button:disabled {
  cursor: not-allowed;
  opacity: .45;
}
.composer-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px 12px;
  margin-top: 10px;
  padding-top: 0;
  border-top: 0;
}
.composer-context-area {
  display: flex;
  min-width: 0;
  flex: 1 1 0;
  align-items: center;
  gap: 2px;
  flex-wrap: wrap;
}
.composer-attach-button:focus-visible {
  outline: 2px solid var(--h-accent-border);
  outline-offset: 2px;
}
.composer-actions {
  display: flex;
  min-width: 0;
  flex: 0 1 auto;
  margin-left: auto;
  align-items: center;
  gap: 8px;
}
:deep(.composer-model) {
  width: 200px;
  min-width: 0;
  max-width: 100%;
  flex: 0 1 200px;
  font-size: 13px;
}
/* 输入区的辅助选项使用轻量样式，边框与主动作留给整个输入框及发送按钮。 */
:deep(.composer-model.arco-select-view-single) {
  min-height: 32px;
  padding: 0 9px;
  border-color: transparent !important;
  border-radius: 8px;
  background: transparent !important;
  font-family: var(--h-ui);
}
:deep(.composer-model.arco-select-view-single:hover),
:deep(.composer-model.arco-select-view-focus) {
  border-color: transparent !important;
  background: var(--h-surface-hover) !important;
}
:deep(.composer-model .arco-select-view-value) {
  color: var(--h-text-secondary) !important;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
:deep(.composer-model .arco-select-view-input) {
  text-overflow: ellipsis;
}
:deep(.composer-model .arco-select-view-suffix) {
  color: var(--h-text-muted);
}
.composer-send-button {
  width: 32px;
  min-width: 32px;
  height: 32px;
  flex: 0 0 32px;
  padding: 0;
  border-radius: 8px;
  font-family: var(--h-ui);
  font-size: 13px;
  font-weight: 500;
}
.composer-send-icon {
  display: block;
  width: 19px;
  height: 19px;
}
/* 空草稿使用暖灰，不让禁用的发送动作抢过输入内容。 */
.composer-send-button.arco-btn-disabled {
  border-color: transparent !important;
  background: var(--h-surface-active) !important;
  color: var(--h-text-muted) !important;
  opacity: 1;
}
.composer-send-button--stop .arco-icon {
  font-size: 16px;
}
@media (
max-width: 800px
) {
  .composer {
    padding: 8px 16px 14px;
  }
}
@container (max-width: 560px) {
  .composer-toolbar {
    row-gap: 8px;
  }
  .composer-actions {
    flex: 1 1 100%;
    margin-left: 0;
  }
  .composer-context-area {
    flex: 1 1 100%;
  }
  .composer-hint {
    flex: 1 1 150px;
  }
  :deep(.composer-model) {
    width: 0;
    flex: 1;
  }
}
@media (prefers-reduced-motion: reduce) {
  .composer-inner,
  .composer-attach-button {
    transition: none;
  }
}
</style>
