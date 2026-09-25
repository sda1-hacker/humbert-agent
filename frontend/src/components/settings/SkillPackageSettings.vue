<script setup>
import {
  computed,
  onMounted,
  ref,
} from "vue";

import { Message } from "../../utils/uiMessage.js";
import { t } from "../../i18n/index.js";

import {
  IconFolder,
  IconPlus,
  IconRefresh,
  IconSearch,
} from "@arco-design/web-vue/es/icon";

import {
  discoverLocalSkillSource,
  discoverSkillSource,
  getSkillState,
  installDiscoveredSkillsFromDirectory,
  installDiscoveredSkillsFromURL,
  selectSkillDirectory,
} from "../../api/skills.js";

import SectionCard from "../ui/SectionCard.vue";
import EmptyState from "../ui/EmptyState.vue";
import StatusPill from "../ui/StatusPill.vue";

const emit = defineEmits([
  "open-detail",
]);

const loading = ref(false);
const loadError = ref("");
const sourceError = ref("");
const rootDir = ref("");
const skills = ref([]);
const search = ref("");
const installPanelVisible = ref(false);
const installAdvancedVisible = ref(false);
const remoteURL = ref("");
const remoteSkillPath = ref("");
const discovery = ref(null);
const discoveryContext = ref(null);
const selectedDiscoveryPaths = ref([]);
const discovering = ref(false);
const installingDiscovered = ref(false);

const visibleSkills = computed(() => {
  const keyword = search.value.trim().toLocaleLowerCase();
  if (!keyword) return skills.value;

  return skills.value.filter((skill) => [
    skill?.alias,
    skill?.name,
    skill?.directoryName,
    skill?.description,
    skill?.source?.provider,
  ]
      .filter(Boolean)
      .join("\n")
      .toLocaleLowerCase()
      .includes(keyword));
});

const discoveryCandidates = computed(() =>
    Array.isArray(discovery.value?.candidates) ? discovery.value.candidates : [],
);

const installableDiscoveryCandidates = computed(() =>
    discoveryCandidates.value.filter((candidate) => candidate?.valid && !candidate?.installed),
);

function displayName(skill) {
  return skill?.alias?.trim() || skill?.name || skill?.directoryName || t("未命名 Skill");
}

function usedByAgents(skill) {
  return Array.isArray(skill?.usedByAgents) ? skill.usedByAgents : [];
}

function skillStatus(skill) {
  if (!skill?.valid) return { label: "无效", tone: "danger" };
  if (skill?.runtimeStatus === "unsupported") return { label: "暂不可启用", tone: "warning" };
  if (skill?.runtimeStatus === "needs_setup") return { label: "需要配置", tone: "warning" };
  if (skill?.specStatus === "legacy") return { label: "兼容模式", tone: "neutral" };
  return { label: "可用", tone: "success" };
}

function discoveryCandidateStatus(candidate) {
  if (!candidate?.valid) return { label: "无效", tone: "danger" };
  if (candidate?.installed) return { label: "已安装", tone: "neutral" };
  if (candidate?.runtimeStatus === "unsupported") return { label: "可安装 · 暂不可启用", tone: "warning" };
  if (candidate?.runtimeStatus === "needs_setup") return { label: "可安装 · 需要配置", tone: "warning" };
  if (candidate?.specStatus === "legacy") return { label: "可安装 · 兼容模式", tone: "neutral" };
  return { label: "可安装", tone: "success" };
}

function normalizeState(state) {
  return {
    rootDir: state?.rootDir ?? "",
    sourceError: typeof state?.sourceError === "string" ? state.sourceError : "",
    skills: Array.isArray(state?.skills)
        ? state.skills.map((skill) => ({
          ...skill,
          alias: typeof skill?.alias === "string" ? skill.alias : "",
          diagnostics: Array.isArray(skill?.diagnostics) ? skill.diagnostics : [],
          scriptRuntimes: Array.isArray(skill?.scriptRuntimes) ? skill.scriptRuntimes : [],
          usedByAgents: Array.isArray(skill?.usedByAgents) ? skill.usedByAgents : [],
          source: skill?.source && typeof skill.source === "object"
              ? skill.source
              : { known: false },
        }))
        : [],
  };
}

