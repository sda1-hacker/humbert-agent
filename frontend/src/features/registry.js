/** 静态页面注册表：只负责稳定 ID 与页面定义，不查找 Store，也不管理后台生命周期。
 * 新功能在 features 下声明自己的页面并加入装配清单；导航与页面宿主共用同一份定义。
 */
export function createFeatureRegistry(definitions) {
  const byKey = new Map();
  for (const definition of definitions) {
    if (!definition.key || byKey.has(definition.key) || typeof definition.load !== "function") {
      throw new Error(`Invalid or duplicate feature: ${definition.key}`);
    }
    byKey.set(definition.key, Object.freeze({ ...definition }));
  }
  return Object.freeze({
    items: Object.freeze([...byKey.values()]),
    get: (key) => byKey.get(key),
  });
}
