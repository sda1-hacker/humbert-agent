<script setup>
import {
  computed,
  ref,
  watch,
} from "vue";

import {
  mcpTransportLabel,
} from "../../utils/mcp.js";

import {
  IconLink,
  IconRefresh,
} from "@arco-design/web-vue/es/icon";

import SectionCard from "../ui/SectionCard.vue";
import StatusPill from "../ui/StatusPill.vue";

const props = defineProps({
  server: {
    type: Object,
    required: true,
  },
  tool: {
    type: Object,
    required: true,
  },
  agentName: {
    type: String,
    default: "当前 Agent",
  },
  enabled: {
    type: Boolean,
    default: false,
  },
  saving: {
    type: Boolean,
    default: false,
  },
});

const emit = defineEmits([
  "back",
  "toggle",
  "set-risk",
  "refresh",
]);

const riskValue = ref(props.tool?.risk || "write");

watch(
    () => props.tool?.risk,
    (value) => {
      riskValue.value = value || "write";
    },
);

const annotationRows = computed(() => {
  const annotations = props.tool?.annotations;
  if (!annotations || typeof annotations !== "object") return [];

  const definitions = [
    ["readOnlyHint", "只读提示"],
    ["destructiveHint", "破坏性提示"],
    ["idempotentHint", "幂等提示"],
    ["openWorldHint", "开放世界提示"],
  ];
  return definitions
      .filter(([key]) => Object.prototype.hasOwnProperty.call(annotations, key))
      .map(([key, label]) => ({
        key,
        label,
        value: annotations[key] === true ? "是" : annotations[key] === false ? "否" : String(annotations[key]),
      }));
});

const inputSchemaText = computed(() => {
  const schema = props.tool?.inputSchema;
  if (!schema || typeof schema !== "object") return "";
  try {
    return JSON.stringify(schema, null, 2);
  } catch {
    return "";
  }
});

const fingerprintShort = computed(() => {
  const value = String(props.server?.fingerprint || "");
  return value ? `${value.slice(0, 12)}…${value.slice(-8)}` : "—";
});

function riskText(risk) {
  switch (risk) {
    case "read":
      return "Read · 读取数据";
    case "exec":
      return "Exec · 执行/高副作用";
    default:
      return "Write · 修改/外部副作用（默认）";
  }
}

function setRisk(value) {
  riskValue.value = value;
  emit("set-risk", value);
}
</script>

<template>
  <div class="mcp-tool-detail">
    <header class="mcp-tool-detail__topbar">
      <a-button type="text" @click="emit('back')">
        返回连接器
      </a-button>

      <a-button size="small" @click="emit('refresh')">
        <template #icon><IconRefresh /></template>
        刷新 Tool
      </a-button>
    </header>

    <section class="mcp-tool-detail__hero">
      <div class="mcp-tool-detail__identity">
        <span class="mcp-tool-detail__icon"><IconLink /></span>
        <div>
          <div class="mcp-tool-detail__eyebrow">{{ server.name }} · MCP Tool</div>
          <h1>{{ tool.rawName }}</h1>
          <code>{{ tool.exposedName }}</code>
        </div>
      </div>

      <div class="mcp-tool-detail__toggle">
        <StatusPill
            :label="enabled ? `已为 ${agentName} 启用` : `未为 ${agentName} 启用`"
            :tone="enabled ? 'success' : 'neutral'"
        />
        <a-switch
            :model-value="enabled"
            :disabled="saving"
            @change="(value) => emit('toggle', value)"
        />
      </div>
    </section>

    <div v-if="server.enabled === false" class="mcp-tool-detail__disabled-note">
      这个 MCP Server 当前已停用。Tool 选择仍会保留，但不会进入 Runtime；重新启用 Server 后自动恢复。
    </div>

    <div class="mcp-tool-detail__grid">
      <SectionCard class="mcp-tool-detail__card--wide" title="Tool 描述">
        <p class="mcp-tool-detail__description">{{ tool.description || "这个 MCP Tool 没有提供描述。" }}</p>
      </SectionCard>

      <SectionCard class="mcp-tool-detail__card--wide" title="Input Schema / 参数">
        <pre v-if="inputSchemaText" class="mcp-tool-detail__schema">{{ inputSchemaText }}</pre>
        <p v-else class="mcp-tool-detail__muted">Server 没有提供可展示的参数 Schema。</p>
        <p class="mcp-tool-detail__muted mcp-tool-detail__schema-note">
          这里展示的是 Eino ToolInfo 已解析的参数定义，用于判断 Tool 会接收哪些输入；真实调用仍由 Eino officialmcp 执行。
        </p>
      </SectionCard>

      <SectionCard title="Humbert 风险级别">
        <p class="mcp-tool-detail__muted">
          MCP Server 的 annotation 只作为参考，不会自动降低权限。未显式覆盖时固定按 Write 处理。
        </p>

        <a-select
            :model-value="riskValue"
            class="mcp-tool-detail__risk-select"
            :disabled="saving"
            @change="setRisk"
        >
          <a-option value="read">Read · 读取数据</a-option>
          <a-option value="write">Write · 修改/外部副作用（默认）</a-option>
          <a-option value="exec">Exec · 执行/高副作用</a-option>
        </a-select>

        <div class="mcp-tool-detail__risk-note">
          当前：{{ riskText(tool.risk) }}
          <span v-if="tool.riskOverridden"> · 用户已覆盖</span>
          <span v-else> · Humbert 默认</span>
        </div>
      </SectionCard>

      <SectionCard title="Server 安全身份">
        <dl class="mcp-tool-detail__facts">
          <dt>Server</dt>
          <dd>{{ server.name }}</dd>
          <dt>稳定 Key</dt>
          <dd><code>{{ server.key }}</code></dd>
          <dt>Transport</dt>
          <dd>{{ mcpTransportLabel(server.transport) }}</dd>
          <dt>Fingerprint</dt>
          <dd><code>{{ fingerprintShort }}</code></dd>
        </dl>
        <p class="mcp-tool-detail__muted">
          Agent 的长期 MCP 授权会绑定 Server ID + Fingerprint。Endpoint、命令或 Credential 引用变化后，旧授权不会自动继承。
        </p>
      </SectionCard>

      <SectionCard class="mcp-tool-detail__card--wide" title="MCP Annotations">
        <div v-if="annotationRows.length > 0" class="mcp-tool-detail__annotations">
          <div v-for="item in annotationRows" :key="item.key" class="mcp-tool-detail__annotation">
            <span>{{ item.label }}</span>
            <strong>{{ item.value }}</strong>
          </div>
        </div>
        <p v-else class="mcp-tool-detail__muted">Server 没有为这个 Tool 提供标准 annotation。</p>
        <p class="mcp-tool-detail__muted">
          Annotations 来自外部 Server，只用于帮助用户理解能力性质，Humbert 不把它们当作可信的 Permission Policy。
        </p>
      </SectionCard>
    </div>
  </div>
