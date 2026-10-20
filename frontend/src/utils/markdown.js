const safeUrlPattern = /^(https?:|mailto:|data:image\/(?:png|jpe?g|gif|webp);base64,)/i;

export function safeUrl(value) {
    const normalized = String(value ?? "").trim();
    if (!normalized || !safeUrlPattern.test(normalized)) {
        return "";
    }
    return normalized;
}

export function safeInlineImageUrl(value) {
    const url = safeUrl(value);
    return /^data:image\/(?:png|jpe?g|gif|webp);base64,/i.test(url) ? url : "";
}

const mathDelimiters = [
    { left: "$$", right: "$$", display: true },
    { left: "\\[", right: "\\]", display: true },
    { left: "\\(", right: "\\)", display: false },
    { left: "$", right: "$", display: false },
];

function isEscaped(text, index) {
    let slashes = 0;
    while (index > 0 && text[--index] === "\\") slashes += 1;
    return slashes % 2 === 1;
}

function readMath(text, start) {
    if (isEscaped(text, start)) return null;
    const delimiter = mathDelimiters.find((item) => text.startsWith(item.left, start));
    if (!delimiter) return null;
    const singleDollar = delimiter.left === "$";
    const contentStart = start + delimiter.left.length;
    if (singleDollar && (!text[contentStart] || /\s|\$/.test(text[contentStart]) || text[start - 1] === "$")) return null;

    let depth = 0;
    for (let end = contentStart; end < text.length; end += 1) {
        if (!delimiter.display && text[end] === "\n") return null;
        if (depth === 0 && text.startsWith(delimiter.right, end)) {
            // 单美元符号采用紧贴内容的边界，避免把 $10 和 $20 之类的金额配成公式。
            if (singleDollar && (/\s/.test(text[end - 1]) || /[\d$]/.test(text[end + 1] || ""))) return null;
            const body = text.slice(contentStart, end);
            if (!body.trim()) return null;
            return { type: "math", text: body, raw: text.slice(start, end + delimiter.right.length), display: delimiter.display };
        }
        if (text[end] === "\\") end += 1;
        else if (text[end] === "{") depth += 1;
        else if (text[end] === "}") depth = Math.max(0, depth - 1);
    }
    // 流式消息尚未闭合时保留原文，不把公式里的下划线、星号解释为 Markdown。
    return singleDollar && /^\d/.test(text[contentStart]) ? null : { type: "text", text: text.slice(start) };
}

export function parseInline(source) {
    const text = String(source ?? "");
    const tokens = [];
    let index = 0;
    const patterns = [
        { type: "image", re: /^!\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)/ },
        { type: "link", re: /^\[([^\]]+)\]\(([^)\s]+)(?:\s+"[^"]*")?\)/ },
        { type: "code", re: /^`([^`]+)`/ },
        { type: "strong", re: /^\*\*([^*]+)\*\*/ },
        { type: "strong", re: /^__([^_]+)__/ },
        { type: "em", re: /^\*([^*\n]+)\*/ },
        { type: "em", re: /^_([^_\n]+)_/ },
        { type: "strike", re: /^~~([^~]+)~~/ },
    ];

    while (index < text.length) {
        const rest = text.slice(index);
        const slashes = rest.match(/^\\{2,}/)?.[0].length || 0;
        if (slashes) {
            const length = slashes - slashes % 2;
            tokens.push({ type: "text", text: rest.slice(0, length) }); index += length; continue;
        }
        if ((rest.startsWith("\\$") || rest.startsWith("\\|")) && !isEscaped(text, index)) {
            tokens.push({ type: "text", text: rest[1] }); index += 2; continue;
        }
        const math = readMath(text, index);
        if (math) {
            tokens.push(math); index += (math.raw || math.text).length; continue;
        }
        let matched = false;
        for (const pattern of patterns) {
            const match = rest.match(pattern.re);
            if (!match) continue;
            if (pattern.type === "image") {
                const url = safeUrl(match[2]);
                const inlineUrl = safeInlineImageUrl(url);
                tokens.push(inlineUrl
                    ? { type: "image", alt: match[1], url: inlineUrl }
                    : url && /^https?:/i.test(url)
                        ? { type: "remote-image", alt: match[1], url }
                        : { type: "text", text: match[0] });
            } else if (pattern.type === "link") {
                const sourceID = match[2].match(/^humbert-source:\/\/([A-Za-z0-9_-]{1,2048})$/)?.[1];
                const url = safeUrl(match[2]);
                tokens.push(sourceID ? { type: "citation", text: match[1], sourceID } : url ? { type: "link", text: match[1], url } : { type: "text", text: match[0] });
            } else {
                tokens.push({ type: pattern.type, text: match[1],
                    ...(pattern.type === "code" ? {} : { children: parseInline(match[1]) }) });
            }
            index += match[0].length;
            matched = true;
            break;
        }
        if (matched) continue;

        const nextSpecial = rest.slice(1).search(/[!\\\[`*_~$]/);
        const length = nextSpecial < 0 ? rest.length : nextSpecial + 1;
        tokens.push({ type: "text", text: rest.slice(0, length) });
        index += length;
    }
    return tokens;
}

