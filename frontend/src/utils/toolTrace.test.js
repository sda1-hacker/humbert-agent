import assert from "node:assert/strict";
import test from "node:test";

import { fileChangesOfCalls, scheduledTasksOfCalls } from "./toolTrace.js";

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
