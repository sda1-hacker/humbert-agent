import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parse as parseSFC } from "@vue/compiler-sfc";
import { parse as parseTemplate, NodeTypes } from "@vue/compiler-dom";
import { language, setLanguage, t } from "../i18n/index.js";
import translations from "../i18n/translations.js";
import { localizeVueTemplates } from "../../scripts/localize-vue.mjs";

test("四种语言切换并保持插值参数", () => {
  const initial = language.value;
  try {
    setLanguage("en-US");
    assert.equal(t("设置"), "Settings");
    assert.equal(t("读取 {path}", { path: "README.md" }), "Read README.md");
    setLanguage("ja-JP");
    assert.equal(t("设置"), "設定");
    setLanguage("ko-KR");
    assert.equal(t("设置"), "설정");
    setLanguage("zh-CN");
    assert.equal(t("设置"), "设置");
    assert.throws(() => setLanguage("fr-FR"));
  } finally {
    setLanguage(initial);
  }
});

test("静态模板文案可翻译，动态消息正文不被改写", () => {
  const source = `<template><div title="设置">设置<span>{{ message.content }}</span></div></template>`;
  const result = localizeVueTemplates().transform(source, "/tmp/Sample.vue");
  assert.match(result.code, /:title="\$t\('设置'\)"/);
  assert.match(result.code, /\{\{ \$t\('设置'\) \}\}/);
  assert.match(result.code, /\{\{ message.content \}\}/);
});

test("所有 Vue 静态中文文案都有英日韩译文", () => {
  const src = fileURLToPath(new URL("../", import.meta.url));
  const files = [];
  function collect(dir) {
    for (const name of fs.readdirSync(dir)) {
      const entry = path.join(dir, name);
      if (fs.statSync(entry).isDirectory()) collect(entry);
      else if (entry.endsWith(".vue")) files.push(entry);
    }
  }
  collect(src);
  const missing = new Set();
  const attributes = new Set(["title", "placeholder", "aria-label", "description", "label", "alt"]);
  function verify(value) {
    if (!/[\u3400-\u9fff]/.test(value)) return;
    for (const code of ["en-US", "ja-JP", "ko-KR"]) {
      if (!Object.hasOwn(translations[code], value)) missing.add(`${code}: ${value}`);
    }
  }
  for (const file of files) {
    const { descriptor } = parseSFC(fs.readFileSync(file, "utf8"), { filename: file });
    if (!descriptor.template) continue;
    function visit(node, ignored = false) {
      if (node.type === NodeTypes.TEXT && !ignored) verify(node.content.trim().replace(/\s+/g, " "));
      if (node.type === NodeTypes.ELEMENT) {
        const skip = ignored || ["pre", "code", "script"].includes(node.tag) ||
          node.props.some((prop) => prop.type === NodeTypes.DIRECTIVE && prop.name === "pre");
        if (!skip) for (const prop of node.props) {
          if (prop.type === NodeTypes.ATTRIBUTE && attributes.has(prop.name) && prop.value) verify(prop.value.content.trim());
        }
        for (const child of node.children) visit(child, skip);
      } else if (node.children) for (const child of node.children) visit(child, ignored);
    }
    visit(parseTemplate(descriptor.template.content));
  }
  assert.deepEqual([...missing], []);
});
