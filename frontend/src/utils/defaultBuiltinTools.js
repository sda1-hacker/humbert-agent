// 默认选择来自后端同一份工具目录，保留用户显式关闭的选择。
export function defaultBuiltinTools(catalog) {
  return (Array.isArray(catalog) ? catalog : [])
    .filter((tool) => tool.defaultEnabled === true)
    .map((tool) => tool.name);
}
