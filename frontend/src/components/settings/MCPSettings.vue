<script setup>
import {
  computed,
  onMounted,
  reactive,
  ref,
} from "vue";

import {
  Message,
} from "@arco-design/web-vue";

import {
  confirmAction,
} from "../../utils/confirm.js";

import {
  IconDelete,
  IconEdit,
  IconPlus,
  IconRefresh,
  IconThunderbolt,
} from "@arco-design/web-vue/es/icon";

import {
  createMCPServer,
  deleteMCPServer,
  disconnectMCPServer,
  exportMCPServersConfig,
  importMCPServersConfig,
  listMCPServers,
  setMCPServerEnabled,
  testMCPConnection,
  updateMCPServer,
} from "../../api/mcp.js";

import {
  mcpConnectionStateLabel,
  mcpConnectionStateTone,
  mcpTransportLabel,
} from "../../utils/mcp.js";

import SectionCard from "../ui/SectionCard.vue";
import EmptyState from "../ui/EmptyState.vue";
import StatusPill from "../ui/StatusPill.vue";

const servers = ref([]);
const loading = ref(false);
const saving = ref(false);
const testingID = ref("");
const connectionState = reactive({});
const importOpen = ref(false);
const importPayload = ref("");
const importing = ref(false);
const exportOpen = ref(false);
const exportPayload = ref("");

const form = reactive({
  id: "",
  name: "",
  key: "",
  transport: "stdio",
  command: "",
  argsText: "",
  workingDirectory: "",
  env: [],
  endpoint: "",
  bearerEnabled: false,
  bearerToken: "",
  bearerHasCredential: false,
  headers: [],
});

const editing = computed(() => Boolean(form.id));
const isStdio = computed(() => form.transport === "stdio");
const isHTTP = computed(() => form.transport === "streamable_http");

function normaliseServers(values) {
  return Array.isArray(values) ? values : [];
}

function slugifyKey(value) {
  return String(value ?? "")
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9_]+/g, "_")
      .replace(/^\d+/, "")
      .replace(/^_+|_+$/g, "")
      .slice(0, 24);
}

function reset() {
  Object.assign(form, {
    id: "",
    name: "",
    key: "",
    transport: "stdio",
    command: "",
    argsText: "",
    workingDirectory: "",
    env: [],
    endpoint: "",
    bearerEnabled: false,
    bearerToken: "",
    bearerHasCredential: false,
    headers: [],
  });
}

function edit(server) {
  Object.assign(form, {
    id: server?.id ?? "",
    name: server?.name ?? "",
    key: server?.key ?? "",
    transport: server?.transport || "stdio",
    command: server?.command ?? "",
    argsText: Array.isArray(server?.args)
        ? server.args.join("\n")
        : "",
    workingDirectory: server?.workingDirectory ?? "",
    env: Array.isArray(server?.env)
        ? server.env.map((item) => ({
          name: item?.name ?? "",
          secret: "",
          hasCredential: Boolean(item?.hasCredential),
        }))
        : [],
    endpoint: server?.endpoint ?? "",
    bearerEnabled: Boolean(server?.hasBearerCredential),
    bearerToken: "",
    bearerHasCredential: Boolean(server?.hasBearerCredential),
    headers: Array.isArray(server?.headers)
        ? server.headers.map((item) => ({
          name: item?.name ?? "",
          secret: "",
          hasCredential: Boolean(item?.hasCredential),
        }))
        : [],
  });
}

function addEnv() {
  if (form.env.length >= 32) {
    Message.warning("最多配置 32 个 stdio 环境变量");
    return;
  }
  form.env.push({
    name: "",
    secret: "",
    hasCredential: false,
  });
}

function removeEnv(index) {
  form.env.splice(index, 1);
}

function addHeader() {
  if (form.headers.length >= 16) {
    Message.warning("最多配置 16 个自定义 Header");
    return;
  }
  form.headers.push({
    name: "",
    secret: "",
    hasCredential: false,
  });
}

function removeHeader(index) {
  form.headers.splice(index, 1);
}