function normalizeDiscovery(value) {
  return {
    sourceKind: value?.sourceKind ?? "",
    provider: value?.provider ?? "",
    displaySource: value?.displaySource ?? "",
    candidates: Array.isArray(value?.candidates)
        ? value.candidates.map((candidate) => ({
          ...candidate,
          diagnostics: Array.isArray(candidate?.diagnostics) ? candidate.diagnostics : [],
          scriptRuntimes: Array.isArray(candidate?.scriptRuntimes) ? candidate.scriptRuntimes : [],
        }))
        : [],
  };
}

function resetDiscovery() {
  discovery.value = null;
  discoveryContext.value = null;
  selectedDiscoveryPaths.value = [];
}

function applyDiscovery(result, context) {
  discovery.value = normalizeDiscovery(result);
  discoveryContext.value = context;
  selectedDiscoveryPaths.value = discovery.value.candidates
      .filter((candidate) => candidate?.valid && !candidate?.installed)
      .map((candidate) => candidate.path);
}

async function load() {
  if (loading.value) return;

  loading.value = true;
  loadError.value = "";
  sourceError.value = "";

  try {
    const state = normalizeState(await getSkillState());
    rootDir.value = state.rootDir;
    sourceError.value = state.sourceError;
    skills.value = state.skills;
  } catch (error) {
    loadError.value = error?.message ?? String(error);
    console.error("[SkillPackageSettings] 加载 Skills 失败", error);
  } finally {
    loading.value = false;
  }
}

function openDetail(skill) {
  const name = skill?.name || skill?.directoryName || "";
  if (name) emit("open-detail", name);
}

async function discoverLocalPackages() {
  if (discovering.value) return;

  try {
    const selected = await selectSkillDirectory();
    if (!selected) return;
    discovering.value = true;
    resetDiscovery();
    const result = await discoverLocalSkillSource(selected);
    applyDiscovery(result, { kind: "local", source: selected });
    if (discoveryCandidates.value.length === 0) {
      Message.warning("这个目录中没有发现 SKILL.md");
    }
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    discovering.value = false;
  }
}

async function discoverRemotePackages() {
  const sourceURL = remoteURL.value.trim();
  if (!sourceURL || discovering.value) {
    if (!sourceURL) Message.warning("请输入公开 HTTPS Skill 地址");
    return;
  }

  discovering.value = true;
  resetDiscovery();
  try {
    const result = await discoverSkillSource(sourceURL, remoteSkillPath.value.trim());
    applyDiscovery(result, { kind: "remote", source: sourceURL });
    if (discoveryCandidates.value.length === 0) {
      Message.warning("这个来源中没有发现 SKILL.md");
    }
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    discovering.value = false;
  }
}

function selectAllInstallable() {
  selectedDiscoveryPaths.value = installableDiscoveryCandidates.value.map((candidate) => candidate.path);
}

async function installSelectedPackages() {
  const paths = selectedDiscoveryPaths.value.filter(Boolean);
  const context = discoveryContext.value;
  if (!context || paths.length === 0 || installingDiscovered.value) return;

  installingDiscovered.value = true;
  try {
    let result;
    if (context.kind === "local") {
      result = await installDiscoveredSkillsFromDirectory(context.source, paths);
    } else {
      result = await installDiscoveredSkillsFromURL(context.source, paths);
    }
    const count = Array.isArray(result) ? result.length : paths.length;
    await load();
    resetDiscovery();
    remoteURL.value = "";
    remoteSkillPath.value = "";
    installPanelVisible.value = false;
    Message.success(`已安装 ${count} 个 Skill`);
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    installingDiscovered.value = false;
  }
}

onMounted(load);
</script>

