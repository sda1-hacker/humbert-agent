<script setup>
import { onMounted, reactive, ref } from "vue";
import { Message } from "@arco-design/web-vue";
import { usePreferenceStore } from "../../stores/preferences.js";
import IdentityAvatar from "../ui/IdentityAvatar.vue";

const MAX_AVATAR_BYTES = 2 * 1024 * 1024;
const AVATAR_TYPES = new Set(["image/png", "image/jpeg", "image/gif", "image/webp"]);

const preferenceStore = usePreferenceStore();
const fileInput = ref(null);
const saving = ref(false);
const form = reactive({ name: "你", avatar: "" });

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
</style>