function requestPayload() {
  return {
    key: editing.value
        ? form.key
        : (form.key || slugifyKey(form.name)),
    name: form.name.trim(),
    transport: form.transport,
    command: isStdio.value ? form.command.trim() : "",
    args: isStdio.value
        ? form.argsText
            .split("\n")
            .map((value) => value.trim())
            .filter(Boolean)
        : [],
    workingDirectory: isStdio.value ? form.workingDirectory.trim() : "",
    env: isStdio.value
        ? form.env.map((item) => ({
          name: String(item?.name ?? "").trim(),
          secret: String(item?.secret ?? "").trim(),
        }))
        : [],
    endpoint: isHTTP.value ? form.endpoint.trim() : "",
    bearerEnabled: isHTTP.value && form.bearerEnabled,
    bearerToken: isHTTP.value && form.bearerEnabled
        ? form.bearerToken.trim()
        : "",
    headers: isHTTP.value
        ? form.headers.map((item) => ({
          name: String(item?.name ?? "").trim(),
          secret: String(item?.secret ?? "").trim(),
        }))
        : [],
  };
}

function validateRequest(request) {
  if (!request.name || !request.key) {
    return "请填写显示名称和稳定 Key";
  }
  if (request.transport === "stdio") {
    if (!request.command) return "请填写 stdio 启动命令";
    const seen = new Set();
    for (let index = 0; index < form.env.length; index += 1) {
      const item = form.env[index];
      const name = String(item?.name ?? "").trim();
      const secret = String(item?.secret ?? "").trim();
      if (!name) return `请填写第 ${index + 1} 个环境变量名`;
      if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) {
        return `环境变量「${name}」名称无效`;
      }
      const key = name.toLowerCase();
      if (seen.has(key)) return `环境变量「${name}」重复`;
      seen.add(key);
      if (!secret && !item?.hasCredential) {
        return `请填写环境变量「${name}」的 Secret`;
      }
    }
    return "";
  }
  if (request.transport === "streamable_http") {
    if (!request.endpoint) return "请填写 Streamable HTTP Endpoint";
    if (request.bearerEnabled && !request.bearerToken && !form.bearerHasCredential) {
      return "请填写 Bearer Token";
    }
    const seen = new Set();
    for (let index = 0; index < form.headers.length; index += 1) {
      const item = form.headers[index];
      const name = String(item?.name ?? "").trim();
      const secret = String(item?.secret ?? "").trim();
      if (!name) return `请填写第 ${index + 1} 个自定义 Header 名称`;
      const key = name.toLowerCase();
      if (seen.has(key)) return `自定义 Header「${name}」重复`;
      seen.add(key);
      if (!secret && !item?.hasCredential) {
        return `请填写 Header「${name}」的 Secret`;
      }
    }
    return "";
  }
  return "请选择有效的传输方式";
}

async function load() {
  if (loading.value) return;

  loading.value = true;
  try {
    servers.value = normaliseServers(await listMCPServers());
    for (const key of Object.keys(connectionState)) delete connectionState[key];
    for (const server of servers.value) {
      const state = server?.connectionState || (server?.enabled === false ? "disabled" : "disconnected");
      connectionState[server.id] = {
        ok: state === "connected",
        tone: mcpConnectionStateTone(state),
        text: serverStatusText(server),
        detail: server?.lastSuccessAt && state === "connected"
            ? `最近成功 ${new Date(server.lastSuccessAt).toLocaleString()}`
            : "",
        error: server?.lastError && (state === "error" || state === "degraded")
            ? String(server.lastError)
            : "",
      };
    }
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    loading.value = false;
  }
}

async function exportConfig() {
  try {
    exportPayload.value = await exportMCPServersConfig();
    exportOpen.value = true;
  } catch (error) {
    Message.error(error?.message ?? String(error));
  }
}

function openImport() {
  importPayload.value = "";
  importOpen.value = true;
}

async function importConfig() {
  if (!importPayload.value.trim() || importing.value) return;
  importing.value = true;
  try {
    const created = await importMCPServersConfig(importPayload.value);
    importOpen.value = false;
    importPayload.value = "";
    await load();
    Message.success(`已导入 ${Array.isArray(created) ? created.length : 0} 个 MCP Server；已默认停用，请重新填写认证信息后启用`);
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    importing.value = false;
  }
}