<template>
  <div class="skill-package-settings">
    <SectionCard
        class="skill-package-panel"
        title="本地 Skill Packages"
        description="这里管理安装到 Humbert 的 Skill Package。为某个 Agent 开启或关闭 Skill，请使用左侧「技能」工作区。"
    >
      <template #actions>
        <div class="panel-actions">
          <a-button :loading="loading" @click="load">
            <template #icon><IconRefresh /></template>
            刷新
          </a-button>
          <a-button type="primary" @click="installPanelVisible = !installPanelVisible">
            <template #icon><IconPlus /></template>
            安装 Skill
          </a-button>
        </div>
      </template>

      <div v-if="loadError" class="inline-error">
        <div>
          <strong>Skills 数据加载失败</strong>
          <span>{{ loadError }}</span>
        </div>
        <a-button size="small" @click="load">重试</a-button>
      </div>

      <a-alert v-if="sourceError" type="warning" :show-icon="true">
        {{ sourceError }}。Skill 列表仍可使用，但来源更新/修复功能会暂时不可用。
      </a-alert>

      <div v-if="installPanelVisible" class="installer-panel">
        <div class="installer-panel__top">
          <div>
            <strong>发现 / 安装 Skills</strong>
            <span>粘贴仓库、skills.sh 或 ZIP 地址，Humbert 会先发现其中的 Skills，再由你选择安装。</span>
          </div>

          <a-button :loading="discovering" @click="discoverLocalPackages">
            <template #icon><IconFolder /></template>
            扫描本地目录
          </a-button>
        </div>

        <div class="installer-panel__row">
          <a-input
              v-model="remoteURL"
              allow-clear
              placeholder="https://github.com/...、skills.sh/... 或公开 HTTPS ZIP"
              @press-enter="discoverRemotePackages"
          />
          <a-button
              type="primary"
              :loading="discovering"
              :disabled="!remoteURL.trim()"
              @click="discoverRemotePackages"
          >
            扫描来源
          </a-button>
        </div>

        <button
            type="button"
            class="advanced-toggle"
            @click="installAdvancedVisible = !installAdvancedVisible"
        >
          {{ installAdvancedVisible ? "收起高级选项" : "高级选项" }}
        </button>

        <div v-if="installAdvancedVisible" class="installer-advanced">
          <a-input
              v-model="remoteSkillPath"
              allow-clear
              placeholder="可选：只扫描仓库子目录，例如 skills/pdf"
          />
          <p>留空时自动发现仓库中的 SKILL.md。扫描和安装都统一经过 SSRF、下载大小、归档与 Package 安全校验。</p>
        </div>

        <div v-if="discovery" class="discovery-panel">
          <div class="discovery-panel__header">
            <div>
              <strong>发现 {{ discoveryCandidates.length }} 个 Skill</strong>
              <span>{{ discovery.provider || discovery.sourceKind || "Skill Source" }} · {{ discovery.displaySource || discoveryContext?.source }}</span>
            </div>
            <button
                v-if="installableDiscoveryCandidates.length > 0"
                type="button"
                class="advanced-toggle discovery-select-all"
                @click="selectAllInstallable"
            >
              选择全部可安装
            </button>
          </div>

          <a-checkbox-group v-model="selectedDiscoveryPaths" class="discovery-list">
            <div
                v-for="candidate in discoveryCandidates"
                :key="candidate.path"
                class="discovery-item"
                :class="{ 'discovery-item--invalid': !candidate.valid }"
            >
              <a-checkbox
                  :value="candidate.path"
                  :disabled="!candidate.valid || candidate.installed"
              />
              <div class="discovery-item__body">
                <div class="discovery-item__title">
                  <strong>{{ candidate.name || candidate.path }}</strong>
                  <code>{{ candidate.path }}</code>
                  <StatusPill
                      :label="t(discoveryCandidateStatus(candidate).label)"
                      :tone="discoveryCandidateStatus(candidate).tone"
                      :dot="false"
                  />
                </div>
                <p :class="{ 'discovery-item__error': !candidate.valid }">
                  {{ candidate.valid ? (candidate.description || "没有描述") : (candidate.error || "Skill Package 校验失败") }}
                </p>
                <div class="discovery-item__meta">
                  <span v-if="candidate.valid">{{ candidate.fileCount }} 个文件</span>
                  <span v-if="candidate.hasScripts">scripts</span>
                  <span v-if="candidate.hasReferences">references</span>
                  <span v-if="candidate.hasAssets">assets</span>
                  <span v-if="candidate.runtimeMessage">{{ candidate.runtimeMessage }}</span>
                  <span v-else-if="candidate.specMessage">{{ candidate.specMessage }}</span>
                </div>
              </div>
            </div>
          </a-checkbox-group>

          <div class="discovery-panel__actions">
            <span>选择了 {{ selectedDiscoveryPaths.length }} 个</span>
            <a-button
                type="primary"
                :loading="installingDiscovered"
                :disabled="selectedDiscoveryPaths.length === 0"
                @click="installSelectedPackages"
            >
              安装所选 Skills
            </a-button>
          </div>
        </div>
      </div>

      <div class="catalog-toolbar">
        <a-input v-model="search" allow-clear placeholder="搜索显示名称、Skill 名称、描述或来源">
          <template #prefix><IconSearch /></template>
        </a-input>
        <span>{{ visibleSkills.length }} / {{ skills.length }}</span>
      </div>

      <div class="catalog-hint">
        <span aria-hidden="true">↗</span>
        <span>点击 Skill 可在独立工作区查看 SKILL.md / references / scripts、修改显示名称、检查更新或修复 Package。</span>
      </div>

      <EmptyState
          v-if="loading && skills.length === 0"
          title="正在读取本地 Skills…"
          description="正在扫描已安装的 Skill Package。"
          compact
      />

      <div v-else-if="visibleSkills.length > 0" class="package-list">
        <button
            v-for="skill in visibleSkills"
            :key="skill.directoryName || skill.name"
            type="button"
            class="package-row"
            :class="{ 'package-row--invalid': !skill.valid, 'package-row--unsupported': skill.valid && ['unsupported', 'needs_setup'].includes(skill.runtimeStatus) }"
            @click="openDetail(skill)"
        >
          <div class="package-row__body">
            <div class="package-row__title">
              <strong>{{ displayName(skill) }}</strong>
              <code v-if="skill.alias">{{ skill.name }}</code>
              <StatusPill
                  :label="t(skillStatus(skill).label)"
                  :tone="skillStatus(skill).tone"
                  :dot="false"
              />
            </div>

            <p :class="{ 'package-row__error': !skill.valid }">
              {{ skill.valid ? skill.description : (skill.error || "Skill Package 校验失败") }}
            </p>

            <div class="package-row__meta">
              <span v-if="skill.valid">{{ skill.fileCount }} 个文件</span>
              <span v-if="skill.hasScripts">scripts</span>
              <span v-if="skill.hasReferences">references</span>
              <span v-if="skill.hasAssets">assets</span>
              <span v-if="skill.valid && ['unsupported', 'needs_setup'].includes(skill.runtimeStatus)" class="package-row__runtime-warning">
                {{ skill.runtimeMessage || (skill.runtimeStatus === 'needs_setup' ? "需要补充运行环境" : "当前 Runtime 暂不支持启用") }}
              </span>
              <span v-if="skill.source?.known">来源：{{ skill.source.provider || "已记录" }}</span>
              <span>{{ usedByAgents(skill).length }} 个 Agent 已启用</span>
              <span v-if="!skill.valid && skill.source?.known">可从来源修复</span>
            </div>
          </div>

          <div class="package-row__action">
            <span>查看详情</span>
            <span aria-hidden="true">›</span>
          </div>
        </button>
      </div>

      <EmptyState
          v-else
          :title="skills.length > 0 ? '没有匹配的 Skill' : '还没有安装 Skill'"
          :description="skills.length > 0 ? '尝试调整搜索关键词。' : '点击上方「安装 Skill」添加第一个 Package。'"
          compact
      />

      <footer class="package-footer">
        <span>Skill Package 存储目录</span>
        <code>{{ rootDir || "~/.humbert-agent/skills" }}</code>
      </footer>
    </SectionCard>
  </div>
