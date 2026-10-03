import assert from "node:assert/strict";
import test from "node:test";
import { createFeatureRegistry } from "../features/registry.js";
import { settingsFeatures, settingsGroups } from "../features/settings.js";
import { workspaceFeatures } from "../features/workspaces.js";
import { normalizeRuntimeManifest } from "../runtime/projections.js";
import { t } from "../i18n/index.js";

test("页面注册表保持既有导航与设置入口，同时允许追加独立模块", () => {
  assert.deepEqual(workspaceFeatures.items.map((item) => item.key), ["tasks", "skills", "connectors"]);
  assert.equal(settingsFeatures.items.length, 11);
  for (const group of settingsGroups) for (const item of group.items) {
    assert.equal(item, settingsFeatures.get(item.key));
    assert.equal(typeof item.load, "function");
  }
  const definitions = [...workspaceFeatures.items, { key: "demo", load: async () => ({ default: {} }) }];
  const extended = createFeatureRegistry(definitions);
  assert.equal(extended.get("demo").key, "demo");
  assert.equal(workspaceFeatures.get("demo"), undefined);
  assert.throws(() => createFeatureRegistry([...definitions, definitions[0]]));
});

test("导航移入页面定义后仍按当前语言翻译", () => {
  for (const feature of workspaceFeatures.items) {
    assert.ok(t(feature.title));
    assert.ok(t(feature.description));
  }
});

test("能力面板兼容旧后端并复制模块摘要，不能修改冻结投影", () => {
  assert.deepEqual(normalizeRuntimeManifest({}).extensions, []);
  const raw = { extensions: [{ id: "demo", revision: "v1", toolNames: [" demo_read "] }] };
  const manifest = normalizeRuntimeManifest(raw);
  raw.extensions[0].toolNames.push("demo_write");
  assert.deepEqual(manifest.extensions, [{ id: "demo", revision: "v1", toolNames: ["demo_read"] }]);
});
