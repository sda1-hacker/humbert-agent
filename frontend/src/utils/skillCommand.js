/**
 * 只识别光标之前、独立词边界上的 /skill 命令，避免把 URL 和 /skills 当成入口。
 * 保留替换范围，不改写命令前后的正文；选区和中文输入法由 Composer 处理。
 */
export function parseSkillCommand(text, cursor = text.length) {
    const before = text.slice(0, cursor);
    const match = /(^|\s)\/skill(?:[ \t]+([^\n]*))?$/.exec(before);
    if (!match) return null;
    return { start: match.index + match[1].length, end: cursor, query: (match[2] || "").trim() };
}

/**
 * Alias 只参与展示和搜索，真正引用始终使用 Agent 的 canonical enabledSkills 名称。
 * 更新后不兼容的包可能仍保留启用引用，需排除 unsupported；needs_setup 仍可加载指令。
 */
export function matchingEnabledSkills(items, enabledNames, query = "") {
    const enabled = new Set(enabledNames || []);
    const needle = query.trim().toLocaleLowerCase();
    return items.filter(item => item.valid && item.runtimeStatus !== "unsupported" && enabled.has(item.name) &&
        [item.name, item.alias, item.description].some(value => String(value || "").toLocaleLowerCase().includes(needle)));
}

/** 草稿与后端保留 canonical 引用；输入区和历史消息将其呈现为技能标签。 */
export function insertSkillReference(text, command, name) {
    const reference = `$${name} `;
    return { text: text.slice(0, command.start) + reference + text.slice(command.end), cursor: command.start + reference.length };
}

/** 只把已知技能的完整引用转成标签，普通美元文本、路径和代码保持原样。 */
export function splitSkillReferences(text, skills = []) {
    const catalog = new Map(skills.filter(skill => skill?.name).map(skill => [skill.name, skill]));
    const names = [...catalog.keys()].sort((a, b) => b.length - a.length);
    if (!names.length || !text) return [{ type: 'text', text, start: 0, end: text.length }];
    const escaped = names.map(name => name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'));
    const pattern = new RegExp(`\\$(${escaped.join('|')})(?![\\p{L}\\p{N}_:-])`, 'gu');
    const codeRanges = [...text.matchAll(/(`+)[\s\S]*?\1/g)].map(match => [match.index, match.index + match[0].length]);
    const parts = [];
    let offset = 0;
    for (const match of text.matchAll(pattern)) {
        const start = match.index;
        if (start && !/[\s([{,，。！？：；、]/u.test(text[start - 1])) continue;
        if (codeRanges.some(([from, to]) => start >= from && start < to)) continue;
        if (start > offset) parts.push({ type: 'text', text: text.slice(offset, start), start: offset, end: start });
        const end = start + match[0].length;
        parts.push({ type: 'skill', text: match[0], name: match[1], skill: catalog.get(match[1]), start, end });
        offset = end;
    }
    if (offset < text.length || !parts.length) parts.push({ type: 'text', text: text.slice(offset), start: offset, end: text.length });
    return parts;
}