</template>

<style scoped>
.skill-package-settings {
  min-width: 0;
}

.panel-actions {
  display: flex;
  flex: 0 0 auto;
  gap: 8px;
}

.inline-error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin: 14px 0 0;
  padding: 12px 14px;
  border: 1px solid color-mix(in srgb, var(--h-danger) 35%, transparent);
  border-radius: 10px;
  background: color-mix(in srgb, var(--h-danger) 6%, var(--h-surface));
}

.inline-error > div {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 3px;
}

.inline-error strong {
  color: var(--h-text);
  font-size: 11px;
}

.inline-error span {
  color: var(--h-danger);
  font-size: 9px;
}

.installer-panel {
  margin: 14px 0 0;
  padding: 15px;
  border: 1px solid var(--h-border);
  border-radius: 12px;
  background: var(--h-surface-hover);
}

.installer-panel__top,
.installer-panel__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.installer-panel__top > div {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 3px;
}

.installer-panel__top strong {
  color: var(--h-text);
  font-size: 11px;
}

.installer-panel__top span,
.installer-advanced p {
  color: var(--h-text-muted);
  font-size: 9px;
  line-height: 1.55;
}

.installer-panel__row {
  margin-top: 12px;
}

.advanced-toggle {
  margin-top: 9px;
  border: 0;
  padding: 0;
  background: transparent;
  color: var(--h-accent-hover);
  cursor: pointer;
  font: inherit;
  font-size: 9px;
}

