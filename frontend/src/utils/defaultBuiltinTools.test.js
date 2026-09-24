import test from "node:test";
import assert from "node:assert/strict";
import { defaultBuiltinTools } from "./defaultBuiltinTools.js";

test("新建 Agent 保留常用文件与浏览器能力，命令和删除工具需显式开启", () => {
  const catalog = ["run_command", "glob_files", "delete_file", "web_search", "read_file", "browser"]
      .map((name) => ({ name }));
  assert.deepEqual(defaultBuiltinTools(catalog), ["glob_files", "web_search", "read_file", "browser"]);
});