async function testServer(server) {
  if (!server?.id || testingID.value || server?.enabled === false) return;

  testingID.value = server.id;
  connectionState[server.id] = {
    ok: false,
    tone: "pending",
    text: "正在连接",
    detail: "正在执行 initialize + tools/list…",
    error: "",
  };
  try {
    const result = await testMCPConnection(server.id);
    await load();
    connectionState[server.id] = {
      ok: true,
      tone: "ok",
      text: "已连接",
      detail: `${result?.toolCount ?? 0} 个 Tool · ${result?.durationMS ?? 0}ms`,
      error: "",
    };
    Message.success(`「${server.name}」连接成功`);
  } catch (error) {
    await load();
    Message.error(error?.message ?? String(error));
  } finally {
    testingID.value = "";
  }
}

async function save() {
  if (saving.value) return;

  const request = requestPayload();
  const validationMessage = validateRequest(request);
  if (validationMessage) {
    Message.warning(validationMessage);
    return;
  }

  saving.value = true;
  try {
    const wasEditing = editing.value;
    const server = wasEditing
        ? await updateMCPServer(form.id, request)
        : await createMCPServer(request);

    Message.success(wasEditing ? "MCP Server 已更新" : "MCP Server 已创建");
    reset();
    await load();
    await testServer(server);
  } catch (error) {
    Message.error(error?.message ?? String(error));
  } finally {
    saving.value = false;
  }
}

async function remove(server) {
  if (!server?.id) return;

  const confirmed = await confirmAction({
    title: "删除 MCP Server",
    message: `确定删除「${server.name}」吗？如果仍有 Agent 启用了这个 Server 的 Tool，Humbert 会拒绝删除。关联的 Credential（包括 stdio 环境变量与 HTTP 认证）也会一并清理。`,
    confirmText: "删除",
    danger: true,
  });

  if (!confirmed) return;

  try {
    await deleteMCPServer(server.id);
    delete connectionState[server.id];
    if (form.id === server.id) reset();
    await load();
    Message.success("MCP Server 已删除");
  } catch (error) {
    Message.error(error?.message ?? String(error));
  }
}

function serverSummary(server) {
  if (server?.transport === "streamable_http") {
    return server?.endpoint || "未配置 Endpoint";
  }
  const args = Array.isArray(server?.args) ? server.args : [];
  return [server?.command, ...args].filter(Boolean).join(" ") || "未配置命令";
}

function credentialSummary(server) {
  if (server?.transport === "stdio") {
    const envCount = Array.isArray(server?.env) ? server.env.length : 0;
    return envCount > 0 ? `${envCount} 个安全环境变量` : "无额外环境变量";
  }
  const parts = [];
  if (server?.hasBearerCredential) parts.push("Bearer");
  const headerCount = Array.isArray(server?.headers) ? server.headers.length : 0;
  if (headerCount > 0) parts.push(`${headerCount} 个 Header`);
  return parts.length > 0 ? parts.join(" · ") : "无认证 Header";
}

function serverStatusText(server) {
  const state = server?.connectionState || (server?.enabled === false ? "disabled" : "disconnected");
  return mcpConnectionStateLabel(state);
}

function serverStatusDetail(server) {
  const cached = connectionState[server?.id];
  if (cached?.detail) return cached.detail;
  const state = server?.connectionState || (server?.enabled === false ? "disabled" : "disconnected");
  if (server?.lastSuccessAt && state === "connected") {
    return `最近成功 ${new Date(server.lastSuccessAt).toLocaleString()}`;
  }
  return "";
}

function serverErrorText(server) {
  const cached = connectionState[server?.id];
  if (cached?.error) return cached.error;
  const state = server?.connectionState || (server?.enabled === false ? "disabled" : "disconnected");
  if ((state === "error" || state === "degraded") && server?.lastError) {
    return String(server.lastError);
  }
  return "";
}

function serverStatusTone(server) {
  const state = server?.connectionState || (server?.enabled === false ? "disabled" : "disconnected");
  return mcpConnectionStateTone(state);
}

async function setServerEnabled(server, enabled) {
  if (!server?.id) return;
  try {
    await setMCPServerEnabled(server.id, enabled);
    if (!enabled) delete connectionState[server.id];
    await load();
    Message.success(enabled ? `「${server.name}」已启用` : `「${server.name}」已停用，Agent Tool 选择已保留`);
  } catch (error) {
    Message.error(error?.message ?? String(error));
  }
}

