import assert from "node:assert/strict";
import test from "node:test";

import { browserNeedsHumanVerification, browserScreenshotOfCall, buildConversationBlocks, fileChangesOfCalls, scheduledTasksOfCalls } from "./toolTrace.js";

test("历史 JSONL 消息投影为思考、工具、再思考的有序步骤", () => {
  const toolCall = (id, name, reasoning) => ({
    id: `assistant-${id}`,
    role: "assistant",
    content: "",
    metadata: {
      reasoning_content: reasoning,
      tool_calls: [{ id, type: "function", function: { name, arguments: "{}" } }],
    },
  });
  const messages = [
    { id: "user", role: "user", content: "查看目录" },
    toolCall("one", "list_files", "先查看目录"),
    { id: "result-one", role: "tool", content: "文件 A", metadata: { tool_call_id: "one", tool_name: "list_files" } },
    toolCall("two", "read_file", "然后读取文件"),
    { id: "result-two", role: "tool", content: "权限不足", metadata: { tool_call_id: "two", tool_name: "read_file", is_error: true } },
    { id: "answer", role: "assistant", content: "已查看", metadata: { reasoning_content: "总结结果" } },
  ];
  const blocks = buildConversationBlocks(messages);
  assert.equal(blocks.length, 2);
  assert.deepEqual(blocks[1].activity.map((step) => step.type), ["thinking", "tool", "thinking", "tool", "thinking"]);
  assert.equal(blocks[1].activity[1].call.result, "文件 A");
  assert.equal(blocks[1].activity[3].call.status, "failed");
  assert.equal(blocks[1].activity[4].content, "总结结果");
});

test("模型未返回思考和过程正文时不伪造思考消息", () => {
  const blocks = buildConversationBlocks([
    { id: "call", role: "assistant", content: "", metadata: { tool_calls: [{ id: "tool-1", function: { name: "list_files", arguments: "{}" } }] } },
    { id: "result", role: "tool", content: "空目录", metadata: { tool_call_id: "tool-1", tool_name: "list_files" } },
    { id: "answer", role: "assistant", content: "目录为空", metadata: {} },
  ]);
  assert.deepEqual(blocks[0].activity.map((step) => step.type), ["tool"]);
  assert.equal(blocks[0].activity[0].call.result, "空目录");
});

test("只把成功的工作区相对文件变化标记为可预览", () => {
  const calls = [
    { name: "write_file", status: "completed", arguments: JSON.stringify({ path: "notes.txt" }), result: JSON.stringify({ created: true }) },
    { name: "write_file", status: "failed", arguments: JSON.stringify({ path: "failed.txt" }) },
    { name: "write_file", status: "completed", arguments: JSON.stringify({ path: "../secret.txt" }) },
  ];
  const changes = fileChangesOfCalls(calls);
  assert.equal(changes.length, 2);
  assert.deepEqual(changes[0], { operation: "created", path: "notes.txt", browsable: true });
  assert.equal(changes[1].path, "../secret.txt");
  assert.equal(changes[1].browsable, false);
});

test("只为成功创建的任务提供入口", () => {
  const created = { name: "schedule_task", status: "completed", result: JSON.stringify({ task_id: "123e4567-e89b-12d3-a456-426614174000", name: "晨间提醒" }) };
  const tasks = scheduledTasksOfCalls([created, created, { ...created, status: "failed" }, { name: "schedule_task", status: "completed", result: "{}" }]);
  assert.deepEqual(tasks, [{ id: "123e4567-e89b-12d3-a456-426614174000", name: "晨间提醒", nextRunAt: "" }]);
});

test("浏览器截图是会话附件，复制后才显示工作区文件", () => {
  const call = { name: "browser", status: "completed", arguments: '{"action":"screenshot"}', result: JSON.stringify({ screenshot_attachment_id: "image-1", screenshot_name: "browser-screenshot.png" }) };
  assert.deepEqual(browserScreenshotOfCall(call), { attachmentId: "image-1", name: "browser-screenshot.png" });
  assert.deepEqual(fileChangesOfCalls([call]), []);
  const copy = { name: "copy_file", status: "completed", arguments: JSON.stringify({ attachment_id: "image-1", destination: "screenshots/go.png" }), result: JSON.stringify({ attachment_id: "image-1", destination: "screenshots/go.png" }) };
  assert.deepEqual(fileChangesOfCalls([call, copy]), [{ operation: "copied", path: "screenshots/go.png", browsable: true }]);
  assert.equal(browserScreenshotOfCall({ ...call, status: "failed" }), null);
});

test("浏览器验证码提示只来自成功的 browser 结构化结果", () => {
  const result = JSON.stringify({ needs_human_verification: true });
  assert.equal(browserNeedsHumanVerification({ name: "browser", status: "completed", result }), true);
  assert.equal(browserNeedsHumanVerification({ name: "browser", status: "failed", result }), false);
  assert.equal(browserNeedsHumanVerification({ name: "web_fetch", status: "completed", result }), false);
  assert.equal(browserNeedsHumanVerification({ name: "browser", status: "completed", result: "not-json" }), false);
});
