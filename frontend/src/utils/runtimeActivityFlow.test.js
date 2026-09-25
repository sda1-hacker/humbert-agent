import test from "node:test";
import assert from "node:assert/strict";
import { createPinia, setActivePinia } from "pinia";
import { useRuntimeStore } from "../stores/runtime.js";

test("实时事件保持思考、工具、再思考的顺序", async () => {
  setActivePinia(createPinia());
  const runtime = useRuntimeStore();
  const base = { sessionID: "session-activity", requestID: "request-activity" };

  await runtime.handleEvent({ ...base, type: "turn.started" });
  await runtime.handleEvent({ ...base, type: "assistant.reasoning.delta", delta: "先查看目录" });
  await runtime.handleEvent({ ...base, type: "tool.started", toolCallID: "call-1", toolName: "list_files", toolArguments: "{}" });
  await runtime.handleEvent({ ...base, type: "tool.completed", toolCallID: "call-1", toolName: "list_files" });
  await runtime.handleEvent({ ...base, type: "assistant.reasoning.delta", delta: "再整理结果" });
  runtime.flushStreamingDelta(base.sessionID);

  const steps = runtime.liveActivity(base.sessionID);
  assert.deepEqual(steps.map((step) => step.type), ["thinking", "tool", "thinking"]);
  assert.equal(steps[0].content, "先查看目录");
  assert.equal(steps[0].status, "completed");
  assert.equal(steps[1].call.status, "completed");
  assert.equal(steps[2].content, "再整理结果");
  runtime.disposeEvents();
});
