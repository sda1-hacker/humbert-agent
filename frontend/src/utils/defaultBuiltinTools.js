// 新建 Agent 只预选常用能力；其余工具仍可在 Agent 设置中逐项开启。
const defaultNames = new Set([
  "list_files", "glob_files", "grep_files", "read_file",
  "write_file", "edit_file", "web_search", "web_fetch", "browser",
  "get_current_time", "schedule_task",
]);

export function defaultBuiltinTools(catalog) {
  return (Array.isArray(catalog) ? catalog : [])
      .filter((tool) => defaultNames.has(tool.name))
      .map((tool) => tool.name);
}
