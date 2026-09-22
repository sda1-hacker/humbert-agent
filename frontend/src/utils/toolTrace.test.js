import assert from "node:assert/strict";
import test from "node:test";

import { fileChangesOfCalls } from "./toolTrace.js";

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
