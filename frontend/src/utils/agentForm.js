import { defaultBuiltinTools } from "./defaultBuiltinTools.js";

/**
 * 把后端 Agent DTO 投影成一份独立表单。目录、能力目录和平台状态只用于展示；
 * 所有可编辑的数组和嵌套对象都重新创建，取消编辑不会修改 Pinia 中的真实 Agent。
 * 新建与编辑共用字段定义，新增 Profile 字段时只需核对这里及请求投影。
 */
export function createAgentForm(agent, { tools = [], sandboxStatus = null, defaultModelID = "" } = {}) {
  const available = agent?.availableBuiltinTools?.length ? agent.availableBuiltinTools : tools;
  return {
    id: agent?.id || "",
    name: agent?.name || "",
    avatar: agent?.avatar || "",
    subagentEnabled: Boolean(agent?.subagentEnabled),
    instruction: agent?.instruction || "",
    modelID: agent ? agent.modelID || "" : defaultModelID,
    modelRoles: { utilityModelID: agent?.modelRoles?.utilityModelID || "" },
    enabledSkills: [...(agent?.enabledSkills || [])],
    workspaceMode: agent?.workspaceMode || "managed",
    workspacePath: agent?.workspacePath || "",
    workspaceDisplayPath: agent?.workspaceDisplayPath || "",
    availableBuiltinTools: [...available],
    // 已配置的空集合表示全部禁用；未配置的既有 Agent 保持其默认工具语义。
    enabledBuiltinTools: agent
      ? agent.builtinToolsConfigured ? [...(agent.enabledBuiltinTools || [])] : available.map(tool => tool.name)
      : defaultBuiltinTools(available),
    builtinToolsConfigured: agent ? Boolean(agent.builtinToolsConfigured) : available.length > 0,
    sandbox: {
      profile: agent?.sandbox?.profile || "",
      additionalWritePaths: [...(agent?.sandbox?.additionalWritePaths || [])],
      networkMode: agent?.sandbox?.networkMode || "",
      nativeMode: agent?.sandbox?.nativeMode || "",
    },
    sandboxStatus: agent?.sandboxStatus || sandboxStatus,
  };
}

/** 只提交可编辑 Profile 字段。请求是提交时的快照，确认对话框及 IPC 等待期间不随表单变化。 */
export function agentFormRequest(form) {
  return {
    name: form.name.trim(), avatar: form.avatar, instruction: form.instruction,
    subagentEnabled: form.subagentEnabled, modelID: form.modelID,
    modelRoles: { ...form.modelRoles }, enabledSkills: [...form.enabledSkills],
    workspaceMode: form.workspaceMode,
    // 托管目录由后端 Agent ID 推导，不能把自定义目录残留值提交给后端。
    workspacePath: form.workspaceMode === "custom" ? form.workspacePath : "",
    builtinToolsConfigured: form.builtinToolsConfigured || form.availableBuiltinTools.length > 0,
    enabledBuiltinTools: [...form.enabledBuiltinTools],
    sandbox: { ...form.sandbox, additionalWritePaths: [...form.sandbox.additionalWritePaths] },
  };
}
