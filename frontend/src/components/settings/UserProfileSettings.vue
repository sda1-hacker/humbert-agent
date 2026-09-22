<script setup>
import { onMounted, reactive, ref } from "vue";
import { Message } from "@arco-design/web-vue";
import { usePreferenceStore } from "../../stores/preferences.js";
import IdentityAvatar from "../ui/IdentityAvatar.vue";
import { addPersonalMemory, deletePersonalMemory, listPersonalMemories, updatePersonalMemory } from "../../api/preferences.js";
import { confirmAction } from "../../utils/confirm.js";

const MAX_AVATAR_BYTES = 2 * 1024 * 1024;
const AVATAR_TYPES = new Set(["image/png", "image/jpeg", "image/gif", "image/webp"]);

const preferenceStore = usePreferenceStore();
const fileInput = ref(null);
const saving = ref(false);
const form = reactive({ name: "你", avatar: "" });
const memories = ref([]);
const newMemory = ref("");
const editingMemoryID = ref("");
const editingText = ref("");
const memoryBusy = ref(false);

async function refreshMemories() {
  memories.value = await listPersonalMemories() || [];
}

async function addMemory() {
  if (!newMemory.value.trim()) return;
  memoryBusy.value = true;
  try {
    await addPersonalMemory(newMemory.value);
    newMemory.value = "";
    await refreshMemories();
  } catch (error) { Message.error(error?.message || String(error)); }
  finally { memoryBusy.value = false; }
}

async function saveMemory(id) {
  memoryBusy.value = true;
  try {
    await updatePersonalMemory(id, editingText.value);
    editingMemoryID.value = "";
    await refreshMemories();
  } catch (error) { Message.error(error?.message || String(error)); }
  finally { memoryBusy.value = false; }
}

async function forgetMemory(id) {
  const confirmed = await confirmAction({ title: "遗忘这条记忆", message: "删除后，新对话将不再引用这条个人记忆。" });
  if (!confirmed) return;
  memoryBusy.value = true;
  try {
    await deletePersonalMemory(id);
    await refreshMemories();
  } catch (error) { Message.error(error?.message || String(error)); }
  finally { memoryBusy.value = false; }
}

function syncForm() {
  form.name = preferenceStore.user?.name || "你";
  form.avatar = preferenceStore.user?.avatar || "";
}

function fileToDataURL(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(reader.error || new Error("读取头像失败"));
    reader.onload = () => resolve(String(reader.result || ""));
    reader.readAsDataURL(file);
  });
}

async function chooseAvatar(event) {
  const input = event?.target;
  const file = input?.files?.[0];
  if (input) input.value = "";
  if (!file) return;
  if (!AVATAR_TYPES.has(String(file.type || "").toLowerCase())) {
    Message.warning("头像仅支持 PNG、JPEG、GIF 或 WebP");
    return;
  }
  if (file.size <= 0 || file.size > MAX_AVATAR_BYTES) {
    Message.warning("头像图片不能超过 2 MiB");
    return;
  }
  try {
    form.avatar = await fileToDataURL(file);
  } catch (error) {
    Message.error(error?.message || String(error));
  }
}

async function save() {
  const name = form.name.trim();
  if (!name) {
    Message.warning("请输入用户名称");
    return;
  }
  saving.value = true;
  try {
    await preferenceStore.updateUser({ name, avatar: form.avatar });
    syncForm();
    Message.success("个人资料已保存");
  } catch (error) {
    Message.error(error?.message || String(error));
  } finally {
    saving.value = false;
  }
}

onMounted(async () => {
  if (!preferenceStore.user?.name || preferenceStore.user.name === "你") {
    try { await preferenceStore.load(); } catch (error) { Message.error(error?.message || String(error)); }
  }
  syncForm();
  try { await refreshMemories(); } catch (error) { Message.error(error?.message || String(error)); }
});
</script>

<template>
  <div class="profile-settings">
    <section class="profile-card">
      <div class="profile-preview">
        <IdentityAvatar :src="form.avatar" :name="form.name" :size="72" />
        <div>
          <strong>{{ form.name.trim() || "你的名称" }}</strong>
          <span>该身份会显示在聊天消息中。</span>
        </div>
      </div>

      <input ref="fileInput" class="profile-file-input" type="file" accept="image/png,image/jpeg,image/gif,image/webp" @change="chooseAvatar" />
      <div class="profile-avatar-actions">
        <a-button @click="fileInput?.click()">选择头像</a-button>
        <a-button v-if="form.avatar" type="text" status="danger" @click="form.avatar = ''">移除头像</a-button>
        <span>PNG / JPEG / GIF / WebP，最大 2 MiB</span>
      </div>

      <a-form :model="form" layout="vertical">
        <a-form-item label="用户名称">
          <a-input v-model="form.name" maxlength="100" placeholder="例如：Alice" />
        </a-form-item>
      </a-form>

      <div class="profile-footer">
        <a-button type="primary" :loading="saving" @click="save">保存个人资料</a-button>
      </div>
    </section>
    <section class="profile-card personal-memory-card">
      <h2>跨会话个人记忆</h2>
      <p>仅保存你明确填写的事实，所有 Agent 的新对话都可以参考。记忆可能过时，你可以随时修订或遗忘；请勿在此保存密码或密钥。</p>
      <div v-for="item in memories" :key="item.id" class="personal-memory-item">
        <template v-if="editingMemoryID === item.id">
          <a-textarea v-model="editingText" :max-length="300" />
          <a-button size="small" type="primary" :loading="memoryBusy" @click="saveMemory(item.id)">保存</a-button>
          <a-button size="small" @click="editingMemoryID = ''">取消</a-button>
        </template>
        <template v-else>
          <span>{{ item.text }}</span>
          <a-button size="small" @click="editingMemoryID = item.id; editingText = item.text">修订</a-button>
          <a-button size="small" status="danger" :loading="memoryBusy" @click="forgetMemory(item.id)">遗忘</a-button>
        </template>
      </div>
      <div class="personal-memory-add">
        <a-textarea v-model="newMemory" :max-length="300" placeholder="例如：我更喜欢简洁的中文回答" />
        <a-button type="primary" :loading="memoryBusy" @click="addMemory">保存记忆</a-button>
      </div>
      <small>最多 20 条，每条不超过 300 字。</small>
    </section>
  </div>
</template>

<style scoped>
.profile-settings { max-width: 720px; }
.profile-card { padding: 20px; border: 1px solid var(--h-border); border-radius: 12px; background: var(--h-surface); }
.profile-preview { display: flex; align-items: center; gap: 16px; margin-bottom: 16px; }
.profile-preview > div { display: grid; gap: 5px; }
.profile-preview strong { color: var(--h-text); font-size: 17px; }
.profile-preview span, .profile-avatar-actions > span { color: var(--h-text-muted); font-size: 11px; }
.profile-file-input { display: none; }
.profile-avatar-actions { display: flex; align-items: center; gap: 9px; margin-bottom: 18px; }
.profile-avatar-actions > span { margin-left: 4px; }
.profile-footer { display: flex; justify-content: flex-end; padding-top: 4px; }
.personal-memory-card { margin-top: 16px; }
.personal-memory-card h2 { margin: 0 0 8px; font-size: 15px; }
.personal-memory-card p, .personal-memory-card small { color: var(--h-text-muted); font-size: 12px; }
.personal-memory-item { display: flex; align-items: center; gap: 8px; padding: 9px 0; border-bottom: 1px solid var(--h-border); }
.personal-memory-item span { flex: 1; overflow-wrap: anywhere; }
.personal-memory-add { display: grid; gap: 8px; margin-top: 14px; }
</style>
