<script setup>
import {
  computed,
} from "vue";

import { Message } from "../../utils/uiMessage.js";
import { t } from "../../i18n/index.js";

import {
  selectSandboxDirectory,
} from "../../api/agents.js";

const props = defineProps({
  sandbox: {
    type: Object,
    required: true,
  },
  enabledBuiltinTools: {
    type: Array,
    default: () => [],
  },
  availableBuiltinTools: {
    type: Array,
    default: () => [],
  },
  sandboxStatus: {
    type: Object,
    default: null,
  },
  showBuiltinTools: {
    type: Boolean,
    default: true,
  },
});

const emit = defineEmits([
  "update:sandbox",
  "update:enabledBuiltinTools",
]);

const effectiveProfile = computed(() =>
    props.sandbox?.profile || props.sandboxStatus?.defaultProfile || "standard",
);

const effectiveNetwork = computed(() =>
    props.sandbox?.networkMode || props.sandboxStatus?.defaultNetworkMode || "public",
);

const computerEnabled = computed(() => props.enabledBuiltinTools.includes("computer"));
const computerAllowed = computed(() => effectiveProfile.value === "full_access" && effectiveNetwork.value === "all");

function allowComputer() {
  // 工具勾选不会偷偷扩大沙盒；用户需要在说明旁明确选择桌面访问范围。
  patchSandbox({ profile: "full_access", networkMode: "all" });
}

const followsGlobal = computed(() =>
    !props.sandbox?.profile &&
    !props.sandbox?.networkMode &&
    !props.sandbox?.nativeMode,
);

const localProgramHint = computed(() => {
  if (!props.sandboxStatus) return "";
  if (props.sandboxStatus.shellEnabled && props.sandboxStatus.shellRuntimeActive) {
    return "本地程序能力已启用；Python、Git、Node 等仍需通过操作确认和沙盒约束。";
  }
  if (props.sandboxStatus.shellEnabled && !props.sandboxStatus.shellRuntimeActive) {
    return "本地程序已经配置为开启，重启 Humbert 后生效。";
  }
  return "本地程序当前关闭。如需运行 Python、Git 或 Node，请到“设置 → 安全”开启，保存后重启 Humbert。";
});

const fileProtectionLabel = computed(() => ({
  standard: "标准保护",
  workspace_only: "严格保护",
  full_access: "不受限制",
})[effectiveProfile.value] ?? "标准保护");

const fileProtectionDescription = computed(() => {
  switch (effectiveProfile.value) {
    case "workspace_only":
      return "仅工作目录可访问，适合不需要读取其他本机资料的 Agent。";
    case "full_access":
      return "文件访问退回当前系统用户权限。仅在你明确需要时使用。";
    default:
      return "工作目录可读写；用户目录中的普通文件只读；密钥等敏感目录仍会被阻止。";
  }
});

const builtinSelection = computed({
  get: () => props.enabledBuiltinTools ?? [],
  set: (values) => emit("update:enabledBuiltinTools", [...values]),
});

const builtinToolGroups = computed(() => {
  const groups = new Map();
  for (const tool of props.availableBuiltinTools ?? []) {
    const key = tool.category || "other";
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(tool);
  }
  return [...groups.entries()].map(([key, tools]) => ({
    key,
    title: ({
      files: "文件与代码",
      execution: "本地执行",
      git: "Git",
      web: "网络",
      skills: "技能",
      collaboration: "Agent 协作",
      agent: "Agent 辅助",
      other: "其他",
    })[key] ?? key,
    tools,
  }));
});

function patchSandbox(patch) {
  emit("update:sandbox", {
    profile: props.sandbox?.profile ?? "",
    additionalWritePaths: [...(props.sandbox?.additionalWritePaths ?? [])],
    networkMode: props.sandbox?.networkMode ?? "",
    nativeMode: props.sandbox?.nativeMode ?? "",
    ...patch,
  });
}

function followGlobalSettings() {
  patchSandbox({
    profile: "",
    networkMode: "",
    nativeMode: "",
  });
}

