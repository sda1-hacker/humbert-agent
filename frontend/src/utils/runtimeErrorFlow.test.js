import test from "node:test";
import assert from "node:assert/strict";
import { createPinia, setActivePinia } from "pinia";
import { useRuntimeStore } from "../stores/runtime.js";
import { useSessionStore } from "../stores/sessions.js";

test("Turn 失败在对应会话保留可见错误和请求编号，关闭后清除", async () => {
  setActivePinia(createPinia());
  const runtime = useRuntimeStore();
  const sessions = useSessionStore();
  sessions.refreshMessages = async () => {};
  runtime.refreshContextUsage = async () => null;

  await runtime.handleEvent({
    type: "turn.failed",
    sessionID: "session-a",
    requestID: "request-1",
    error: "模型服务的免费额度已用尽。",
  });

  assert.match(runtime.terminalError("session-a"), /免费额度已用尽/);
  assert.equal(runtime.terminalErrorRequestID("session-a"), "request-1");
  assert.equal(runtime.terminalError("session-b"), "");

  runtime.dismissTerminalError("session-a");
  assert.equal(runtime.terminalError("session-a"), "");
  assert.equal(runtime.terminalErrorRequestID("session-a"), "");
});

test("成功的新 Turn 清除上一次失败提示", async () => {
  setActivePinia(createPinia());
  const runtime = useRuntimeStore();
  const sessions = useSessionStore();
  sessions.refreshMessages = async () => {};
  runtime.refreshContextUsage = async () => null;

  await runtime.handleEvent({type: "turn.failed", sessionID: "session-a", requestID: "request-1", error: "余额不足"});
  await runtime.handleEvent({type: "turn.completed", sessionID: "session-a", requestID: "request-2"});

  assert.equal(runtime.terminalError("session-a"), "");
  assert.equal(runtime.terminalErrorRequestID("session-a"), "");
});

test("旧请求的异步收尾不能删除新请求的工具和审批", async () => {
  setActivePinia(createPinia());
  const runtime = useRuntimeStore();
  const sessions = useSessionStore();
  let release;
  sessions.refreshMessages = () => new Promise(resolve => { release = resolve; });
  runtime.refreshContextUsage = async () => null;
  await runtime.handleEvent({type: "turn.started", sessionID: "s", requestID: "old"});
  const finishing = runtime.handleEvent({type: "turn.completed", sessionID: "s", requestID: "old"});
  await runtime.handleEvent({type: "turn.started", sessionID: "s", requestID: "new"});
  await runtime.handleEvent({type: "tool.started", sessionID: "s", requestID: "new", toolCallID: "t", toolName: "read_file"});
  await runtime.handleEvent({type: "approval.requested", sessionID: "s", requestID: "new", approval: {id: "a", requestID: "new"}});
  release();
  await finishing;
  assert.equal(runtime.runs.s.requestID, "new");
  assert.equal(runtime.runs.s.tools[0].id, "t");
  assert.equal(runtime.runs.s.approval.id, "a");
  await runtime.handleEvent({type: "turn.failed", sessionID: "s", requestID: "old", error: "stale"});
  assert.equal(runtime.runs.s.requestID, "new");
  assert.equal(runtime.terminalError("s"), "");
  runtime.disposeEvents();
});
