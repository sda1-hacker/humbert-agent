import { defineStore } from 'pinia';
import { ref } from 'vue';
import { getPermissionMode, setPermissionMode } from '../api/permissions.js';

// 聊天区与设置页共享已保存模式，界面只在后端保存成功后展示新权限。
export const usePermissionStore = defineStore('permissions', () => {
  const mode = ref('');
  const busy = ref(false);
  let revision = 0;
  let loading = null;

  function acceptMode(value) {
    revision += 1;
    mode.value = value;
  }

  async function load() {
    if (mode.value) return;
    if (loading) return loading;
    const current = revision;
    loading = getPermissionMode().then(value => {
      // 设置页保存期间，旧读取不能把模式回滚。
      if (current === revision) acceptMode(value);
    }).finally(() => { loading = null; });
    return loading;
  }

  async function changeMode(value) {
    if (busy.value || value === mode.value) return;
    busy.value = true;
    try { acceptMode(await setPermissionMode(value)); }
    finally { busy.value = false; }
  }

  return { mode, busy, load, changeMode, acceptMode };
});
