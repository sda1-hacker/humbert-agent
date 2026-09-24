import test from "node:test";
import assert from "node:assert/strict";
import { pollIndexedSearch } from "./searchPolling.js";

test("索引更新结束后返回最新结果", async () => {
    const snapshots = [
        { results: [{ id: "old" }], updating: true },
        { results: [{ id: "new" }], updating: false },
    ];
    const seen = [];
    await pollIndexedSearch(
        async () => snapshots.shift(),
        () => true,
        (results) => seen.push(results[0].id),
        async () => {},
    );
    assert.deepEqual(seen, ["old", "new"]);
});

test("用户切换搜索词后丢弃旧请求结果", async () => {
    let current = true;
    const seen = [];
    await pollIndexedSearch(
        async () => { current = false; return { results: [{ id: "stale" }], updating: false }; },
        () => current,
        (results) => seen.push(results),
    );
    assert.deepEqual(seen, []);
});
