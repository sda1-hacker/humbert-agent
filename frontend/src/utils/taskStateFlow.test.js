import test, { beforeEach, mock } from "node:test";
import assert from "node:assert/strict";
import { createPinia, setActivePinia } from "pinia";

let fetchTasks, fetchRuns;
const noop = async () => [];
mock.module("../api/tasks.js", { namedExports: {
    archiveTask: noop, cancelTaskRun: noop, clearTaskRuns: noop, createTask: noop,
    deleteTask: noop, deleteTaskRun: noop, runTaskNow: noop, updateTask: noop,
    setTaskStatus: async (id, status) => ({ id, status }),
    listTasks: (...args) => fetchTasks(...args),
    listTaskRuns: (...args) => fetchRuns(...args),
} });
mock.module("../stores/sessions.js", { namedExports: { useSessionStore: () => ({ forgetSessions: noop }) } });
const { useTaskStore } = await import("../stores/tasks.js");

function deferred() {
    let resolve, reject;
    const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
    return { promise, resolve, reject };
}
beforeEach(() => {
    setActivePinia(createPinia());
    fetchTasks = async () => [];
    fetchRuns = async () => [];
});

test("旧运行记录响应不能把已完成任务改回运行中", async () => {
    const old = deferred(), latest = deferred();
    let calls = 0;
    fetchRuns = () => [old.promise, latest.promise][calls++];
    const store = useTaskStore();
    const first = store.loadRuns("task"), second = store.loadRuns("task");
    latest.resolve([{ id: "run", status: "succeeded" }]); await second;
    old.resolve([{ id: "run", status: "running" }]); await first;
    assert.equal(store.runsByTask.task[0].status, "succeeded");
});

test("旧请求报错不能提前结束新请求的加载状态，也不影响其它任务", async () => {
    const old = deferred(), latest = deferred(), other = deferred();
    let calls = 0;
    fetchRuns = (id) => id === "other" ? other.promise : [old.promise, latest.promise][calls++];
    const store = useTaskStore();
    const first = store.loadRuns("task"), second = store.loadRuns("task"), third = store.loadRuns("other");
    old.reject(new Error("stale error")); await first;
    assert.equal(store.loadingRuns.task, true);
    other.resolve([{ id: "other-run" }]); await third;
    latest.resolve([{ id: "current-run" }]); await second;
    assert.equal(store.runsByTask.task[0].id, "current-run");
    assert.equal(store.runsByTask.other[0].id, "other-run");
    assert.equal(store.loadingRuns.task, false);
});

test("较晚返回的旧任务列表不能覆盖新列表", async () => {
    const old = deferred(), latest = deferred();
    let calls = 0;
    fetchTasks = () => [old.promise, latest.promise][calls++];
    const store = useTaskStore();
    const first = store.load(), second = store.load();
    latest.resolve([{ id: "new" }]); await second;
    old.resolve([{ id: "old" }]); await first;
    assert.equal(store.items[0].id, "new");
    assert.equal(store.selectedID, "new");
});

test("后台刷新期间用户切换任务后保持新选择", async () => {
    const pending = deferred();
    fetchTasks = () => pending.promise;
    const store = useTaskStore();
    store.items = [{ id: "a" }, { id: "b" }]; store.selectedID = "a";
    const refresh = store.refresh();
    await store.select("b");
    pending.resolve(store.items); await refresh;
    assert.equal(store.selectedID, "b");
});

test("清空或删除成功后在途查询不能恢复旧运行记录", async () => {
    for (const action of ["clearRuns", "archive", "remove", "removeRun"]) {
        setActivePinia(createPinia());
        const pending = deferred();
        fetchRuns = () => pending.promise;
        const store = useTaskStore();
        store.runsByTask.task = [{ id: "old" }];
        const load = store.loadRuns("task");
        fetchRuns = async () => [];
        await store[action]("task", "old");
        pending.resolve([{ id: "old" }]); await load;
        assert.deepEqual(store.runsByTask.task ?? [], [], action);
        assert.equal(store.loadingRuns.task, false, action);
    }
});

test("更新任务状态后旧列表响应不能撤销更新", async () => {
    const pending = deferred(); fetchTasks = () => pending.promise;
    const store = useTaskStore(); store.items = [{ id: "task", status: "active" }];
    const load = store.load();
    await store.setStatus("task", "paused");
    pending.resolve([{ id: "task", status: "active" }]); await load;
    assert.equal(store.items[0].status, "paused");
    assert.equal(store.loading, false);
});
