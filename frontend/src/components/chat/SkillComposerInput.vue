<script setup>
import { createVNode, getCurrentInstance, onBeforeUnmount, onMounted, ref, render, watch } from 'vue';
import { splitSkillReferences } from '../../utils/skillCommand.js';
import { readEditorText, readEditorSelection, setEditorSelection } from '../../utils/skillEditor.js';
import SkillReference from './SkillReference.vue';

const props = defineProps({
  modelValue: { type: String, default: '' },
  skills: { type: Array, default: () => [] },
  disabled: Boolean,
  placeholder: { type: String, default: '' },
  inputAttrs: { type: Object, default: () => ({}) },
});
const emit = defineEmits(['update:modelValue', 'input', 'paste', 'keydown', 'keyup', 'click', 'focus', 'compositionend']);
const editor = ref(null);
const appContext = getCurrentInstance().appContext;
let composing = false;
let selection = { start: 0, end: 0 };
let chipRoots = [];
let history = [];
let historyIndex = -1;

function captureSelection() {
  if (editor.value) selection = readEditorSelection(editor.value, selection);
  return selection;
}

function clearChips() {
  for (const root of chipRoots) render(null, root);
  chipRoots = [];
}

function paint(text, restore = false) {
  if (!editor.value) return;
  const scrollTop = editor.value.scrollTop;
  const document = editor.value.ownerDocument;
  const fragment = document.createDocumentFragment();
  clearChips();
  for (const part of splitSkillReferences(text, props.skills)) {
    if (part.type === 'text') fragment.append(document.createTextNode(part.text));
    else {
      const chip = document.createElement('span');
      chip.setAttribute('data-skill-reference', part.name);
      chip.setAttribute('contenteditable', 'false');
      const vnode = createVNode(SkillReference, { skill: part.skill, editable: true });
      vnode.appContext = appContext;
      render(vnode, chip);
      fragment.append(chip);
      chipRoots.push(chip);
    }
  }
  // 一个真实的末尾文本节点让光标能落在原子标签之后。
  fragment.append(document.createTextNode(''));
  if (text.endsWith('\n')) {
    const end = document.createElement('br');
    end.setAttribute('data-editor-end', '');
    fragment.append(end);
  }
  editor.value.replaceChildren(fragment);
  editor.value.scrollTop = scrollTop;
  if (restore) {
    setEditorSelection(editor.value, selection.start, selection.end);
    const range = document.getSelection()?.getRangeAt(0);
    const caret = range?.getBoundingClientRect();
    const bounds = editor.value.getBoundingClientRect();
    if (caret?.height) {
      if (caret.bottom > bounds.bottom - 12) editor.value.scrollTop += caret.bottom - bounds.bottom + 12;
      else if (caret.top < bounds.top + 2) editor.value.scrollTop -= bounds.top + 2 - caret.top;
    }
  }
}

function remember(text) {
  const previous = history[historyIndex];
  if (previous?.text === text) { Object.assign(previous, selection); return; }
  history = history.slice(0, historyIndex + 1);
  history.push({ text, ...selection });
  if (history.length > 100) history.shift();
  historyIndex = history.length - 1;
}

function publish(text, event) {
  emit('update:modelValue', text);
  emit('input', text, event);
}

function onInput(event) {
  captureSelection();
  const text = readEditorText(editor.value);
  if (!composing) { paint(text, true); remember(text); }
  publish(text, event);
}

function replaceSelection(text, event, range = captureSelection()) {
  if (props.disabled) return;
  const before = readEditorText(editor.value);
  if (history[historyIndex]) Object.assign(history[historyIndex], range);
  const next = before.slice(0, range.start) + text + before.slice(range.end);
  selection = { start: range.start + text.length, end: range.start + text.length };
  paint(next, true);
  remember(next);
  publish(next, event);
}

function undo(redo = false) {
  const next = historyIndex + (redo ? 1 : -1);
  if (next < 0 || next >= history.length) return;
  historyIndex = next;
  const entry = history[next];
  selection = { start: entry.start, end: entry.end };
  paint(entry.text, true);
  publish(entry.text);
}

function deleteChip(backward, event) {
  const range = captureSelection();
  if (range.start !== range.end) return false;
  const chip = splitSkillReferences(readEditorText(editor.value), props.skills).find(part => part.type === 'skill' && (backward ? part.end === range.start : part.start === range.start));
  if (!chip) return false;
  event.preventDefault();
  replaceSelection('', event, { start: chip.start, end: chip.end });
  return true;
}

