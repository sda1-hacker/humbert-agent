import { Call } from "@wailsio/runtime";

let bindingPromise = null;

/**
 * 延迟加载 Wails AgentService Binding。
 *
 * 开发环境重新生成 bindings 后如果 import 失败，
 * 会清除缓存，允许下一次调用再次尝试加载。
 */
function loadBinding() {
    if (!bindingPromise) {
        bindingPromise = import(
            "../../bindings/github.com/sda1-hacker/humbert-agent/internal/services/agentservice.js"
            ).catch((error) => {
            bindingPromise = null;

            console.error(
                "[Humbert] AgentService Binding 加载失败",
                error,
            );

            throw new Error(
                "无法加载 AgentService Binding，请重新执行 wails3 generate bindings",
                {
                    cause: error,
                },
            );
        });
    }

    return bindingPromise;
}

/**
 * 返回全部 Agent。
 */
export async function listAgents() {
    const binding =
        await loadBinding();

    return binding.ListAgents();
}

/**
 * 返回一个 Agent。
 */
export async function getAgent(id) {
    const binding =
        await loadBinding();

    return binding.GetAgent(id);
}

/**
 * 创建 Agent。
 */
export async function createAgent(
    request,
) {
    const binding =
        await loadBinding();

    return binding.CreateAgent(
        request,
    );
}

/**
 * 删除 Agent Profile。
 *
 * Workspace 文件不会因为这个操作自动删除。
 */
export async function deleteAgent(
    id,
) {
    const binding =
        await loadBinding();

    return binding.DeleteAgent(id);
}

/**
 * 打开 Wails 原生目录选择器。
 *
 * 返回：
 *
 *   "/Users/alice/Projects/foo"
 *
 * 用户取消时返回空字符串。
 */
export async function selectWorkspaceDirectory(
    currentPath = "",
) {
    const binding =
        await loadBinding();

    return binding.SelectWorkspaceDirectory(
        currentPath,
    );
}

/**
 * 返回 Humbert 内置 Tool Catalog。
 */
export async function listBuiltinTools() {
    const binding = await loadBinding();
    return binding.ListBuiltinTools();
}

/**
 * 返回当前平台 Sandbox 能力与默认策略。
 */
export async function getSandboxStatus() {
    const binding = await loadBinding();
    return binding.GetSandboxStatus();
}

/**
 * 只更新 Agent 的安全配置。
 */
export async function updateAgentSecurity(id, request) {
    const binding = await loadBinding();
    return binding.UpdateAgentSecurity(id, request);
}

/**
 * 打开用于 Sandbox 额外目录的原生目录选择器。
 */
export async function selectSandboxDirectory(currentPath = "") {
    const binding = await loadBinding();
    return binding.SelectSandboxDirectory(currentPath);
}

/**
 * 更新应用级 Sandbox 默认策略。
 */
export async function updateSandboxSettings(request) {
    const binding = await loadBinding();
    return binding.UpdateSandboxSettings(request);
}

/**
 * 在临时目录中运行 PathGuard + Native Sandbox 安全自检。
 */
export async function runSandboxDiagnostics() {
    const binding = await loadBinding();
    return binding.RunSandboxDiagnostics();
}

/**
 * 只修改 Agent 默认模型。
 *
 * 该命令不会触碰 Workspace、Sandbox、Skills 或其它 Profile 字段。
 */
export function setAgentModel(id, modelID) {
    return Call.ByName(
        "github.com/sda1-hacker/humbert-agent/internal/services.AgentService.SetAgentModel",
        id,
        { modelID },
    );
}

/**
 * 只更新 Utility / Memory / Vision Model Roles。
 */
export function setAgentModelRoles(id, roles) {
    return Call.ByName(
        "github.com/sda1-hacker/humbert-agent/internal/services.AgentService.SetAgentModelRoles",
        id,
        {
            utilityModelID: roles?.utilityModelID ?? "",
            memoryModelID: roles?.memoryModelID ?? "",
            visionModelID: roles?.visionModelID ?? "",
        },
    );
}