function setAgentProtection(value) {
  patchSandbox({profile: value});
}

function setAgentNetwork(enabled) {
  patchSandbox({networkMode: enabled ? "public" : "none"});
}

async function addWritablePath() {
  try {
    const selected = await selectSandboxDirectory("");
    if (!selected) return;

    const current = [...(props.sandbox?.additionalWritePaths ?? [])];
    if (!current.includes(selected)) current.push(selected);
    patchSandbox({additionalWritePaths: current});
  } catch (error) {
    Message.error(error?.message ?? String(error));
  }
}

function removeWritablePath(index) {
  const current = [...(props.sandbox?.additionalWritePaths ?? [])];
  current.splice(index, 1);
  patchSandbox({additionalWritePaths: current});
}
</script>

<template>
  <div class="security-editor">
    <section class="security-summary">
      <div>
        <strong>文件保护</strong>
        <span>{{ fileProtectionDescription }}</span>
      </div>
      <div class="security-summary__right">
        <span class="security-pill">{{ fileProtectionLabel }}</span>
        <a-button
            v-if="!followsGlobal"
            size="mini"
            type="text"
            @click="followGlobalSettings"
        >
          跟随全局设置
        </a-button>
      </div>
    </section>

    <div v-if="localProgramHint" class="local-program-note">
      <strong>本地程序</strong>
      <span>{{ localProgramHint }}</span>
    </div>

    <div v-if="computerEnabled" class="local-program-note">
      <strong>{{ t('桌面操作') }}</strong>
      <span>{{ t('桌面操作可影响应用中的文件、账号和网络，无法由文件沙盒隔离；截图会发送给视觉模型。需要明确允许不受限制的访问，并授予系统截图与控制权限。') }}</span>
      <a-button v-if="!computerAllowed" size="small" @click="allowComputer">{{ t('允许桌面操作') }}</a-button>
      <span v-else>{{ t('桌面访问已允许；操作确认仍会生效。') }}</span>
    </div>

    <details class="advanced-panel">
      <summary>高级设置</summary>

      <div class="advanced-panel__body">
        <div class="security-row">
          <div>
            <div class="security-label">这个 Agent 的文件保护</div>
            <div class="security-help">
              通常保持“跟随全局设置”即可。严格保护只允许访问工作目录；不受限制会显著扩大文件访问范围。
            </div>
          </div>
          <a-select
              :model-value="sandbox.profile || ''"
              class="security-select"
              @update:model-value="setAgentProtection"
          >
            <a-option value="">跟随全局设置</a-option>
            <a-option value="standard">标准保护</a-option>
            <a-option value="workspace_only">严格保护</a-option>
            <a-option value="full_access">不受限制</a-option>
          </a-select>
        </div>

        <div class="security-row">
          <div>
            <div class="security-label">这个 Agent 允许联网</div>
            <div class="security-help">
              默认跟随全局安全设置。关闭后，Humbert 的网络型工具会被禁止；本地子进程的网络限制仍取决于当前平台能力。
            </div>
          </div>
          <div class="network-control">
            <span>{{ effectiveNetwork === 'none' ? '关闭' : '开启' }}</span>
            <a-switch
                :model-value="effectiveNetwork !== 'none'"
                @change="setAgentNetwork"
            />
          </div>
        </div>

        <div class="path-policy path-policy--advanced">
          <div class="path-policy__header">
            <div>
              <div class="security-label">允许修改其他目录</div>
              <div class="security-help">
                Agent 可以读取、创建和修改这些目录中的文件，但不会获得删除或移动权限。一般 Agent 通常不需要添加。
              </div>
            </div>
          </div>

          <div
              v-for="(path, index) in sandbox.additionalWritePaths || []"
              :key="`write-${path}`"
              class="path-policy__row"
          >
            <code>{{ path }}</code>
            <a-button type="text" size="mini" @click="removeWritablePath(index)">移除</a-button>
          </div>

          <a-button size="small" type="secondary" @click="addWritablePath">
            添加可修改目录
          </a-button>
        </div>

        <div v-if="showBuiltinTools" class="builtin-tools">
          <div>
            <div class="security-label">内置工具</div>
            <div class="security-help">
              新建 Agent 默认开启桌面操作、技能安装和 Agent 协作，可在下方逐项关闭。文件保护和操作确认仍会继续生效。
            </div>
          </div>

          <div v-for="group in builtinToolGroups" :key="group.key" class="builtin-tools__group">
            <div class="builtin-tools__title">{{ group.title }}</div>
            <a-checkbox-group
                v-model="builtinSelection"
                class="builtin-tools__grid"
            >
              <a-checkbox v-for="tool in group.tools" :key="tool.name" :value="tool.name">
                <span>{{ t(tool.label || tool.name) }}</span>
              </a-checkbox>
            </a-checkbox-group>
          </div>
        </div>
      </div>
    </details>
  </div>