async function disconnectServer(server) {
  if (!server?.id) return;
  try {
    await disconnectMCPServer(server.id);
    delete connectionState[server.id];
    await load();
    Message.success(`「${server.name}」已断开`);
  } catch (error) {
    Message.error(error?.message ?? String(error));
  }
}

onMounted(load);
</script>

<template>
  <div class="mcp-settings">
    <SectionCard
        class="mcp-server-list"
        title="MCP Server"
        description="统一管理本地 stdio 与远程 Streamable HTTP Server。Agent 要使用哪些 Tool，请到左侧「连接器」工作区配置。"
    >
      <template #actions>
        <div class="mcp-list-actions">
          <a-button size="small" @click="openImport">导入</a-button>
          <a-button size="small" :disabled="servers.length === 0" @click="exportConfig">导出</a-button>
          <a-button size="small" :loading="loading" @click="load">
            <template #icon><IconRefresh /></template>
            刷新
          </a-button>
        </div>
      </template>

      <EmptyState
          v-if="servers.length === 0 && !loading"
          title="还没有 MCP Server"
          description="在右侧添加一个 stdio 或 Streamable HTTP Server，保存后可以立即测试连接。"
          compact
      />

      <div v-else class="server-items">
        <article
            v-for="server in servers"
            :key="server.id"
            class="server-item h-list-row"
            :class="{
              'server-item--editing': form.id === server.id,
              'h-list-row--selected': form.id === server.id,
            }"
        >
          <div class="server-item__main">
            <div class="server-item__title-row">
              <strong>{{ server.name }}</strong>
              <code>{{ server.key }}</code>
              <span class="transport-chip">{{ mcpTransportLabel(server.transport) }}</span>
            </div>

            <div class="server-item__command">
              {{ serverSummary(server) }}
            </div>

            <div class="server-item__auth">
              {{ credentialSummary(server) }}
            </div>

            <div class="server-item__status-row">
              <StatusPill
                  class="server-item__status"
                  :label="connectionState[server.id]?.text || serverStatusText(server)"
                  :tone="connectionState[server.id]?.tone || serverStatusTone(server)"
              />
              <span v-if="serverStatusDetail(server)" class="server-item__status-detail">
                {{ serverStatusDetail(server) }}
              </span>
            </div>

            <p
                v-if="serverErrorText(server)"
                class="server-item__error"
                :title="serverErrorText(server)"
            >
              {{ serverErrorText(server) }}
            </p>
          </div>

          <div class="server-item__actions">
            <a-tooltip :content="server.enabled === false ? '启用 Server' : '停用 Server（保留 Agent Tool 选择）'">
              <a-switch
                  size="small"
                  :model-value="server.enabled !== false"
                  @change="(value) => setServerEnabled(server, value)"
              />
            </a-tooltip>

            <a-tooltip v-if="server.connected" content="断开当前 Session">
              <a-button type="text" size="mini" @click="disconnectServer(server)">
                断开
              </a-button>
            </a-tooltip>

            <a-tooltip :content="['error', 'degraded'].includes(server.connectionState) ? '重试连接' : '测试连接'">
              <a-button
                  type="text"
                  size="mini"
                  :loading="testingID === server.id"
                  :disabled="server.enabled === false"
                  @click="testServer(server)"
              >
                <template #icon><IconThunderbolt /></template>
              </a-button>
            </a-tooltip>

            <a-tooltip content="编辑 Server">
              <a-button type="text" size="mini" @click="edit(server)">
                <template #icon><IconEdit /></template>
              </a-button>
            </a-tooltip>

            <a-tooltip content="删除 Server">
              <a-button type="text" size="mini" status="danger" @click="remove(server)">
                <template #icon><IconDelete /></template>
              </a-button>
            </a-tooltip>
          </div>
        </article>
      </div>
    </SectionCard>

    <SectionCard
        class="mcp-editor"
        :title="editing ? '编辑 MCP Server' : '添加 MCP Server'"
        description="stdio 适合本机进程；Streamable HTTP 适合远程 MCP。Secret 只保存到 Humbert Credential Store，不写入 servers.json。"
    >
      <template #actions>
        <a-button v-if="editing" type="text" size="small" @click="reset">取消编辑</a-button>
      </template>

      <a-form :model="form" layout="vertical">
        <a-form-item label="显示名称">
          <a-input
              v-model="form.name"
              placeholder="例如：Filesystem / GitHub"
              @blur="!editing && !form.key && (form.key = slugifyKey(form.name))"
          />
        </a-form-item>

        <a-form-item label="稳定 Key">
          <a-input
              v-model="form.key"
              :disabled="editing"
              placeholder="github"
          />
          <template #extra>
            <span class="field-help">
              创建后不可修改，用于模型侧 Tool Namespace，例如 mcp_github_search_issues。
            </span>
          </template>
        </a-form-item>

        <a-form-item label="传输方式">
          <div class="transport-picker" role="radiogroup" aria-label="MCP 传输方式">
            <button
                type="button"
                class="transport-option"
                :class="{ 'transport-option--active': form.transport === 'stdio' }"
                role="radio"
                :aria-checked="form.transport === 'stdio'"
                @click="form.transport = 'stdio'"
            >
              <span class="transport-option__mark" aria-hidden="true"></span>
              <span class="transport-option__copy">
                <strong>本地 stdio</strong>
                <small>启动本机 MCP 进程，通过 stdin / stdout 通信。</small>
              </span>
            </button>

            <button
                type="button"
                class="transport-option"
                :class="{ 'transport-option--active': form.transport === 'streamable_http' }"
                role="radio"
                :aria-checked="form.transport === 'streamable_http'"
                @click="form.transport = 'streamable_http'"
            >
              <span class="transport-option__mark" aria-hidden="true"></span>
              <span class="transport-option__copy">
                <strong>Streamable HTTP</strong>
                <small>连接远程 MCP Endpoint，Secret 由 Credential Store 管理。</small>
              </span>
            </button>
          </div>
        </a-form-item>

        <template v-if="isStdio">
          <a-form-item label="启动命令">
            <a-input v-model="form.command" placeholder="npx" />
            <template #extra>
              <span class="field-help">
                建议使用系统可直接找到的命令或绝对路径。Windows 运行 npx 型 MCP 时优先使用 cmd，并把 /c、npx 放到参数前两行。
              </span>
            </template>
          </a-form-item>

          <a-form-item label="参数">
            <a-textarea
                v-model="form.argsText"
                :auto-size="{ minRows: 4, maxRows: 9 }"
                placeholder="每行一个参数，例如：\n-y\n@modelcontextprotocol/server-filesystem\n/path/to/files"
            />
            <template #extra>
              <span class="field-help">每一行都会作为独立 argv 传给进程，不经过 shell 拼接。</span>
            </template>
          </a-form-item>

          <a-form-item label="工作目录">
            <a-input
                v-model="form.workingDirectory"
                placeholder="可选；留空使用 Humbert 启动目录"
            />
          </a-form-item>

          <div class="credential-section custom-env">
            <div class="credential-section__header">
              <div>
                <strong>安全环境变量</strong>
                <span>适用于 GITHUB_TOKEN、API_KEY 等。Value 只进入本地 Credential Store，不写入 servers.json。</span>
              </div>
              <a-button size="mini" :disabled="form.env.length >= 32" @click="addEnv">
                <template #icon><IconPlus /></template>
                添加变量
              </a-button>
            </div>

            <div v-if="form.env.length === 0" class="headers-empty">
              没有额外环境变量。MCP 子进程只继承启动所需的最小系统环境，不会自动继承 Humbert 的 API Key 或其他业务 Secret。
            </div>

            <div v-else class="header-list">
              <div
                  v-for="(item, index) in form.env"
                  :key="index"
                  class="header-row"
              >
                <a-input
                    v-model="item.name"
                    class="header-row__name"
                    placeholder="GITHUB_TOKEN"
                />
                <a-input-password
                    v-model="item.secret"
                    class="header-row__secret"
                    :placeholder="item.hasCredential ? '已保存；留空保持不变' : 'Secret'"
                />
                <a-button type="text" status="danger" @click="removeEnv(index)">
                  移除
                </a-button>
              </div>
            </div>
          </div>
        </template>

        <template v-else>
          <a-form-item label="MCP Endpoint">
            <a-input
                v-model="form.endpoint"
                placeholder="https://example.com/mcp"
            />
            <template #extra>
              <span class="field-help">
                远程地址必须使用 HTTPS；HTTP 仅允许 localhost / loopback。
              </span>
            </template>
          </a-form-item>

          <div class="credential-section">
            <div class="credential-section__header">
              <div>
                <strong>Bearer Token</strong>
                <span>可选。Token 仅进入本地 Credential Store。</span>
              </div>
              <a-switch v-model="form.bearerEnabled" />
            </div>

            <a-input-password
                v-if="form.bearerEnabled"
                v-model="form.bearerToken"
                allow-clear
                :placeholder="form.bearerHasCredential ? '已保存；留空保持原 Token 不变' : '输入 Bearer Token'"
            />
          </div>

          <div class="credential-section custom-headers">
            <div class="credential-section__header">
              <div>
                <strong>自定义认证 Header</strong>
                <span>适用于 X-API-Key、Authorization 等认证方式；启用 Bearer 时不能再添加 Authorization。</span>
              </div>
              <a-button size="mini" :disabled="form.headers.length >= 16" @click="addHeader">
                <template #icon><IconPlus /></template>
                添加 Header
              </a-button>
            </div>

            <div v-if="form.headers.length === 0" class="headers-empty">
              没有自定义 Header。
            </div>

            <div v-else class="header-list">
              <div
                  v-for="(header, index) in form.headers"
                  :key="index"
                  class="header-row"
              >
                <a-input
                    v-model="header.name"
                    class="header-row__name"
                    placeholder="X-API-Key"
                />
                <a-input-password
                    v-model="header.secret"
                    class="header-row__secret"
                    :placeholder="header.hasCredential ? '已保存；留空保持不变' : 'Secret'"
                />
                <a-button type="text" status="danger" @click="removeHeader(index)">
                  移除
                </a-button>
              </div>
            </div>
          </div>
        </template>

        <div class="editor-actions">
          <a-button v-if="editing" :disabled="saving" @click="reset">
            取消
          </a-button>

          <a-button type="primary" :loading="saving" @click="save">
            <template #icon><IconPlus v-if="!editing" /></template>
            {{ editing ? "保存并测试" : "添加并测试" }}
          </a-button>
        </div>
      </a-form>
    </SectionCard>

    <a-modal
        v-model:visible="exportOpen"
        title="导出 MCP 配置"
        :footer="false"
    >
      <p class="import-help">
        下面只包含非敏感配置。Bearer Token、自定义 Header Value、stdio Secret 和 Credential ID 都不会导出。
      </p>
      <a-textarea
          :model-value="exportPayload"
          readonly
          :auto-size="{ minRows: 10, maxRows: 18 }"
      />
    </a-modal>

    <a-modal
        v-model:visible="importOpen"
        title="导入 MCP 配置"
        :ok-loading="importing"
        ok-text="导入"
        cancel-text="取消"
        @ok="importConfig"
    >
      <p class="import-help">
        粘贴 Humbert 导出的 MCP JSON。Secret 不在导出文件中，因此导入后的 Server 会保持停用；请逐个编辑并重新填写 Token、Header 或环境变量后再启用。
      </p>
      <a-textarea
          v-model="importPayload"
          :auto-size="{ minRows: 10, maxRows: 18 }"
          placeholder='{ "version": 1, "servers": [...] }'
      />
    </a-modal>
  </div>
