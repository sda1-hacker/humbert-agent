import test, { beforeEach, mock } from "node:test";
import assert from "node:assert/strict";
import { createPinia, setActivePinia } from "pinia";

let fetchAgents, saveModel, saveAgent, create, remove;
mock.module("../api/agents.js", { namedExports: {
    listAgents: (...args) => fetchAgents(...args),
    setAgentModel: (...args) => saveModel(...args),
    updateAgent: (...args) => saveAgent(...args),
    createAgent: (...args) => create(...args),
    deleteAgent: (...args) => remove(...args),
} });
const { useAgentStore } = await import("../stores/agents.js");

function deferred() {
    let resolve, reject;
    const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
    return { promise, resolve, reject };
}
beforeEach(() => {
    setActivePinia(createPinia());
    fetchAgents = async () => [];
    saveModel = async (id, modelID) => ({ id, modelID, modelDisplayName: "New Model" });
    saveAgent = async (id, fields) => ({ id, ...fields });
    create = async () => ({ id: "created" });
    remove = async () => {};
});
function populatedStore() {
    const store = useAgentStore();
    store.items = [{ id: "agent", modelID: "old", name: "Old Name" }];
    store.selectedID = "agent";
    return store;
}

test("Skill 页面提交选择后，旧 Agent 列表不能覆盖引用", async () => {
    const pending = deferred(); fetchAgents = () => pending.promise;
    const store = populatedStore(); const load = store.load();
    store.applySkillSelection("agent", ["skill"]);
    pending.resolve([{id:"agent",enabledSkills:[]}]); await load;
    assert.deepEqual(store.selectedAgent.enabledSkills,["skill"]);
});

test("模型切换保存成功后，旧 Agent 列表不能撤销页面选择", async () => {
    const pending = deferred(); fetchAgents = () => pending.promise;
    const store = populatedStore();
    const load = store.load();
    await store.switchSelectedModel("new");
    assert.equal(store.selectedAgent.modelID, "new");
    pending.resolve([{ id: "agent", modelID: "old" }]); await load;
    assert.equal(store.selectedAgent.modelID, "new");
    assert.equal(store.loading, false);
});

test("更新 Agent 设置后使旧列表失效，同时保留更新协议字段过滤", async () => {
    const pending = deferred(); fetchAgents = () => pending.promise;
    const store = populatedStore();
    const load = store.load();
    await store.update("agent", { name: "New Name", builtinToolsConfigured: true });
    pending.resolve([{ id: "agent", name: "Old Name" }]); await load;
    assert.equal(store.selectedAgent.name, "New Name");
    assert.equal("builtinToolsConfigured" in store.selectedAgent, false);
});

test("乱序列表及旧错误不能覆盖新结果或提前结束新请求的 loading", async () => {
    for (const oldFails of [false, true]) {
        setActivePinia(createPinia());
        const old = deferred(), latest = deferred(); let calls = 0;
        fetchAgents = () => [old.promise, latest.promise][calls++];
        const store = populatedStore();
        const first = store.load(), second = store.load();
        if (oldFails) old.reject(new Error("stale error"));
        else old.resolve([{ id: "stale" }]);
        await first;
        assert.equal(store.loading, true);
        assert.equal(store.selectedID, "agent");
        latest.resolve([{ id: "latest" }]); await second;
        assert.equal(store.selectedID, "latest");
        assert.equal(store.loading, false);
    }
});

test("较晚返回的旧列表不能覆盖已经提交的新列表", async () => {
    const old = deferred(), latest = deferred(); let calls = 0;
    fetchAgents = () => [old.promise, latest.promise][calls++];
    const store = populatedStore(); const first = store.load(), second = store.load();
    latest.resolve([{ id: "new" }]); await second;
    old.resolve([{ id: "old" }]); await first;
    assert.deepEqual(store.items.map((item) => item.id), ["new"]);
    assert.equal(store.selectedID, "new");
});

test("创建和删除后的刷新不接受修改前的列表", async () => {
    for (const action of ["create", "remove"]) {
        setActivePinia(createPinia());
        const old = deferred(), fresh = deferred(), started = deferred(); let calls = 0;
        fetchAgents = () => {
            if (calls++ === 0) return old.promise;
            started.resolve(); return fresh.promise;
        };
        const store = populatedStore(); const oldLoad = store.load();
        const mutation = store[action](action === "create" ? {} : "agent");
        await started.promise;
        old.resolve([{ id: "agent", modelID: "old" }]); await oldLoad;
        assert.equal(store.loading, true);
        if (action === "remove") assert.equal(store.items.some((item) => item.id === "agent"), false);
        fresh.resolve(action === "create" ? [{ id: "created" }] : [{ id: "remaining" }]);
        await mutation;
        assert.equal(store.selectedID, action === "create" ? "created" : "remaining");
        assert.equal(store.loading, false);
    }
});

test("当前请求失败正常报告，保存失败不使合法的列表请求失效", async () => {
    const store = populatedStore();
    fetchAgents = async () => { throw new Error("current failure"); };
    await assert.rejects(store.load(), /current failure/);
    assert.equal(store.loading, false);
    const pending = deferred(); fetchAgents = () => pending.promise;
    const load = store.load();
    saveModel = async () => { throw new Error("save failed"); };
    await assert.rejects(store.switchSelectedModel("new"), /save failed/);
    assert.equal(store.loading, true);
    pending.resolve([{ id: "agent", modelID: "server-model" }]); await load;
    assert.equal(store.selectedAgent.modelID, "server-model");
});
