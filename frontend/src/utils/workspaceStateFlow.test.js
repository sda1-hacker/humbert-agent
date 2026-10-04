import test, { beforeEach, mock } from "node:test";
import assert from "node:assert/strict";
import { createPinia, setActivePinia } from "pinia";
import { mkdtemp, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

let fetchOverview, fetchDirectory, fetchPreview;
mock.module("../api/workspace.js", { namedExports: {
    getWorkspaceOverview: (...args) => fetchOverview(...args),
    listWorkspaceDirectory: (...args) => fetchDirectory(...args),
    previewWorkspaceFile: (...args) => fetchPreview(...args),
} });
const { useWorkspaceStore } = await import("../stores/workspace.js");

function deferred() {
    let resolve, reject;
    const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
    return { promise, resolve, reject };
}
beforeEach(() => {
    setActivePinia(createPinia());
    fetchOverview = async () => ({});
    fetchDirectory = async () => ({ entries: [] });
    fetchPreview = async (agent, path) => ({ kind: "text", content: path });
});

test("后台刷新不会把用户刚选的文件切回旧文件", async () => {
    const overview = deferred(), directory = deferred();
    fetchOverview = () => overview.promise; fetchDirectory = () => directory.promise;
    const store = useWorkspaceStore(); store.resetForAgent("agent");
    await store.openPath("a.txt");
    const refresh = store.refresh();
    await store.openPath("b.txt");
    overview.resolve({}); directory.resolve({ entries: [] }); await refresh;
    assert.equal(store.selectedPath, "b.txt");
    assert.equal(store.preview.content, "b.txt");
});

test("刷新预览只更新内容，保留选中文件的元数据", async () => {
    fetchDirectory = async () => ({ entries: [{ path: "file.txt", type: "file", size: 123 }] });
    const store = useWorkspaceStore(); store.resetForAgent("agent");
    await store.selectEntry({ path: "file.txt", type: "file", size: 123 });
    fetchPreview = async () => ({ content: "updated" });
    await store.refresh();
    assert.equal(store.preview.content, "updated");
    assert.equal(store.selectedEntry.size, 123);
});

test("同一文件的旧预览不能覆盖新内容或提前关闭加载状态", async () => {
    const old = deferred(), latest = deferred();
    let calls = 0;
    fetchPreview = () => [old.promise, latest.promise][calls++];
    const store = useWorkspaceStore(); store.resetForAgent("agent");
    const first = store.openPath("a.txt"), second = store.openPath("a.txt");
    old.resolve({ content: "old" }); await first;
    assert.equal(store.loadingPreview, true);
    assert.equal(store.preview, null);
    latest.resolve({ content: "new" }); await second;
    assert.equal(store.preview.content, "new");
    assert.equal(store.loadingPreview, false);
});

test("选中目录会使文件预览失效，旧失败不会影响当前选择", async () => {
    const pending = deferred(); fetchPreview = () => pending.promise;
    const store = useWorkspaceStore(); store.resetForAgent("agent");
    const load = store.openPath("old.txt");
    await store.selectEntry({ path: "src", type: "directory" });
    pending.reject(new Error("old preview error")); await load;
    assert.equal(store.selectedPath, "src");
    assert.equal(store.preview, null);
    assert.equal(store.loadingPreview, false);
});

test("切换 A → B → A 后第一次 A 的请求不能写回新工作区", async () => {
    const overview = deferred(), directory = deferred(), preview = deferred();
    fetchOverview = () => overview.promise;
    fetchDirectory = () => directory.promise;
    fetchPreview = () => preview.promise;
    const store = useWorkspaceStore();
    const oldLoad = store.load("a"), oldPreview = store.openPath("file.txt");
    fetchOverview = async () => ({ rootDir: "new-a" });
    fetchDirectory = async () => ({ entries: [{ path: "new.txt" }] });
    fetchPreview = async () => ({ content: "new" });
    await store.load("b"); await store.load("a"); await store.openPath("file.txt");
    overview.reject(new Error("stale overview error"));
    directory.resolve({ entries: [{ path: "old.txt" }] });
    preview.resolve({ content: "old" });
    await Promise.all([oldLoad, oldPreview]);
    assert.equal(store.overview.rootDir, "new-a");
    assert.equal(store.rootEntries[0].path, "new.txt");
    assert.equal(store.preview.content, "new");
    assert.equal(store.error, "");
});

test("同一路径的新目录响应优先，不妨碍其它目录并行加载", async () => {
    const old = deferred(), latest = deferred(), other = deferred();
    let calls = 0;
    fetchDirectory = (agent, path) => path === "other" ? other.promise : [old.promise, latest.promise][calls++];
    const store = useWorkspaceStore(); store.resetForAgent("agent");
    const first = store.loadDirectory("src", true), second = store.loadDirectory("src", true), third = store.loadDirectory("other", true);
    latest.resolve({ entries: [{ path: "new.txt" }] }); await second;
    other.resolve({ entries: [{ path: "other.txt" }] }); await third;
    old.resolve({ entries: [{ path: "old.txt" }] }); await first;
    assert.equal(store.directories.src.entries[0].path, "new.txt");
    assert.equal(store.directories.other.entries[0].path, "other.txt");
});

test("目录加载期间收起后不会被响应重新展开", async () => {
    const pending = deferred(); fetchDirectory = () => pending.promise;
    const store = useWorkspaceStore(); store.resetForAgent("agent");
    const expand = store.toggleDirectory("src");
    await store.toggleDirectory("src");
    pending.resolve({ entries: [] }); await expand;
    assert.equal(store.expandedPaths.includes("src"), false);
});

test("当前预览失败仍向调用方报告，并结束加载状态", async () => {
    fetchDirectory = async () => ({ entries: [{ path: "missing.txt", type: "file" }] });
    fetchPreview = async () => { throw new Error("current failure"); };
    const store = useWorkspaceStore(); store.resetForAgent("agent");
    await assert.rejects(store.openPath("missing.txt"), /current failure/);
    assert.equal(store.loadingPreview, false);
});

test("外部删除选中文件后同步列表和预览，不再次读取已删除文件", async (t) => {
    const root = await mkdtemp(join(tmpdir(), "humbert-workspace-sync-"));
    t.after(() => rm(root, { recursive: true, force: true }));
    await writeFile(join(root, "aaaa.md"), "temporary file");
    await writeFile(join(root, "aaaa.txt"), "keep this file");
    let previews = 0;
    fetchDirectory = async () => ({ entries: await Promise.all((await readdir(root)).map(async (name) => {
        const info = await stat(join(root, name));
        return { path: name, name, type: "file", size: info.size, modifiedAt: info.mtime.toISOString() };
    })) });
    fetchPreview = async (agent, path) => {
        previews++;
        return { path, content: await readFile(join(root, path), "utf8") };
    };
    const store = useWorkspaceStore();
    await store.load("agent");
    await store.selectEntry(store.rootEntries.find((entry) => entry.path === "aaaa.md"));
    await rm(join(root, "aaaa.md"));
    await store.refresh({ background: true });
    assert.deepEqual(store.rootEntries.map((entry) => entry.path), ["aaaa.txt"]);
    assert.equal(store.selectedPath, "");
    assert.equal(store.selectedEntry, null);
    assert.equal(store.preview, null);
    assert.equal(store.loadingPreview, false);
    assert.equal(store.error, "");
    assert.equal(previews, 1);
});

test("在下次同步前点击已经删除的旧节点会核对目录并清理，不弹预览错误", async () => {
    const store = useWorkspaceStore(); store.resetForAgent("agent");
    store.directories = { ".": { entries: [{ path: "gone.txt", type: "file" }] } };
    fetchPreview = async () => { throw new Error("stat gone.txt: no such file"); };
    await store.selectEntry(store.rootEntries[0]);
    assert.deepEqual(store.rootEntries, []);
    assert.equal(store.selectedEntry, null);
    assert.equal(store.preview, null);
    assert.equal(store.loadingPreview, false);
});

test("整个展开目录删除后清掉子树缓存，迟到预览不能复活选中项", async () => {
    const pending = deferred();
    fetchPreview = () => pending.promise;
    fetchDirectory = async (agent, path) => {
        if (path !== ".") throw new Error("directory removed");
        return { entries: [] };
    };
    const store = useWorkspaceStore(); store.resetForAgent("agent");
    store.directories = { ".": { entries: [{ path: "src", type: "directory" }] }, src: { entries: [] }, "src/nested": { entries: [] } };
    store.expandedPaths = [".", "src", "src/nested"];
    const preview = store.openPath("src/nested/file.txt");
    await store.refresh({ background: true });
    pending.resolve({ content: "deleted contents" }); await preview;
    assert.deepEqual(Object.keys(store.directories), ["."]);
    assert.deepEqual(store.expandedPaths, ["."]);
    assert.equal(store.selectedPath, "");
    assert.equal(store.preview, null);
    assert.equal(store.loadingPreview, false);
    assert.equal(store.error, "");
});

test("删除的尚未缓存目录，其迟到结果也不能重新写入缓存", async () => {
    const pending = deferred();
    fetchDirectory = (agent, path) => path === "src" ? pending.promise : Promise.resolve({ entries: [] });
    const store = useWorkspaceStore(); store.resetForAgent("agent");
    const directory = store.loadDirectory("src", true);
    await store.loadDirectory(".", true);
    pending.resolve({ entries: [{ path: "src/deleted.txt", type: "file" }] }); await directory;
    assert.equal(store.directories.src, undefined);
    assert.equal(store.loadingDirectories.src, undefined);
});

test("目录结果被截断时不能把未列出的文件和目录当成已删除", async () => {
    const store = useWorkspaceStore(); store.resetForAgent("agent");
    store.directories = { src: { entries: [] } };
    store.expandedPaths = [".", "src"];
    await store.openPath("src/file.txt");
    fetchDirectory = async () => ({ entries: [], truncated: true });
    await store.loadDirectory(".", true);
    assert.equal(store.selectedPath, "src/file.txt");
    assert.equal(store.preview.content, "src/file.txt");
    assert.ok(store.directories.src);
    assert.deepEqual(store.expandedPaths, [".", "src"]);
});

test("后台同步不扫描总览或重复读取未变化文件，但外部编辑会更新预览", async () => {
    let info = { path: "file.txt", type: "file", size: 10, modifiedAt: "before" };
    let overviews = 0, previews = 0;
    fetchOverview = async () => { overviews++; return {}; };
    fetchDirectory = async () => ({ entries: [{ ...info }] });
    fetchPreview = async () => { previews++; return { ...info, content: info.modifiedAt }; };
    const store = useWorkspaceStore();
    await store.load("agent"); await store.selectEntry(store.rootEntries[0]);
    await store.refresh({ background: true });
    assert.equal(previews, 1);
    assert.equal(overviews, 1);
    info = { ...info, modifiedAt: "after" };
    await store.refresh({ background: true });
    assert.equal(store.preview.content, "after");
    assert.equal(previews, 2);
    await store.refresh({ background: true });
    assert.equal(previews, 2);
    assert.equal(overviews, 1);
});

test("收起目录再次展开时重新读内容，避免复用外部删除前的缓存", async () => {
    const store = useWorkspaceStore(); store.resetForAgent("agent");
    store.directories = { src: { entries: [{ path: "src/old.txt", type: "file" }] } };
    await store.toggleDirectory("src");
    assert.deepEqual(store.directories.src.entries, []);
});
