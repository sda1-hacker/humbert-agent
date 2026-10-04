import assert from "node:assert/strict";
import test from "node:test";
import { createPinia, defineStore, setActivePinia } from "pinia";
import { beginLatestRequest, invalidateRequests } from "./latestRequest.js";

test("Pinia 的不同 action 代理共享请求所有者，清理目录能使在途读取失效", () => {
    setActivePinia(createPinia());
    const store = defineStore("request-owner", { state: () => ({}) })();
    const firstAction = new Proxy(store, {}), secondAction = new Proxy(store, {});
    const oldDirectory = beginLatestRequest(firstAction, "directory:src");
    const preview = beginLatestRequest(firstAction, "preview");
    const newDirectory = beginLatestRequest(secondAction, "directory:src");
    assert.equal(oldDirectory(), false);
    assert.equal(newDirectory(), true);
    assert.equal(preview(), true);
    invalidateRequests(firstAction, "directory:src");
    assert.equal(newDirectory(), false);
    invalidateRequests(secondAction);
    assert.equal(preview(), false);
});

test("普通对象请求所有者仍互相隔离", () => {
    const first = {}, second = {};
    const firstRequest = beginLatestRequest(first, "load");
    const secondRequest = beginLatestRequest(second, "load");
    invalidateRequests(first);
    assert.equal(firstRequest(), false);
    assert.equal(secondRequest(), true);
});
