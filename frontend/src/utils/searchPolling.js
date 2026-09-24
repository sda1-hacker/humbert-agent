/**
 * 后端搜索先返回已提交的索引快照，并在后台增量更新。
 * sequence 检查由调用方提供，旧搜索不能覆盖用户的新输入。
 */
export async function pollIndexedSearch(fetch, isCurrent, onUpdate, wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms))) {
    while (isCurrent()) {
        const response = await fetch();
        if (!isCurrent()) return;
        if (response?.error) throw new Error(response.error);
        onUpdate(Array.isArray(response?.results) ? response.results : [], Boolean(response?.updating));
        if (!response?.updating) return;
        await wait(400);
    }
}
