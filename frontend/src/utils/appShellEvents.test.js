import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { parse, compileScript } from "@vue/compiler-sfc";

// 验证实际 AppShell 的资源清理函数，不为测试额外导出一套生产生命周期 API。
const { descriptor } = parse(readFileSync(new URL("../layouts/AppShell.vue", import.meta.url), "utf8"));
const ast = compileScript(descriptor, { id: "shell-events-test" }).scriptSetupAst;
const functions = ast.filter((node) => node.type === "FunctionDeclaration" && ["startEvents", "stopEvents"].includes(node.id.name));
assert.equal(functions.length, 2);
const source = functions.map((node) => descriptor.scriptSetup.content.slice(node.start, node.end)).join("\n");
const createShellEvents = new Function("eventStores", `let startedEventStores = []; ${source}; return { start: startEvents, stop: stopEvents };`);

test("事件初始化失败时释放部分初始化的组件，其他组件不会被启动", () => {
  const calls = [];
  const lifecycle = createShellEvents([
    { key: "first", initialiseEvents: () => calls.push("initialiseEvents:first"), disposeEvents: () => calls.push("disposeEvents:first") },
    { key: "failed", initialiseEvents() { calls.push("initialiseEvents:failed"); throw new Error("failed"); }, disposeEvents: () => calls.push("disposeEvents:failed") },
    { key: "last", initialiseEvents: () => calls.push("initialiseEvents:last") },
  ]);
  assert.throws(() => lifecycle.start(), /failed/);
  lifecycle.stop();
  assert.deepEqual(calls, ["initialiseEvents:first", "initialiseEvents:failed", "disposeEvents:failed", "disposeEvents:first"]);
});

test("重复挂载不重复订阅，某个卸载失败仍继续清理其他组件", () => {
  const calls = [];
  const lifecycle = createShellEvents([
    { key: "first", initialiseEvents: () => calls.push("initialiseEvents:first"), disposeEvents: () => calls.push("disposeEvents:first") },
    { key: "last", initialiseEvents: () => calls.push("initialiseEvents:last"), disposeEvents() { calls.push("disposeEvents:last"); throw new Error("cleanup"); } },
  ]);
  lifecycle.start(); lifecycle.start();
  assert.throws(() => lifecycle.stop(), AggregateError);
  lifecycle.stop();
  assert.deepEqual(calls, ["initialiseEvents:first", "initialiseEvents:last", "disposeEvents:last", "disposeEvents:first"]);
});
