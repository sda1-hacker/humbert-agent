<script setup>
import {
  computed,
  onMounted,
  reactive,
  ref,
} from "vue";

import { Message } from "../../utils/uiMessage.js";

import {
  getSandboxStatus,
  runSandboxDiagnostics,
  updateSandboxSettings,
} from "../../api/agents.js";

import SectionCard from "../ui/SectionCard.vue";
import StatusPill from "../ui/StatusPill.vue";
import EmptyState from "../ui/EmptyState.vue";

const loading = ref(true);
const saving = ref(false);
const diagnosing = ref(false);
const error = ref("");
const status = ref(null);
const diagnostics = ref(null);

const form = reactive({
  defaultProfile: "standard",
  defaultNetworkMode: "public",
  defaultNativeMode: "preferred",
  commandGracePeriodMS: 1500,
  shellEnabled: false,
});

const initialShell = reactive({
  enabled: false,
});

const sandboxEnabled = computed({
  get: () => form.defaultProfile !== "full_access",
  set: (enabled) => {
    form.defaultProfile = enabled ? "standard" : "full_access";
    form.defaultNativeMode = enabled ? "preferred" : "off";
  },
});

const networkEnabled = computed({
  get: () => form.defaultNetworkMode !== "none",
  set: (enabled) => {
    form.defaultNetworkMode = enabled ? "public" : "none";
  },
});

const strictMode = computed(() => form.defaultProfile === "workspace_only");

const localProgramIsolationNote = computed(() => {
  if (!form.shellEnabled) return "";
  if (form.defaultNativeMode === "off") {
    return "关闭原生隔离时，run_command 和 Skill 脚本无法保证工作区只读，因此会拒绝执行。";
  }
  if (status.value?.platform === "windows" && !status.value?.filesystem) {
    return "Windows 当前无法为任意本地命令保证工作区只读，因此 run_command 会拒绝执行。";
  }
  if (!status.value?.available || !status.value?.filesystem) {
    return "当前系统无法建立可靠的本地程序文件隔离，run_command 和 Skill 脚本会拒绝执行。";
  }
  return "";
});

const platformName = computed(() => ({
  darwin: "macOS",
  windows: "Windows",
  linux: "Linux",
})[status.value?.platform] ?? status.value?.platform ?? "未知平台");

const protectionStatus = computed(() => {
  if (!sandboxEnabled.value) {
    return {
      tone: "warning",
      label: "保护已关闭",
      description: "Agent 的文件访问将退回当前系统用户权限。",
    };
  }
  if (!status.value?.available) {
    return {
      tone: "warning",
      label: "部分保护",
      description: "应用层文件保护仍然生效，但当前系统的本地程序隔离能力不可用。",
    };
  }
  return {
    tone: "success",
    label: strictMode.value ? "严格保护" : "保护已开启",
    description: strictMode.value
        ? "当前只允许访问工作目录。你可以切换到标准保护，让普通用户文件保持只读可访问。"
        : "工作目录可读写；普通用户文件只读；密钥等敏感目录会被阻止。",
  };
});

const diagnosticsTone = computed(() => ({
  pass: "success",
  warning: "warning",
  fail: "danger",
})[diagnostics.value?.summary] ?? "neutral");

const diagnosticsLabel = computed(() => ({
  pass: "正常",
  warning: "需要注意",
  fail: "存在异常",
})[diagnostics.value?.summary] ?? "未知");

function applyStatus(value) {
  status.value = value ?? null;
  if (!value) return;
  form.defaultProfile = value.defaultProfile || "standard";
  form.defaultNetworkMode = value.defaultNetworkMode || "public";
  form.defaultNativeMode = value.defaultNativeMode || "preferred";
  form.commandGracePeriodMS = Number(value.commandGracePeriodMS || 1500);
  form.shellEnabled = Boolean(value.shellEnabled);
  initialShell.enabled = form.shellEnabled;
}

const shellNeedsRestart = computed(() => {
  return form.shellEnabled !== initialShell.enabled;
});

