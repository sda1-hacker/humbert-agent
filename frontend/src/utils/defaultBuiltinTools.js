// 新建 Agent 默认开启常用能力；用户仍可在设置中逐项关闭。
const defaultNames = new Set([
  "list_files", "glob_files", "grep_files", "read_file",
  "write_file", "edit_file", "web_search", "web_fetch", "browser",
  "schedule_task", "computer", "install_skill", "list_agents", "run_agent",
]);

export function defaultBuiltinTools(catalog) {
  return (Array.isArray(catalog) ? catalog : [])
      .filter((tool) => defaultNames.has(tool.name))
      .map((tool) => tool.name);
}