</template>

<style scoped>

.mcp-list-actions {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}

.mcp-server-list :deep(.h-section-card__header) {
  display: block;
}

.mcp-server-list :deep(.h-section-card__copy) {
  width: 100%;
}

.mcp-server-list :deep(.h-section-card__actions) {
  width: 100%;
  max-width: 100%;
  margin: 14px 0 0;
  justify-content: flex-start;
}

.import-help {
  margin: 0 0 12px;
  color: var(--color-text-2);
  font-size: 12px;
  line-height: 1.65;
}
.mcp-settings {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(370px, 450px);
  align-items: start;
  gap: var(--h-section-gap);
}

.mcp-server-list {
  overflow: hidden;
}

.mcp-editor {
}

.server-items {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 8px;
  padding: 0;
}

.server-item {
  display: flex;
  width: 100%;
  min-width: 0;
  min-height: 112px;
  align-items: flex-start;
  gap: 12px;
  padding: 12px;
  overflow: hidden;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-md);
  background: color-mix(in srgb, var(--h-bg) 72%, var(--h-surface) 28%);
  transition: border-color 120ms ease, background-color 120ms ease;
}

.server-item__main {
  min-width: 0;
  flex: 1;
}

.server-item__title-row {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 7px;
}

.server-item__title-row strong {
  color: var(--h-text);
  font-size: 12px;
  font-weight: 600;
}

