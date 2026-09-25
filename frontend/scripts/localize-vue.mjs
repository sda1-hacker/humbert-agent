import { parse as parseSFC } from "@vue/compiler-sfc";
import { NodeTypes, parse as parseTemplate } from "@vue/compiler-dom";

const hasChinese = /[\u3400-\u9fff]/;
const translatedAttributes = new Set([
  "title", "placeholder", "aria-label", "description", "label", "alt",
]);

function expression(value) {
  const escaped = value.replaceAll("\\", "\\\\").replaceAll("'", "\\'");
  return `$t('${escaped}')`;
}

/**
 * 静态模板文案在编译前转成显式 $t 调用。只触碰 Vue Text 与用户可读静态属性，
 * 不扫描 DOM、不翻译用户消息、文件内容、模型回答或工具结果。
 */
export function localizeVueTemplates() {
  return {
    name: "humbert-localize-static-template",
    enforce: "pre",
    transform(source, id) {
      if (!id.endsWith(".vue") || !hasChinese.test(source)) return null;
      const { descriptor } = parseSFC(source, { filename: id });
      if (!descriptor.template) return null;
      const template = descriptor.template;
      const ast = parseTemplate(template.content);
      const edits = [];
      const offset = template.loc.start.offset;

      function visit(node, ignored = false) {
        if (node.type === NodeTypes.TEXT && !ignored && hasChinese.test(node.content)) {
          const value = node.content.trim().replace(/\s+/g, " ");
          if (value) edits.push({
            start: offset + node.loc.start.offset,
            end: offset + node.loc.end.offset,
            replacement: `{{ ${expression(value)} }}`,
          });
        }
        if (node.type === NodeTypes.ELEMENT) {
          const skip = ignored || ["pre", "code", "script"].includes(node.tag) ||
            node.props.some((prop) => prop.type === NodeTypes.DIRECTIVE && prop.name === "pre");
          if (!skip) {
            for (const prop of node.props) {
              if (prop.type !== NodeTypes.ATTRIBUTE || !translatedAttributes.has(prop.name) ||
                  !prop.value || !hasChinese.test(prop.value.content)) continue;
              const value = prop.value.content.trim();
              const safe = expression(value).replaceAll("&", "&amp;").replaceAll('"', "&quot;");
              edits.push({
                start: offset + prop.loc.start.offset,
                end: offset + prop.loc.end.offset,
                replacement: `:${prop.name}="${safe}"`,
              });
            }
          }
          for (const child of node.children) visit(child, skip);
        } else if (node.children) {
          for (const child of node.children) visit(child, ignored);
        }
      }
      visit(ast);
      if (!edits.length) return null;
      let result = source;
      for (const edit of edits.sort((a, b) => b.start - a.start)) {
        result = result.slice(0, edit.start) + edit.replacement + result.slice(edit.end);
      }
      return { code: result, map: null };
    },
  };
}