function onKeydown(event) {
  captureSelection();
  if (history[historyIndex]) Object.assign(history[historyIndex], selection);
  if (composing || event.isComposing || event.keyCode === 229) return;
  const command = event.ctrlKey || event.metaKey;
  if (command && !event.altKey && ['z', 'y'].includes(event.key.toLowerCase())) {
    event.preventDefault();
    undo(event.shiftKey || event.key.toLowerCase() === 'y');
    return;
  }
  emit('keydown', event);
  if (event.defaultPrevented) return;
  if (event.key === 'Enter') { event.preventDefault(); replaceSelection('\n', event); }
  else if (event.key === 'Backspace' || event.key === 'Delete') deleteChip(event.key === 'Backspace', event);
}

function onBeforeInput(event) {
  if (composing || event.isComposing) return;
  if (event.inputType === 'historyUndo' || event.inputType === 'historyRedo') {
    event.preventDefault(); undo(event.inputType === 'historyRedo');
  } else if (event.inputType === 'deleteContentBackward' || event.inputType === 'deleteContentForward') deleteChip(event.inputType === 'deleteContentBackward', event);
}

function onPaste(event) {
  emit('paste', event); // 图片仍交给现有附件入口处理。
  if (event.defaultPrevented) return;
  event.preventDefault();
  replaceSelection((event.clipboardData?.getData('text/plain') || '').replace(/\r\n?/g, '\n'), event);
}

function onCopy(event, cut = false) {
  const range = captureSelection();
  if (range.start === range.end || !event.clipboardData) return;
  event.preventDefault();
  event.clipboardData.setData('text/plain', readEditorText(editor.value).slice(range.start, range.end));
  if (cut) replaceSelection('', event, range);
}

function onCompositionEnd(event) {
  composing = false;
  onInput(event);
  emit('compositionend', event);
}

function onDrop(event) {
  event.preventDefault();
  const text = event.dataTransfer?.getData('text/plain');
  if (text) replaceSelection(text, event);
}

const input = {
  get value() { return editor.value ? readEditorText(editor.value) : props.modelValue; },
  get selectionStart() { return captureSelection().start; },
  get selectionEnd() { return captureSelection().end; },
  focus() { editor.value?.focus(); },
  setSelectionRange(start, end = start) {
    selection = { start, end };
    if (editor.value) setEditorSelection(editor.value, start, end);
    if (history[historyIndex]) Object.assign(history[historyIndex], selection);
  },
};
defineExpose({ input });

onMounted(() => { paint(props.modelValue); remember(props.modelValue); });
watch(() => props.modelValue, () => {
  if (!editor.value || composing) return;
  const focused = editor.value.ownerDocument.activeElement === editor.value;
  if (focused) captureSelection();
  if (props.modelValue === readEditorText(editor.value)) return;
  paint(props.modelValue, focused);
  if (!props.modelValue) { selection = { start: 0, end: 0 }; history = []; historyIndex = -1; }
  remember(props.modelValue);
});
watch(() => props.skills, () => {
  if (!editor.value || composing) return;
  const focused = editor.value.ownerDocument.activeElement === editor.value;
  if (focused) captureSelection();
  paint(props.modelValue, focused);
}, { deep: true });
onBeforeUnmount(clearChips);
</script>

<template>
  <div class="composer-textarea">
    <div ref="editor" class="skill-editor" role="textbox" aria-multiline="true" :aria-disabled="disabled" :contenteditable="disabled ? 'false' : 'true'" :tabindex="disabled ? -1 : 0" :data-placeholder="placeholder" :data-empty="!modelValue" v-bind="inputAttrs"
      @input="onInput" @beforeinput="onBeforeInput" @keydown="onKeydown" @keyup="event => { captureSelection(); emit('keyup', event); }"
      @click="event => { captureSelection(); emit('click', event); }" @focus="event => emit('focus', event)"
      @compositionstart="composing = true" @compositionend="onCompositionEnd" @paste="onPaste" @copy="onCopy" @cut="event => onCopy(event, true)" @drop="onDrop"/>
  </div>
</template>

<style scoped>
/* 标签和普通文本共享固定输入空间，长草稿仅在内部滚动。 */
.skill-editor { height: 88px; overflow-y: auto; padding: 2px 4px 12px; outline: none; background: transparent; color: var(--h-text); font: 15px/1.65 var(--h-ui); letter-spacing: 0; white-space: pre-wrap; overflow-wrap: anywhere; cursor: text; }
.skill-editor[data-empty="true"]::before { content: attr(data-placeholder); color: var(--h-text-muted); pointer-events: none; }
.skill-editor[aria-disabled="true"] { cursor: not-allowed; opacity: .6; }
</style>
