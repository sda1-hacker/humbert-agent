import { toRaw } from "vue";

// 请求令牌只用于异步提交校验，不属于需要渲染或持久化的业务状态。
// 按 Store 实例隔离；同一资源的新请求使旧请求失效，不影响其它资源并行加载。
const requestsByOwner = new WeakMap();

export function beginLatestRequest(owner, key) {
    // Pinia 开发工具为每个 action 包装不同代理，必须以同一原始 Store 作为请求所有者。
    owner = toRaw(owner);
    let requests = requestsByOwner.get(owner);
    if (!requests) {
        requests = new Map();
        requestsByOwner.set(owner, requests);
    }
    const token = Symbol(key);
    requests.set(key, token);
    return () => requests.get(key) === token;
}

// 切换工作区时全部失效；删除任务等操作则只使相关资源失效。
export function invalidateRequests(owner, ...keys) {
    owner = toRaw(owner);
    const requests = requestsByOwner.get(owner);
    if (!requests) return;
    if (keys.length === 0) requests.clear();
    else keys.forEach((key) => requests.delete(key));
}