.server-item__title-row code,
.server-item__command,
.server-item__auth {
  color: var(--h-text-muted);
  font-size: 10px;
}

.server-item__command {
  margin-top: 5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.server-item__auth {
  margin-top: 3px;
}

.transport-chip {
  border: 1px solid var(--h-border);
  border-radius: 999px;
  padding: 1px 6px;
  color: var(--h-text-muted);
  font-size: 8px;
}

.server-item__status-row {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 7px 9px;
  margin-top: 9px;
}

.server-item__status-detail {
  min-width: 0;
  color: var(--h-text-muted);
  font-size: 9px;
  line-height: 1.45;
}

.server-item__error {
  max-width: 100%;
  margin: 7px 0 0;
  overflow: hidden;
  color: var(--h-danger);
  font-size: 9px;
  line-height: 1.5;
  overflow-wrap: anywhere;
  word-break: break-word;
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.server-item__actions {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 2px;
  padding-top: 1px;
}

.field-help {
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.55;
}

.transport-picker {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 9px;
}

.transport-option {
  display: grid;
  min-width: 0;
  grid-template-columns: 16px minmax(0, 1fr);
  align-items: flex-start;
  gap: 9px;
  border: 1px solid var(--h-border);
  border-radius: 10px;
  padding: 11px 12px;
  background: var(--h-bg);
  color: var(--h-text);
  cursor: pointer;
  text-align: left;
  transition: border-color 120ms ease, background-color 120ms ease, box-shadow 120ms ease;
}

.transport-option:hover {
  border-color: var(--h-border-strong);
  background: var(--h-surface-hover);
}

.transport-option--active {
  border-color: var(--h-accent-border);
  background: var(--h-accent-soft);
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--h-accent) 22%, transparent);
}

