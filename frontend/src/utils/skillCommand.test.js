import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { parse, compileScript } from "@vue/compiler-sfc";
import { ref, computed, nextTick } from "vue";
import { parseSkillCommand, matchingEnabledSkills, insertSkillReference, splitSkillReferences } from "./skillCommand.js";

test("/skill 只匹配独立命令，按光标替换且保留前后正文", () => {
    assert.deepEqual(parseSkillCommand("/skill"), { start: 0, end: 6, query: "" });
    const text = "先看需求\n/skill 版式\n保留这段正文";
    const cursor = text.indexOf("\n保留");
    const command = parseSkillCommand(text, cursor);
    assert.equal(command.query, "版式");
    const inserted = insertSkillReference(text, command, "kami");
    assert.equal(inserted.text, "先看需求\n$kami \n保留这段正文");
    assert.equal(inserted.cursor, "先看需求\n$kami ".length);
    assert.equal(parseSkillCommand("前文 /skill /skill").start, 3);
    for (const value of ["https://example.com/skill", "/skills", "/skillful", "路径/skill", "/skill\n普通正文"]) assert.equal(parseSkillCommand(value), null);
});

test("搜索只返回当前 Agent 启用且有效的技能，别名不改变真实引用", () => {
    const items = [
        { name: "kami", alias: "页面排版", description: "Design layouts", valid: true },
        { name: "disabled", alias: "页面排版", valid: true },
        { name: "broken", alias: "页面排版", valid: false },
    ];
    assert.deepEqual(matchingEnabledSkills(items, ["kami", "broken"], "排版").map(s => s.name), ["kami"]);
    assert.equal(matchingEnabledSkills(items, ["kami"], "DESIGN").length, 1);
    assert.equal(matchingEnabledSkills(items, [], "").length, 0);
    assert.equal(matchingEnabledSkills(items, ["missing"], "").length, 0);
});

test("不兼容包即使仍被 Agent 引用也不能选择；待配置包仍允许加载指令", () => {
    const items = [
        { name: "unsupported-enabled", valid: true, runtimeStatus: "unsupported" },
        { name: "needs-setup", valid: true, runtimeStatus: "needs_setup" },
    ];
    assert.deepEqual(matchingEnabledSkills(items, items.map(s => s.name)).map(s => s.name), ["needs-setup"]);
});

// 执行 Composer 的真实交互函数，覆盖中文输入法、键盘选择及等待目录期间的发送保护。
const { descriptor } = parse(readFileSync(new URL("../components/chat/ComposerBar.vue", import.meta.url), "utf8"));
const ast = compileScript(descriptor, { id: "skill-command-test" }).scriptSetupAst;
const functions = ast.filter(node => node.type === "FunctionDeclaration" && ["handleComposerKey", "chooseSkill", "textareaElement"].includes(node.id.name));
const source = functions.map(node => descriptor.scriptSetup.content.slice(node.start, node.end)).join("\n");
function composer() {
    const draft = ref("/skill");
    const skillCommand = ref(parseSkillCommand(draft.value));
    const skillStore = { loading: false, loadError: "" };
    const skillIndex = ref(0);
    const skillMatches = ref([{ name: "kami" }, { name: "code-review" }]);
    let sends = 0;
    const input = { selectionStart: 6, focus() {}, setSelectionRange(start) { this.selectionStart = start; } };
    const bindings = { draft, skillCommand, skillStore, skillIndex, skillMatches, nextTick, parseSkillCommand, insertSkillReference,
        skillMenuOpen: computed(() => Boolean(skillCommand.value)), composerInput: ref({ input }),
        handleEnter: () => sends++, document: { getElementById: () => null } };
    const handlers = new Function(...Object.keys(bindings), `${source}; return {handleComposerKey, chooseSkill};`)(...Object.values(bindings));
    return { ...bindings, ...handlers, input, sends: () => sends };
}
function key(key, extra = {}) { return { key, preventDefault() { this.prevented = true; }, ...extra }; }

test("方向键和 Tab 选择技能；选择后焦点仍在草稿中，不直接发送", async () => {
    const c = composer();
    c.handleComposerKey(key("ArrowUp"));
    assert.equal(c.skillIndex.value, 1);
    c.handleComposerKey(key("Tab"));
    await nextTick();
    assert.equal(c.draft.value, "$code-review ");
    assert.equal(c.skillCommand.value, null);
    assert.equal(c.sends(), 0);
    assert.equal(c.input.selectionStart, c.draft.value.length);
});

test('技能标签只改变显示，保留 canonical 引用、原文、换行与多次引用', () => {
    const skills = [{ name: 'kami', alias: '紙', description: '排版' }, { name: 'code-review' }];
    const text = '请用 $kami 查看\n$code-review 然后再用 $kami。';
    const parts = splitSkillReferences(text, skills);
    assert.equal(parts.map(part => part.text).join(''), text);
    assert.deepEqual(parts.filter(part => part.type === 'skill').map(part => part.name), ['kami', 'code-review', 'kami']);
    assert.equal(parts.find(part => part.type === 'skill').skill.alias, '紙');
    for (const part of parts) assert.equal(text.slice(part.start, part.end), part.text);
});

test('美元、未知技能、路径、代码与相似前缀不能误转为技能标签', () => {
    const skills = [{ name: 'kami' }, { name: 'code' }, { name: 'code-review' }, { name: 'a.b' }];
    const text = '$100 $missing $kami-extra /tmp/$kami https://example.com/$kami `$kami` ```\n$kami\n```';
    assert.equal(splitSkillReferences(text, skills).every(part => part.type === 'text'), true);
    assert.deepEqual(splitSkillReferences('$code-review $a.b', skills).filter(part => part.type === 'skill').map(part => part.name), ['code-review', 'a.b']);
    assert.equal(splitSkillReferences('$aXb', skills)[0].type, 'text');
});

test("中文组词 Enter、Shift+Enter 和 Shift+Tab 保留默认行为；Esc 只关闭菜单", () => {
    const c = composer();
    for (const event of [key("Enter", { isComposing: true }), key("Enter", { keyCode: 229 }), key("Enter", { shiftKey: true }), key("Tab", { shiftKey: true })]) {
        c.handleComposerKey(event);
        assert.equal(event.prevented, undefined);
    }
    c.handleComposerKey(key("Escape"));
    assert.equal(c.skillCommand.value, null);
    assert.equal(c.draft.value, "/skill");
    assert.equal(c.sends(), 0);
});

test("加载、失败或没有可用技能时 Enter 不发送；旧菜单选择不能引用已停用技能", async () => {
    const c = composer();
    for (const state of [{ loading: true, loadError: "" }, { loading: false, loadError: "failed" }]) {
        Object.assign(c.skillStore, state);
        const event = key("Enter"); c.handleComposerKey(event);
        assert.equal(event.prevented, true);
    }
    Object.assign(c.skillStore, { loading: false, loadError: "" });
    c.skillMatches.value = [];
    c.handleComposerKey(key("Enter"));
    await c.chooseSkill({ name: "kami" });
    assert.equal(c.draft.value, "/skill");
    assert.equal(c.sends(), 0);
});