</template>

<style scoped>
.mcp-tool-detail {
  width: min(var(--h-content-standard), calc(100% - (var(--h-page-gutter) * 2)));
  min-width: 0;
  margin: 0 auto;
  padding: var(--h-page-top) 0 var(--h-page-bottom);
  color: var(--h-text);
}

.mcp-tool-detail__topbar,
.mcp-tool-detail__hero,
.mcp-tool-detail__identity,
.mcp-tool-detail__toggle {
  display: flex;
  align-items: center;
}

.mcp-tool-detail__topbar {
  justify-content: space-between;
  margin-bottom: 20px;
}

.mcp-tool-detail__hero {
  justify-content: space-between;
  gap: 24px;
  padding: 24px;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-lg);
  background: var(--h-surface);
}

.mcp-tool-detail__identity {
  min-width: 0;
  gap: 14px;
}

.mcp-tool-detail__icon {
  display: grid;
  width: 44px;
  height: 44px;
  flex: 0 0 44px;
  place-items: center;
  border: 1px solid var(--h-accent-border);
  border-radius: 12px;
  background: var(--h-accent-soft);
  color: var(--h-accent-hover);
}

.mcp-tool-detail__eyebrow {
  margin-bottom: 3px;
  color: var(--h-text-muted);
  font-size: 10px;
  letter-spacing: .06em;
  text-transform: uppercase;
}

.mcp-tool-detail h1 {
  margin: 0 0 5px;
  font-size: 27px;
  font-weight: 650;
}

.mcp-tool-detail code {
  color: var(--h-text-secondary);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
  overflow-wrap: anywhere;
}

.mcp-tool-detail__toggle {
  flex: 0 0 auto;
  gap: 10px;
  color: var(--h-text-secondary);
  font-size: 11px;
}

.mcp-tool-detail__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
  margin-top: 14px;
}

.mcp-tool-detail__card--wide {
  grid-column: 1 / -1;
}

.mcp-tool-detail__description,
.mcp-tool-detail__muted {
  margin: 0;
  color: var(--h-text-secondary);
  font-size: 12px;
  line-height: 1.75;
}

.mcp-tool-detail__risk-select {
  width: 100%;
  margin-top: 14px;
}

.mcp-tool-detail__risk-note {
  margin-top: 8px;
  color: var(--h-text-muted);
  font-size: 10px;
}

.mcp-tool-detail__facts {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: 8px 12px;
  margin: 0 0 14px;
  font-size: 11px;
}

.mcp-tool-detail__facts dt {
  color: var(--h-text-muted);
}

.mcp-tool-detail__facts dd {
  min-width: 0;
  margin: 0;
  color: var(--h-text);
  overflow-wrap: anywhere;
}

.mcp-tool-detail__annotations {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
  margin-bottom: 12px;
}

.mcp-tool-detail__annotation {
  display: flex;
  justify-content: space-between;
  gap: 10px;
  padding: 9px 10px;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-sm);
  background: var(--h-bg);
  color: var(--h-text-secondary);
  font-size: 11px;
}

.mcp-tool-detail__annotation strong {
  color: var(--h-text);
  font-weight: 600;
}


.mcp-tool-detail__disabled-note {
  margin-top: 14px;
  padding: 11px 13px;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-md);
  background: var(--h-bg);
  color: var(--h-text-secondary);
  font-size: 11px;
  line-height: 1.6;
}

.mcp-tool-detail__schema {
  max-height: 360px;
  margin: 0;
  padding: 14px;
  overflow: auto;
  border: 1px solid var(--h-border);
  border-radius: var(--h-radius-md);
  background: var(--h-bg);
  color: var(--h-text-secondary);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 10px;
  line-height: 1.6;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.mcp-tool-detail__schema-note {
  margin-top: 10px;
}

@media (max-width: 900px) {
  .mcp-tool-detail__grid {
    grid-template-columns: 1fr;
  }

  .mcp-tool-detail__card--wide {
    grid-column: auto;
  }
}
</style>