.transport-option__mark {
  width: 13px;
  height: 13px;
  margin-top: 2px;
  border: 1px solid var(--h-border-strong);
  border-radius: 50%;
  background: var(--h-surface);
  box-shadow: inset 0 0 0 3px var(--h-surface);
}

.transport-option--active .transport-option__mark {
  border-color: var(--h-accent);
  background: var(--h-accent);
}

.transport-option__copy {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 3px;
}

.transport-option__copy strong {
  color: var(--h-text);
  font-size: 10px;
  font-weight: 600;
}

.transport-option__copy small {
  color: var(--h-text-muted);
  font-size: 8px;
  line-height: 1.5;
}

.credential-section {
  margin: 0 0 16px;
  padding: 13px;
  border: 1px solid var(--h-border);
  border-radius: 10px;
  background: var(--h-surface-hover);
}

.credential-section__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  margin-bottom: 10px;
}

.credential-section__header > div {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 3px;
}

.credential-section__header strong {
  color: var(--h-text);
  font-size: 11px;
  font-weight: 600;
}

.credential-section__header span,
.headers-empty {
  color: var(--h-text-muted);
  font-size: 10px;
  line-height: 1.5;
}

.header-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.header-row {
  display: grid;
  grid-template-columns: minmax(110px, 0.8fr) minmax(150px, 1.2fr) auto;
  align-items: center;
  gap: 8px;
}

.editor-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding-top: 4px;
}

@media (max-width: 1040px) {
  .mcp-settings {
    grid-template-columns: 1fr;
  }

  .mcp-editor {
    order: -1;
  }
}

@media (max-width: 640px) {
  .mcp-server-list :deep(.h-section-card__actions) {
    width: 100%;
    margin-left: 0;
    justify-content: flex-start;
  }

  .mcp-list-actions {
    width: 100%;
    justify-content: flex-start;
  }

  .transport-picker {
    grid-template-columns: 1fr;
  }
}
</style>
