export const knowledgeCitationContext = Symbol("knowledge-citations");

// 定位来自解析器；合并重复、相邻的页码，不把近似位置显示成精确出处。
export function sourceLocationSummary(locators = []) {
  const values = Array.isArray(locators) ? locators.filter((value) => value && typeof value === "object") : [];
  function ranges(items, startKey, endKey = startKey) {
    const spans = items.filter((v) => Number.isSafeInteger(v[startKey]) && v[startKey] > 0)
      .map((v) => [v[startKey], Number.isSafeInteger(v[endKey]) ? Math.max(v[startKey], v[endKey]) : v[startKey]])
      .sort((a, b) => a[0] - b[0]);
    const merged = [];
    for (const span of spans) {
      const last = merged.at(-1);
      if (last && span[0] <= last[1] + 1) last[1] = Math.max(last[1], span[1]);
      else merged.push(span);
    }
    return merged.map(([start, end]) => start === end ? String(start) : `${start}–${end}`).join("、");
  }
  const parts = [];
  const pages = ranges(values, "page", "page_end");
  const slides = ranges(values, "slide");
  if (pages) parts.push(`第 ${pages} 页`);
  if (slides) parts.push(`幻灯片 ${slides}`);
  for (const sheet of new Set(values.map((v) => v.sheet).filter(Boolean))) {
    const rows = ranges(values.filter((v) => v.sheet === sheet), "row_start", "row_end");
    parts.push(`工作表 ${sheet}${rows ? ` · 第 ${rows} 行` : ""}`);
  }
  if (!parts.length) {
    const blocks = ranges(values, "block");
    if (blocks) parts.push(`段落 / 表格 ${blocks}`);
  }
  return { label: parts.join(" · ") || "原文分块", approximate: values.some((v) => v.partial) };
}

export function sourceIDOfURL(url) {
  const match = typeof url === "string" && url.match(/^humbert-source:\/\/([A-Za-z0-9_-]{1,2048})$/);
  return match ? match[1] : "";
}

// 只接受实际知识库工具返回的结构化结果；模型生成一个 URL 并不能获得文件读取权限。
export function normalizeKnowledgeSource(value) {
  if (!value || typeof value !== "object" || !value.id || sourceIDOfURL(value.url) !== value.id) return null;
  if (typeof value.title !== "string" || typeof value.content !== "string" || !value.collection_id || !value.document_id || !value.chunk_id || !Number.isSafeInteger(value.revision) || value.revision < 1) return null;
  return { ...value, title: value.title.slice(0, 300), content: value.content.slice(0, 24000), excerpt: String(value.excerpt || "").slice(0, 4000) };
}

export function knowledgeSourcesOfCalls(calls) {
  const sources = new Map();
  for (const call of Array.isArray(calls) ? calls : []) {
    if (call?.name !== "knowledge_search" || call.status !== "completed" || typeof call.result !== "string") continue;
    try {
      const result = JSON.parse(call.result);
      if (result.kind !== "knowledge_sources" || !Array.isArray(result.sources)) continue;
      for (const value of result.sources) {
        const source = normalizeKnowledgeSource(value);
        if (source && (sources.get(source.id)?.content.length || 0) <= source.content.length) sources.set(source.id, source);
      }
    } catch { /* 流式未闭合或普通文本不推断为检索来源。 */ }
  }
  return [...sources.values()];
}
