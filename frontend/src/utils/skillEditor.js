/** 富文本只是草稿的显示投影；标签始终序列化为原来的 $canonical-name。 */
export function readEditorText(root) {
    if (root.nodeType === 3) return (root.nodeValue || '').replaceAll('\u00a0', ' ');
    if (root.nodeType === 1 && root.hasAttribute('data-skill-reference')) return `$${root.getAttribute('data-skill-reference')}`;
    if (root.nodeType === 1 && root.hasAttribute('data-editor-end')) return '';
    if (root.nodeName === 'BR') return '\n';
    const children = [...root.childNodes];
    if (children.length === 1 && children[0].nodeName === 'BR') return '';
    return children.map((child, index) => {
        const block = /^(DIV|P)$/.test(child.nodeName);
        const previousBlock = index && /^(DIV|P)$/.test(children[index - 1].nodeName);
        return `${index && (block || previousBlock) ? '\n' : ''}${readEditorText(child)}`;
    }).join('');
}

/** 使用 canonical 文本偏移，显示别名变长或变短都不会改变命令替换位置。 */
export function readEditorSelection(root, fallback = { start: 0, end: 0 }) {
    const selection = root.ownerDocument.getSelection();
    if (!selection?.rangeCount) return fallback;
    const range = selection.getRangeAt(0);
    if (!root.contains(range.startContainer) || !root.contains(range.endContainer)) return fallback;
    function offset(container, position) {
        const prefix = root.ownerDocument.createRange();
        prefix.selectNodeContents(root);
        prefix.setEnd(container, position);
        return readEditorText(prefix.cloneContents()).length;
    }
    return { start: offset(range.startContainer, range.startOffset), end: offset(range.endContainer, range.endOffset) };
}

export function setEditorSelection(root, start, end = start) {
    const units = [];
    function visit(node) {
        if (node.nodeType === 3 || node.nodeName === 'BR' || (node.nodeType === 1 && node.hasAttribute('data-skill-reference'))) {
            units.push({ node, length: readEditorText(node).length });
        } else for (const child of node.childNodes) visit(child);
    }
    visit(root);
    function point(target) {
        let offset = Math.max(0, target);
        for (const { node, length } of units) {
            if (offset <= length) {
                if (node.nodeType === 3) return [node, offset];
                const index = [...node.parentNode.childNodes].indexOf(node);
                return [node.parentNode, index + (offset > 0 ? 1 : 0)];
            }
            offset -= length;
        }
        return [root, root.childNodes.length];
    }
    const range = root.ownerDocument.createRange();
    range.setStart(...point(start));
    range.setEnd(...point(end));
    const selection = root.ownerDocument.getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
}
