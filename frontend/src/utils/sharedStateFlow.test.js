import test, { mock } from "node:test";
import assert from "node:assert/strict";
import { createPinia, setActivePinia } from "pinia";
let invoke;
mock.module("../api/mcp.js", {namedExports: {
  listMCPServers: () => invoke(".ListServers"),
  testMCPConnection: () => invoke(".TestConnection"),
  discoverMCPTools: () => invoke(".DiscoverTools"),
  refreshMCPTools: () => invoke(".RefreshTools"),
}});
mock.module("../api/chat.js", {namedExports: {
  getContextOverview: () => invoke(".ContextOverview"),
  cancelTurn: () => {}, compactContext: () => {}, resolveApproval: () => {}, startTurn: () => {},
}});
const { useMCPStore } = await import("../stores/mcp.js");
const { useRuntimeStore } = await import("../stores/runtime.js");

// 模拟真实 IPC 边界，保留 Pinia action 的异步执行顺序。
test("两个 MCP 页面复用一次目录请求，配置更新后丢弃旧工具目录", async () => {
  setActivePinia(createPinia());
  let releaseList, releaseTools, listCalls = 0;
  invoke = (name) => {
    if (name.endsWith(".ListServers")) {
      listCalls++;
      return new Promise(resolve => { releaseList = resolve; });
    }
    return new Promise(resolve => { releaseTools = resolve; });
  };
  try {
    const mcp = useMCPStore();
    const first = mcp.load(), second = mcp.load();
    releaseList([{id: "s", enabled: true, updatedAt: "1", connectionState: "disconnected"}]);
    await Promise.all([first, second]);
    assert.equal(listCalls, 1);
    const discover = mcp.operate(mcp.servers[0], "discover");
    const reload = mcp.load();
    releaseList([{id: "s", enabled: true, updatedAt: "2", connectionState: "disconnected"}]);
    await reload;
    releaseTools([{name: "old_tool"}]);
    await discover;
    assert.equal(mcp.toolCatalog.s, undefined);
    assert.equal(mcp.servers[0].connectionState, "disconnected");
    assert.equal(mcp.catalogLoading.s, undefined);
  } finally { invoke = null; }
});

test("较早发起的空 Overview 不能清除随后到达的新运行事件", async () => {
  setActivePinia(createPinia());
  let release;
  invoke = () => new Promise(resolve => { release = resolve; });
  const runtime = useRuntimeStore();
  try {
    const refresh = runtime.refreshContextUsage("s");
    await runtime.handleEvent({type: "turn.started", sessionID: "s", requestID: "new"});
    release({usage: {contextWindow: 32768, usedTokens: 10}, assembly: {}, runtime: {}, active: null});
    await refresh;
    assert.equal(runtime.runs.s.requestID, "new");
    assert.equal(runtime.runs.s.status.phase, "running");
  } finally { invoke = null; runtime.disposeEvents(); }
});