function splitTableRow(line) {
    let value = line.trim();
    if (value.startsWith("|")) value = value.slice(1);
    if (value.endsWith("|") && !isEscaped(value, value.length - 1)) value = value.slice(0, -1);
    const cells = [];
    let start = 0;
    for (let index = 0; index < value.length; index += 1) {
        if (value[index] === "\\" && value[index + 1] === "\\") { index += 1; continue; }
        if (value[index] === "`" && !isEscaped(value, index)) {
            const end = value.indexOf("`", index + 1);
            if (end > index) { index = end; continue; }
        }
        if (value[index] === "$" || value[index] === "\\") {
            const math = readMath(value, index);
            if (math?.type === "math") { index += math.raw.length - 1; continue; }
        }
        if (value[index] === "|" && !isEscaped(value, index)) {
            cells.push(value.slice(start, index).trim()); start = index + 1;
        }
    }
    cells.push(value.slice(start).trim());
    return cells;
}

function isTableSeparator(line) {
    const cells = splitTableRow(line);
    return cells.length > 0 && cells.every((cell) => /^:?-{3,}:?$/.test(cell));
}

export function parseMarkdown(source) {
    const text = String(source ?? "").replace(/\r\n?/g, "\n");
    const lines = text.split("\n");
    let offset = 0;
    const offsets = lines.map((line) => { const start = offset; offset += line.length + 1; return start; });
    const blocks = [];
    let i = 0;

    while (i < lines.length) {
        const line = lines[i];
        if (!line.trim()) { i += 1; continue; }

        const fence = line.match(/^\s*(`{3,}|~{3,})([^`]*)$/);
        if (fence) {
            const language = fence[2].trim();
            const closingFence = new RegExp(`^\\s*${fence[1][0]}{${fence[1].length},}\\s*$`);
            const body = [];
            i += 1;
            while (i < lines.length && !closingFence.test(lines[i])) { body.push(lines[i]); i += 1; }
            if (i < lines.length) i += 1;
            blocks.push({ type: "code", language, text: body.join("\n") });
            continue;
        }

        const trimmed = line.trimStart();
        if (/^(\$\$|\\\[)/.test(trimmed)) {
            const start = offsets[i] + line.length - trimmed.length;
            const math = readMath(text, start);
            if (math?.type === "math") {
                const end = start + math.raw.length;
                const nextLine = text.indexOf("\n", end);
                if (!text.slice(end, nextLine < 0 ? text.length : nextLine).trim()) {
                    blocks.push(math);
                    while (i < lines.length && offsets[i] < end) i += 1;
                    continue;
                }
            }
        }

        const heading = line.match(/^\s*(#{1,6})\s+(.+)$/);
        if (heading) {
            blocks.push({ type: "heading", level: heading[1].length, inline: parseInline(heading[2].trim()) });
            i += 1; continue;
        }

        if (/^\s*>/.test(line)) {
            const quoted = [];
            while (i < lines.length && /^\s*>/.test(lines[i])) { quoted.push(lines[i].replace(/^\s*>\s?/, "")); i += 1; }
            blocks.push({ type: "quote", text: quoted.join("\n") });
            continue;
        }

        const listMatch = line.match(/^\s*([-+*]|\d+\.)\s+(.+)$/);
        if (listMatch) {
            const ordered = /\d+\./.test(listMatch[1]);
            const items = [];
            while (i < lines.length) {
                const item = lines[i].match(/^\s*([-+*]|\d+\.)\s+(.+)$/);
                if (!item || /\d+\./.test(item[1]) !== ordered) break;
                items.push(parseInline(item[2])); i += 1;
            }
            blocks.push({ type: "list", ordered, items });
            continue;
        }

        if (i + 1 < lines.length && line.includes("|") && isTableSeparator(lines[i + 1])) {
            const headers = splitTableRow(line).map(parseInline);
            i += 2;
            const rows = [];
            while (i < lines.length && lines[i].trim() && lines[i].includes("|")) {
                rows.push(splitTableRow(lines[i]).map(parseInline)); i += 1;
            }
            blocks.push({ type: "table", headers, rows });
            continue;
        }

        if (/^\s*([-*_])(?:\s*\1){2,}\s*$/.test(line)) {
            blocks.push({ type: "rule" }); i += 1; continue;
        }

        const paragraph = [line.trim()];
        i += 1;
        while (i < lines.length && lines[i].trim()) {
            if (/^\s*(`{3,}|~{3,})/.test(lines[i]) || /^\s*(#{1,6})\s+/.test(lines[i]) || /^\s*>/.test(lines[i]) || /^\s*([-+*]|\d+\.)\s+/.test(lines[i])) break;
            if (/^\s*(\$\$|\\\[)/.test(lines[i])) break;
            if (i + 1 < lines.length && lines[i].includes("|") && isTableSeparator(lines[i + 1])) break;
            paragraph.push(lines[i].trim()); i += 1;
        }
        blocks.push({ type: "paragraph", inline: parseInline(paragraph.join("\n")) });
    }
    return blocks;
}
