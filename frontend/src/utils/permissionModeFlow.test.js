import test, { beforeEach, mock } from 'node:test';
import assert from 'node:assert/strict';
import { createPinia, setActivePinia } from 'pinia';

let readMode;
let saveMode;
mock.module(new URL('../api/permissions.js', import.meta.url).href, {
  namedExports: {
    getPermissionMode: () => readMode(),
    setPermissionMode: value => saveMode(value),
  },
});
const { usePermissionStore } = await import('../stores/permissions.js');
beforeEach(() => {
  setActivePinia(createPinia());
  readMode = async () => 'risk';
  saveMode = async value => value;
});

test('审批模式保存失败不改变当前权限显示，成功后所有入口共享结果', async () => {
  const chat = usePermissionStore();
  await chat.load();
  saveMode = async () => { throw new Error('磁盘写入失败'); };
  await assert.rejects(chat.changeMode('full'), /磁盘写入失败/);
  assert.equal(chat.mode, 'risk');
  assert.equal(chat.busy, false);
  saveMode = async value => value;
  await chat.changeMode('always');
  assert.equal(usePermissionStore().mode, 'always');
  usePermissionStore().acceptMode('full');
  assert.equal(chat.mode, 'full');
});

test('设置页已保存的模式不会被聊天区旧读取响应覆盖', async () => {
  let finishRead;
  readMode = () => new Promise(resolve => { finishRead = resolve; });
  const store = usePermissionStore();
  const loading = store.load();
  store.acceptMode('always');
  finishRead('risk');
  await loading;
  assert.equal(store.mode, 'always');
});
