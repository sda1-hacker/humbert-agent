import { Call } from "@wailsio/runtime";

const agentServiceName =
    "github.com/sda1-hacker/humbert-agent/internal/services.AgentService";

/**
 * 返回全部 Agent。
 */
export function listAgents() {
    return Call.ByName(`${agentServiceName}.ListAgents`);
}

/**
 * 返回一个 Agent。
 */
export function getAgent(id) {
    return Call.ByName(`${agentServiceName}.GetAgent`, id);
}

/**
 * 创建 Agent。
 */
export function createAgent(
    request,
) {
    return Call.ByName(`${agentServiceName}.CreateAgent`, request);
}

/**
 * 保存 Agent 的完整“Agent 设置”表单。
 * 局部操作（例如切换模型）仍使用独立窄命令。
 */
export function updateAgent(id, request) {
    return Call.ByName(`${agentServiceName}.UpdateAgent`, id, request);
}

/**
 * 删除 Agent 及其 Humbert 内部数据。
 *
 * 后端会级联删除 Session 和 Humbert 管理的 Workspace；
 * 用户选择的 Custom Workspace 不会被删除。
 */
export function deleteAgent(
    id,
) {
    return Call.ByName(`${agentServiceName}.DeleteAgent`, id);
}

/**
 * 打开 Wails 原生目录选择器。
 *
 * 返回：
 *
 *   "/Users/alice/workspaces/foo"
 *
 * 用户取消时返回空字符串。
 */
export function selectWorkspaceDirectory(
    currentPath = "",
) {
    return Call.ByName(`${agentServiceName}.SelectWorkspaceDirectory`, currentPath);
}

/**
 * 返回 Humbert 内置 Tool Catalog。
 */
export function listBuiltinTools() {
    return Call.ByName(`${agentServiceName}.ListBuiltinTools`);
}

/**
 * 返回当前平台 Sandbox 能力与默认策略。
 */
export function getSandboxStatus() {
    return Call.ByName(`${agentServiceName}.GetSandboxStatus`);
}

/**
 * 只更新 Agent 的安全配置。
 */
export function updateAgentSecurity(id, request) {
    return Call.ByName(`${agentServiceName}.UpdateAgentSecurity`, id, request);
}

/**
 * 打开用于 Sandbox 额外目录的原生目录选择器。
 */
export function selectSandboxDirectory(currentPath = "") {
    return Call.ByName(`${agentServiceName}.SelectSandboxDirectory`, currentPath);
}

/**
 * 更新应用级 Sandbox 默认策略。
 */
export function updateSandboxSettings(request) {
    return Call.ByName(`${agentServiceName}.UpdateSandboxSettings`, request);
}

/**
 * 在临时目录中运行 PathGuard + Native Sandbox 安全自检。
 */
export function runSandboxDiagnostics() {
    return Call.ByName(`${agentServiceName}.RunSandboxDiagnostics`);
}

/**
 * 只修改 Agent 默认模型。
 *
 * 该命令不会触碰 Workspace、Sandbox、Skills 或其它 Profile 字段。
 */
export function setAgentModel(id, modelID) {
    return Call.ByName(
        `${agentServiceName}.SetAgentModel`,
        id,
        { modelID },
    );
}

/**
 * 只更新 Utility / Memory Model Roles。
 */
export function setAgentModelRoles(id, roles) {
    return Call.ByName(
        `${agentServiceName}.SetAgentModelRoles`,
        id,
        {
            utilityModelID: roles?.utilityModelID ?? "",
            memoryModelID: roles?.memoryModelID ?? "",
        },
    );
}