async function load() {
  loading.value = true;
  error.value = "";
  try {
    applyStatus(await getSandboxStatus());
  } catch (reason) {
    error.value = reason?.message ?? String(reason);
  } finally {
    loading.value = false;
  }
}

async function save() {
  if (saving.value) return;
  saving.value = true;
  try {
    const value = await updateSandboxSettings({
      defaultProfile: form.defaultProfile,
      defaultNetworkMode: form.defaultNetworkMode,
      defaultNativeMode: form.defaultNativeMode,
      commandGracePeriodMS: Number(form.commandGracePeriodMS),
      shellEnabled: form.shellEnabled,
    });
    const changedShell = shellNeedsRestart.value;
    applyStatus(value);
    Message.success(changedShell ? "安全设置已保存；本地程序设置将在重启 Humbert 后生效" : "安全设置已保存");
  } catch (reason) {
    Message.error(reason?.message ?? String(reason));
  } finally {
    saving.value = false;
  }
}

async function useStandardProtection() {
  form.defaultProfile = "standard";
  await save();
}

async function diagnose() {
  if (diagnosing.value) return;
  diagnosing.value = true;
  diagnostics.value = null;
  try {
    diagnostics.value = await runSandboxDiagnostics();
  } catch (reason) {
    Message.error(reason?.message ?? String(reason));
  } finally {
    diagnosing.value = false;
  }
}

function diagnosticStatusLabel(value) {
  switch (value) {
    case "pass": return "正常";
    case "warning": return "注意";
    case "fail": return "异常";
    default: return value || "未知";
  }
}

onMounted(load);
</script>