.installer-advanced {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 9px;
}

.installer-advanced p {
  margin: 0;
}

.discovery-panel {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-top: 14px;
  border-top: 1px solid var(--h-border);
  padding-top: 14px;
}

.discovery-panel__header,
.discovery-panel__actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.discovery-panel__header > div {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 3px;
}

.discovery-panel__header strong {
  color: var(--h-text);
  font-size: 11px;
}

.discovery-panel__header span,
.discovery-panel__actions > span {
  overflow: hidden;
  color: var(--h-text-muted);
  font-size: 9px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.discovery-select-all {
  flex: 0 0 auto;
  margin-top: 0;
}

.discovery-list {
  display: flex;
  width: 100%;
  flex-direction: column;
  border: 1px solid var(--h-border);
  border-radius: 10px;
  background: var(--h-surface);
}

.discovery-item {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: flex-start;
  gap: 10px;
  padding: 11px 12px;
}

.discovery-item + .discovery-item {
  border-top: 1px solid var(--h-border);
}

.discovery-item--invalid {
  background: color-mix(in srgb, var(--h-danger) 3%, transparent);
}

.discovery-item__body {
  min-width: 0;
}

.discovery-item__title,
.discovery-item__meta {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 7px;
}

.discovery-item__title strong {
  color: var(--h-text);
  font-size: 10px;
}

.discovery-item__title code,
.discovery-item__meta {
  color: var(--h-text-muted);
  font-size: 8px;
}

.discovery-item p {
  margin: 4px 0 0;
  color: var(--h-text-secondary);
  font-size: 9px;
  line-height: 1.5;
}

.discovery-item p.discovery-item__error {
  color: var(--h-danger);
}

.discovery-item__meta {
  margin-top: 6px;
}

.discovery-panel__actions {
  border-top: 1px solid var(--h-border);
  padding-top: 10px;
}

.catalog-toolbar {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 12px;
  padding: 14px 0 10px;
}

.catalog-toolbar > span,
.catalog-hint {
  color: var(--h-text-muted);
  font-size: 9px;
}

.catalog-hint {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  padding: 0 0 12px;
  line-height: 1.55;
}

.catalog-hint > span:first-child {
  color: var(--h-accent);
}

.package-list {
  border-top: 1px solid var(--h-border);
}

.package-row {
  display: grid;
  width: 100%;
  min-width: 0;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 20px;
  border: 0;
  padding: 15px 0;
  background: transparent;
  cursor: pointer;
  text-align: left;
  transition: background-color 120ms ease;
}

.package-row + .package-row {
  border-top: 1px solid var(--h-border);
}

.package-row:hover {
  background: var(--h-surface-hover);
}

.package-row--invalid {
  background: color-mix(in srgb, var(--h-danger) 3%, transparent);
}

.package-row--unsupported {
  background: color-mix(in srgb, var(--h-warning, #f59e0b) 4%, transparent);
}

.package-row__body {
  min-width: 0;
}

.package-row__title {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 8px;
}

.package-row__title strong {
  color: var(--h-text);
  font-size: 12px;
  font-weight: 600;
}

.package-row__title code {
  color: var(--h-text-muted);
  font-size: 9px;
}

.package-row__body p {
  display: -webkit-box;
  max-width: 820px;
  margin: 5px 0 0;
  overflow: hidden;
  color: var(--h-text-secondary);
  font-size: 10px;
  line-height: 1.55;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.package-row__body p.package-row__error {
  color: var(--h-danger);
}

.package-row__runtime-warning {
  color: var(--h-warning, #b7791f);
}

.package-row__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 7px;
  color: var(--h-text-muted);
  font-size: 9px;
}

.package-row__action {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 6px;
  color: var(--h-accent-hover);
  font-size: 10px;
}

.package-footer {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  border-top: 1px solid var(--h-border);
  padding: 12px 0 0;
  color: var(--h-text-muted);
  font-size: 9px;
}

.package-footer code {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 820px) {
  .installer-panel__top,
  .installer-panel__row,
  .package-footer {
    align-items: stretch;
    flex-direction: column;
  }

  .panel-actions {
    flex-wrap: wrap;
  }
}
</style>
