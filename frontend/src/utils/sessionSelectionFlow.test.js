import test, { beforeEach, mock } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createPinia, setActivePinia } from 'pinia';
import { computed, ref, reactive, nextTick } from 'vue';
import { useMenuTooltip } from './menuTooltip.js';
import { parseSkillCommand, matchingEnabledSkills, insertSkillReference } from './skillCommand.js';

const requests = [];
const messageReads = [];
mock.module('../api/sessions.js', { namedExports: {
    createSession: async () => {}, deleteSession: async () => {},
    renameSession: async () => {}, setSessionArchived: async () => {}, getMessageWindow: async () => {},
    listSessions: agentID => new Promise((resolve, reject) => requests.push({ agentID, resolve, reject })),
    listMessagePage: async sessionID => {
        messageReads.push(sessionID);
        return { messages: [{ id: `message-${sessionID}` }], hasMore: false };
    },
} });
const { useSessionStore } = await import('../stores/sessions.js');

function sessions(agentID) { return [{ id: `session-${agentID}`, agentID }]; }
function initialStore() {
    const store = useSessionStore();
    store.agentID = 'a';
    store.cacheAgentSessions('a', sessions('a'));
    store.selectedID = 'session-a';
    return store;
}
function assertAgentBPage(store) {
    assert.equal(store.agentID, 'b');
    assert.equal(store.selectedID, '');
    assert.equal(store.selectedSession, null);
    assert.deepEqual(messageReads, []);
    assert.deepEqual(store.messages, []);
    assert.equal(store.sessionsForAgent('b')[0].id, 'session-b');
    assert.equal(store.loading, false);
}
beforeEach(() => {
    requests.length = 0;
    messageReads.length = 0;
    setActivePinia(createPinia());
});

test('主视图切换与侧栏预加载共享读取，等待期间立即解除旧会话选择', async () => {
    const store = initialStore();
    const foreground = store.loadForAgent('b');
    assert.equal(store.selectedID, '');
    assert.equal(store.selectedSession, null);
    const background = store.loadAgentSessions('b');
    assert.equal(requests.length, 1);
    requests[0].resolve(sessions('b'));
    await Promise.all([foreground, background]);
    assertAgentBPage(store);
});

test('侧栏先加载时，主视图也复用同一次读取', async () => {
    const store = initialStore();
    const background = store.loadAgentSessions('b');
    const foreground = store.loadForAgent('b');
    assert.equal(requests.length, 1);
    requests[0].resolve(sessions('b'));
    await Promise.all([foreground, background]);
    assertAgentBPage(store);
});

for (const order of ['old-first', 'new-first']) {
    test(`后台强制刷新替换请求后，主视图追随最新结果：${order}`, async () => {
        const store = initialStore();
        const foreground = store.loadForAgent('b');
        const refresh = store.loadAgentSessions('b', { force: true });
        if (order === 'old-first') {
            requests[0].resolve([{ id: 'stale-b' }]);
            // 让旧请求完成，让主视图开始等待刷新，不依赖定时器排序。
            await new Promise(resolve => setImmediate(resolve));
            assert.equal(store.loading, true);
            assert.equal(requests.length, 2);
            requests[1].resolve(sessions('b'));
        } else {
            requests[1].resolve(sessions('b'));
            await refresh;
            requests[0].resolve([{ id: 'stale-b' }]);
        }
        await Promise.all([foreground, refresh]);
        assertAgentBPage(store);
    });
}

test('侧栏点击会话与 AppShell watcher 同时切换时，保留用户明确选择的会话', async () => {
    const store = initialStore();
    const click = (async () => {
        await store.loadForAgent('b');
        await store.select('session-b-2');
    })();
    const watcher = store.loadForAgent('b');
    assert.equal(requests.length, 1);
    requests[0].resolve([...sessions('b'), { id: 'session-b-2', agentID: 'b' }]);
    await Promise.all([click, watcher]);
    assert.equal(store.selectedID, 'session-b-2');
    assert.equal(store.messages[0].id, 'message-session-b-2');
    assert.equal(store.loading, false);
});

test('共享请求失败不恢复旧会话，下次切换可以重新读取', async () => {
    const store = initialStore();
    const foreground = store.loadForAgent('b');
    const background = store.loadAgentSessions('b');
    const failures = Promise.allSettled([foreground, background]);
    requests[0].reject(new Error('读取失败'));
    assert.deepEqual((await failures).map(item => item.status), ['rejected', 'rejected']);
    assert.equal(store.selectedID, '');
    assert.equal(store.loading, false);
    assert.equal(store.loadingAgents.b, undefined);
    const retry = store.loadForAgent('b');
    requests[1].resolve(sessions('b'));
    await retry;
    assertAgentBPage(store);
});

test('快速切换 Agent 和删除 Agent 均使旧选择流程失效，不自动重发读取', async () => {
    const store = initialStore();
    const old = store.loadForAgent('b');
    const current = store.loadForAgent('c');
    requests[1].resolve(sessions('c'));
    await current;
    requests[0].resolve(sessions('b'));
    await old;
    assert.equal(store.agentID, 'c');
    assert.equal(store.selectedID, '');
    assert.deepEqual(messageReads, []);

    const deleted = store.loadForAgent('b');
    store.forgetAgent('b');
    requests[2].resolve(sessions('b'));
    await deleted;
    assert.equal(store.selectedID, '');
    assert.equal(store.agentID, '');
    assert.equal(store.itemsByAgent.b, undefined);
    assert.equal(requests.length, 3);
});

test('发送入口拒绝 Agent 与会话不一致，切换完成后发送到新会话', async () => {
    const store = initialStore();
    const agentStore = reactive({ selectedID: 'a', selectedAgent: { id: 'a', name: 'A', modelID: 'model' } });
    const sends = [];
    // 执行真实 Composer 脚本，保留 canSend 和 send，只替换运行时及 UI 依赖。
    const source = readFileSync(new URL('../components/chat/ComposerBar.vue', import.meta.url), 'utf8')
        .split('<script setup>')[1].split('</script>')[0]
        .replace(/\bimport[\s\S]*?from\s+["'][^"']+["'];?/g, '');
    const bindings = {
        computed, ref, nextTick, useMenuTooltip, watch: () => {},
        useSkillStore: () => ({ items: [] }), t: key => key,
        parseSkillCommand, matchingEnabledSkills, insertSkillReference,
        Message: { warning: error => { throw new Error(error); }, error: error => { throw new Error(error); } },
        useSessionStore: () => store, useAgentStore: () => agentStore, useModelStore: () => ({}),
        useRuntimeStore: () => ({ isSessionRunning: () => false, send: async (...args) => sends.push(args) }),
    };
    const composer = Function(...Object.keys(bindings), `${source}; return {canSend, send, draft};`)(...Object.values(bindings));
    composer.draft.value = '给 A 的草稿';
    assert.equal(composer.canSend.value, true);
    // 模拟 Agent watcher 执行前的间隙；残留 selectedID 也不能发给 A。
    agentStore.selectedID = 'b';
    agentStore.selectedAgent = { id: 'b', name: 'B', modelID: 'model' };
    await composer.send();
    assert.equal(sends.length, 0);
    const switching = store.loadForAgent('b');
    requests[0].resolve(sessions('b'));
    await switching;
    await store.select('session-b');
    composer.draft.value = '给 B 的消息';
    await composer.send();
    assert.deepEqual(sends, [['session-b', '给 B 的消息', []]]);
    assert.equal(store.draftForSession('session-a'), '给 A 的草稿');
});