<template>
  <div class="sandbox-settings">
    <EmptyState
        v-if="loading"
        title="正在加载安全设置"
        description="Humbert 正在检查当前系统可以提供的保护能力。"
    />

    <EmptyState
        v-else-if="error"
        title="无法加载安全设置"
        :description="error"
    />

    <EmptyState
        v-else-if="!status"
        title="安全状态不可用"
        description="后端没有返回当前平台的安全能力。"
    />

    <template v-else>
      <SectionCard
          title="安全保护"
          description="默认情况下，工作目录可以正常读写；工作目录外的普通用户文件只读；密码、密钥和其他敏感目录会被阻止。"
      >
        <div class="security-status">
          <div>
            <StatusPill :tone="protectionStatus.tone" :label="protectionStatus.label" />
            <span>{{ protectionStatus.description }}</span>
          </div>
          <a-button
              v-if="strictMode"
              size="small"
              type="secondary"
              :loading="saving"
              @click="useStandardProtection"
          >
            使用标准保护
          </a-button>
        </div>

        <div class="simple-setting-list">
          <div class="simple-setting-row">
            <div>
              <strong>安全沙盒</strong>
              <span>
                开启后限制 Agent 对本机文件的修改范围。关闭后，文件访问会退回当前系统用户权限。
              </span>
            </div>
            <a-switch v-model="sandboxEnabled" />
          </div>

          <div class="simple-setting-row">
            <div>
              <strong>允许沙盒联网</strong>
              <span>
                允许 Agent、网络工具和受支持的本地程序访问公网。关闭后会禁止网络型能力。
              </span>
            </div>
            <a-switch v-model="networkEnabled" />
          </div>

          <div class="simple-setting-row">
            <div>
              <strong>允许运行本地程序</strong>
              <span>
                允许 Agent 在操作确认和原生沙盒约束下运行本地程序。命令对工作区只有读取权限，不能修改或删除文件。修改后需要重启 Humbert。
              </span>
              <small v-if="status.shellEnabled !== status.shellRuntimeActive" class="restart-note">
                当前配置与运行状态不同，请重启 Humbert 完成切换。
              </small>
              <small v-if="localProgramIsolationNote" class="restart-note">
                {{ localProgramIsolationNote }}
              </small>
            </div>
            <a-switch v-model="form.shellEnabled" />
          </div>
        </div>

        <div class="card-actions">
          <a-button type="primary" :loading="saving" @click="save">
            保存安全设置
          </a-button>
        </div>
      </SectionCard>

      <details class="advanced-card">
        <summary>
          <div>
            <strong>高级设置与安全自检</strong>
            <span>仅在需要更严格的隔离、排查平台问题或开发调试时使用。</span>
          </div>
        </summary>

        <div class="advanced-card__body">
          <SectionCard
              title="高级保护策略"
              description="这些选项会改变所有继承全局设置的 Agent。普通使用场景保持默认即可。"
          >
            <div class="policy-editor">
              <div class="policy-field">
                <span>文件保护范围</span>
                <a-select v-model="form.defaultProfile" aria-label="文件保护范围">
                  <a-option value="standard">标准保护</a-option>
                  <a-option value="workspace_only">严格保护（仅工作目录）</a-option>
                  <a-option value="full_access">不受限制</a-option>
                </a-select>
                <small>标准保护允许只读访问用户目录中的普通文件；严格保护只允许访问工作目录。</small>
              </div>

              <div class="policy-field">
                <span>网络范围</span>
                <a-select v-model="form.defaultNetworkMode" aria-label="网络范围">
                  <a-option value="none">禁止联网</a-option>
                  <a-option value="public">仅公网</a-option>
                  <a-option value="all">全部网络（含本机与局域网）</a-option>
                </a-select>
                <small>“全部网络”会扩大到本机和局域网服务，仅在明确需要时使用。</small>
              </div>

              <div class="policy-field">
                <span>本地程序隔离</span>
                <a-select v-model="form.defaultNativeMode" aria-label="本地程序隔离">
                  <a-option value="preferred">自动使用</a-option>
                  <a-option value="required">必须使用</a-option>
                  <a-option value="off">关闭</a-option>
                </a-select>
                <small>任意命令和 Skill 脚本始终要求原生文件隔离；stdio MCP 按选定策略执行。</small>
              </div>

              <div class="policy-field">
                <span>进程退出等待时间</span>
                <a-input-number
                    v-model="form.commandGracePeriodMS"
                    aria-label="进程退出等待时间"
                    :min="100"
                    :max="30000"
                    :step="100"
                />
                <small>停止或超时后等待子进程正常退出的毫秒数。通常不需要修改。</small>
              </div>
            </div>

            <div class="card-actions">
              <a-button type="primary" :loading="saving" @click="save">保存高级设置</a-button>
            </div>
          </SectionCard>

          <SectionCard
              title="当前系统保护能力"
              description="这里展示技术信息，仅用于诊断。正常使用时不需要理解这些实现细节。"
          >
            <div class="platform-summary">
              <div>
                <strong>{{ platformName }}</strong>
                <span>{{ status.available ? "系统级隔离可用" : "系统级隔离不可用" }}</span>
              </div>
              <StatusPill
                  :tone="status.available ? 'success' : 'warning'"
                  :label="status.available ? '可用' : '受限'"
              />
            </div>

            <div class="capability-grid">
              <div>
                <span>文件隔离</span>
                <strong>{{ status.filesystem ? "支持" : "受限" }}</strong>
              </div>
              <div>
                <span>子进程保护</span>
                <strong>{{ status.processTree ? "支持" : "受限" }}</strong>
              </div>
              <div>
                <span>网络隔离</span>
                <strong>{{ status.network ? "支持" : "受限" }}</strong>
              </div>
            </div>

            <div v-if="status.reason" class="sandbox-note">{{ status.reason }}</div>
            <div class="technical-line">技术后端：{{ status.backend }}</div>
          </SectionCard>

          <SectionCard
              title="安全自检"
              description="在系统临时目录中验证文件边界和本地程序隔离，不会读取或修改你的工作区文件。"
          >
            <div class="diagnostic-header">
              <a-button type="secondary" :loading="diagnosing" @click="diagnose">
                运行安全自检
              </a-button>
              <StatusPill
                  v-if="diagnostics"
                  :tone="diagnosticsTone"
                  :label="diagnosticsLabel"
              />
            </div>

            <div v-if="diagnostics" class="diagnostic-list">
              <div v-for="check in diagnostics.checks" :key="check.key" class="diagnostic-row">
                <StatusPill
                    :tone="check.status === 'pass' ? 'success' : check.status === 'fail' ? 'danger' : 'warning'"
                    :label="diagnosticStatusLabel(check.status)"
                />
                <div>
                  <strong>{{ check.label }}</strong>
                  <span>{{ check.detail }}</span>
                </div>
              </div>
            </div>
          </SectionCard>
        </div>
      </details>
    </template>
  </div>
