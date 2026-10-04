import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { parse, compileScript } from "@vue/compiler-sfc";

// 执行实际组件的同步生命周期，验证隐藏/卸载不会残留计时器，不另造测试专用实现。
const { descriptor } = parse(readFileSync(new URL("../components/workspace/ContextPanel.vue", import.meta.url), "utf8"));
const names = ["syncWorkspace", "scheduleWorkspaceSync", "handleWorkspaceFocus", "startWorkspaceSync", "stopWorkspaceSync"];
const ast = compileScript(descriptor, { id: "workspace-panel-sync" }).scriptSetupAst;
const functions = ast.filter((node) => node.type === "FunctionDeclaration" && names.includes(node.id.name));
assert.equal(functions.length, names.length);
const source = functions.map((node) => descriptor.scriptSetup.content.slice(node.start, node.end)).join("\n");
const createSync = new Function("workspace", "agents", "window", "document", "setTimeout", "clearTimeout", "report", `
  let syncTimer = null, syncActive = false, syncRunning = false, syncError = "";
  ${source}
  return { start: startWorkspaceSync, stop: stopWorkspaceSync, sync: syncWorkspace };
`);

function harness(refresh = async () => {}) {
  const timers = new Map(), focus = new Set(), visibility = new Set(), errors = [];
  let sequence = 0;
  const window = { addEventListener: (_, fn) => focus.add(fn), removeEventListener: (_, fn) => focus.delete(fn) };
  const document = { hidden: false, addEventListener: (_, fn) => visibility.add(fn), removeEventListener: (_, fn) => visibility.delete(fn) };
  const workspace = { agentID: "agent", refresh };
  const sync = createSync(workspace, { selectedID: "agent" }, window, document,
    (fn, delay) => { timers.set(++sequence, { fn, delay }); return sequence; },
    (id) => timers.delete(id), (error) => errors.push(error));
  return { ...sync, workspace, document, timers, focus, visibility, errors,
    async tick() {
      const [id, { fn, delay }] = timers.entries().next().value;
      assert.equal(delay, 2000);
      timers.delete(id);
      await fn();
    },
  };
}

test("可见面板定期轻量同步，隐藏暂停，回到应用立即同步", async () => {
  const calls = [];
  const sync = harness(async (options) => calls.push(options));
  sync.start();
  await sync.tick();
  assert.deepEqual(calls, [{ background: true }]);
  sync.document.hidden = true;
  for (const listener of sync.visibility) listener();
  assert.equal(sync.timers.size, 0);
  assert.equal(calls.length, 1);
  sync.document.hidden = false;
  for (const listener of sync.visibility) listener();
  assert.equal(calls.length, 2);
  assert.equal(sync.timers.size, 1);
  sync.stop();
  assert.equal(sync.timers.size, 0);
  assert.equal(sync.focus.size, 0);
  assert.equal(sync.visibility.size, 0);
});

test("慢请求期间重复焦点事件不叠加请求，卸载后不重新建立定时器", async () => {
  let resolve, calls = 0;
  const pending = new Promise((yes) => { resolve = yes; });
  const sync = harness(() => { calls++; return pending; });
  sync.start();
  const tick = sync.tick();
  for (const listener of sync.focus) { listener(); listener(); }
  assert.equal(calls, 1);
  sync.stop();
  resolve(); await tick;
  assert.equal(sync.timers.size, 0);
});

test("真实同步错误只提示一次，成功恢复后再次失败仍提示", async () => {
  let failure = true;
  const sync = harness(async () => { if (failure) throw new Error("permission denied"); });
  sync.start();
  await sync.tick(); await sync.tick();
  assert.equal(sync.errors.length, 1);
  failure = false; await sync.tick();
  failure = true; await sync.tick();
  assert.equal(sync.errors.length, 2);
  sync.stop();
});

test("选中 Agent 与工作区尚未一致时，不刷新上一个工作区", async () => {
  let calls = 0;
  const sync = harness(async () => { calls++; });
  sync.workspace.agentID = "previous-agent";
  sync.start(); await sync.tick();
  assert.equal(calls, 0);
  sync.stop();
});
