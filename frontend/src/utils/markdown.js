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
                const url = safeUrl(match[2]);
                tokens.push(url ? { type: "link", text: match[1], url } : { type: "text", text: match[0] });
            } else {
                tokens.push({ type: pattern.type, text: match[1] });
            }
            index += match[0].length;
            matched = true;
            break;
        }
        if (matched) continue;

        const nextSpecial = rest.slice(1).search(/[!\[`*_~]/);
        const length = nextSpecial < 0 ? rest.length : nextSpecial + 1;
        tokens.push({ type: "text", text: rest.slice(0, length) });
        index += length;
    }
    return tokens;
}

function splitTableRow(line) {
    let value = line.trim();
    if (value.startsWith("|")) value = value.slice(1);
    if (value.endsWith("|")) value = value.slice(0, -1);
    return value.split("|").map((cell) => cell.trim());
}

function isTableSeparator(line) {
    const cells = splitTableRow(line);
    return cells.length > 0 && cells.every((cell) => /^:?-{3,}:?$/.test(cell));
}

export function parseMarkdown(source) {
    const lines = String(source ?? "").replace(/\r\n?/g, "\n").split("\n");
    const blocks = [];
    let i = 0;

    while (i < lines.length) {
        const line = lines[i];
        if (!line.trim()) { i += 1; continue; }

        const fence = line.match(/^\s*```([^`]*)$/);
        if (fence) {
            const language = fence[1].trim();
            const body = [];
            i += 1;
            while (i < lines.length && !/^\s*```\s*$/.test(lines[i])) { body.push(lines[i]); i += 1; }
            if (i < lines.length) i += 1;
            blocks.push({ type: "code", language, text: body.join("\n") });
            continue;
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
            if (/^\s*```/.test(lines[i]) || /^\s*(#{1,6})\s+/.test(lines[i]) || /^\s*>/.test(lines[i]) || /^\s*([-+*]|\d+\.)\s+/.test(lines[i])) break;
            if (i + 1 < lines.length && lines[i].includes("|") && isTableSeparator(lines[i + 1])) break;
            paragraph.push(lines[i].trim()); i += 1;
        }
        blocks.push({ type: "paragraph", inline: parseInline(paragraph.join("\n")) });
    }
    return blocks;
}