</template>

<style scoped>
.sandbox-settings {
  display: grid;
  gap: var(--h-section-gap);
}

.security-status {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  margin-bottom: 14px;
  padding: 10px 12px;
  border-radius: 9px;
  background: var(--h-bg);
}

.security-status > div {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 10px;
}

.security-status span {
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.55;
}

.simple-setting-list {
  overflow: hidden;
  border: 1px solid var(--h-border);
  border-radius: 10px;
}

.simple-setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  padding: 15px;
}

.simple-setting-row + .simple-setting-row {
  border-top: 1px solid var(--h-border);
}

.simple-setting-row > div {
  display: grid;
  min-width: 0;
  gap: 5px;
}

.simple-setting-row strong {
  color: var(--h-text);
  font-size: 12px;
}

.simple-setting-row span {
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.6;
}

.card-actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}

.advanced-card {
  border: 1px solid var(--h-border);
  border-radius: 12px;
  background: var(--h-surface);
}

.advanced-card > summary {
  cursor: pointer;
  list-style: none;
  padding: 16px 18px;
  user-select: none;
}

.advanced-card > summary::-webkit-details-marker {
  display: none;
}

.advanced-card > summary > div {
  display: grid;
  gap: 5px;
}

.advanced-card > summary strong {
  color: var(--h-text);
  font-size: 12px;
}

.advanced-card > summary span {
  color: var(--h-text-muted);
  font-size: 10px;
}

.advanced-card__body {
  display: grid;
  gap: var(--h-section-gap);
  padding: 0 14px 14px;
}

.policy-editor {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px 18px;
}

.policy-field {
  display: grid;
  gap: 7px;
}

.policy-field > span {
  color: var(--h-text);
  font-size: 11px;
  font-weight: 650;
}

.policy-editor small {
  color: var(--h-text-muted);
  font-size: 9px;
  line-height: 1.55;
}

.platform-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.platform-summary > div {
  display: grid;
  gap: 4px;
}

.platform-summary strong {
  color: var(--h-text);
  font-size: 14px;
}

.platform-summary span,
.sandbox-note,
.technical-line {
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.6;
}

.capability-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
  margin-top: 16px;
}

.capability-grid > div {
  display: grid;
  gap: 5px;
  padding: 11px;
  border: 1px solid var(--h-border);
  border-radius: 8px;
}

.capability-grid span {
  color: var(--h-text-muted);
  font-size: 10px;
}

.capability-grid strong {
  color: var(--h-text-secondary);
  font-size: 11px;
}

.sandbox-note,
.technical-line {
  margin-top: 10px;
}

.diagnostic-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.diagnostic-list {
  display: grid;
  gap: 8px;
  margin-top: 14px;
}

.diagnostic-row {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: start;
  gap: 10px;
  padding: 10px;
  border: 1px solid var(--h-border);
  border-radius: 8px;
}

.diagnostic-row > div {
  display: grid;
  gap: 4px;
}

.diagnostic-row strong {
  color: var(--h-text-secondary);
  font-size: 11px;
}

.diagnostic-row span {
  color: var(--h-text-muted);
  font-size: 9px;
  line-height: 1.55;
}

@media (max-width: 760px) {
  .policy-editor,
  .capability-grid {
    grid-template-columns: 1fr;
  }

  .security-status,
  .simple-setting-row {
    align-items: flex-start;
  }
}
.restart-note {
  display: block;
  margin-top: 6px;
  color: var(--color-warning-6, #c98232);
  font-size: 10px;
  line-height: 1.5;
}
</style>