</template>

<style scoped>
.security-editor {
  display: grid;
  width: 100%;
  gap: 16px;
}

.security-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  padding: 14px;
  border: 1px solid var(--h-border);
  border-radius: 10px;
  background: var(--h-surface);
}

.security-summary > div:first-child {
  display: grid;
  min-width: 0;
  gap: 5px;
}

.security-summary strong {
  color: var(--h-text);
  font-size: 12px;
}

.security-summary span {
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.6;
}

.security-summary__right {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 6px;
}

.security-pill {
  padding: 4px 9px;
  border: 1px solid var(--h-border);
  border-radius: 999px;
  color: var(--h-text-secondary) !important;
  font-size: 9px !important;
  font-weight: 650;
}

.security-label {
  color: var(--h-text);
  font-size: 12px;
  font-weight: 650;
}

.security-help {
  margin-top: 4px;
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.6;
}

.path-policy {
  display: grid;
  gap: 8px;
}

.path-policy__header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
}

.path-policy__row {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
}

.path-policy__row code {
  min-width: 0;
  flex: 1;
  overflow: hidden;
  padding: 8px 10px;
  border: 1px solid var(--h-border);
  border-radius: 8px;
  color: var(--h-text-secondary);
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.local-program-note {
  display: grid;
  gap: 4px;
  padding: 10px 12px;
  border: 1px solid var(--h-border);
  border-radius: 9px;
  background: var(--h-surface-soft, var(--h-surface));
}

.local-program-note strong {
  color: var(--h-text);
  font-size: 11px;
}

.local-program-note span {
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.6;
}

.advanced-panel {
  border-top: 1px solid var(--h-border);
  padding-top: 12px;
}

.advanced-panel > summary {
  width: fit-content;
  cursor: pointer;
  color: var(--h-text-secondary);
  font-size: 11px;
  font-weight: 650;
  user-select: none;
}

.advanced-panel__body {
  display: grid;
  gap: 16px;
  margin-top: 14px;
}

.security-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(200px, 250px);
  align-items: center;
  gap: 16px;
}

.security-select {
  width: 100%;
}

.network-control {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
  color: var(--h-text-muted);
  font-size: 10px;
}

.path-policy--advanced {
  padding-top: 4px;
}

.builtin-tools {
  display: grid;
  gap: 12px;
  padding-top: 4px;
}

.builtin-tools__group {
  display: grid;
  gap: 8px;
}

.builtin-tools__title {
  color: var(--h-text-secondary);
  font-size: 10px;
  font-weight: 650;
}

.builtin-tools__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px 12px;
}

.builtin-tools__grid :deep(.arco-checkbox) {
  margin-right: 0;
}

@media (max-width: 760px) {
  .security-summary,
  .security-row {
    grid-template-columns: 1fr;
  }

  .security-summary {
    align-items: flex-start;
    flex-direction: column;
  }

  .network-control {
    justify-content: flex-start;
  }

  .builtin-tools__grid {
    grid-template-columns: 1fr;
  }
}
</style>
