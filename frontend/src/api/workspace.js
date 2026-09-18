import { Call } from "@wailsio/runtime";

// WorkspaceService 只提供当前 Agent Workspace 的只读浏览/预览/产物查询。
// 前端永远只提交 agentID + 相对路径，不提交物理 Workspace Root。
const workspaceServiceName =
    "github.com/sda1-hacker/humbert-agent/internal/services.WorkspaceService";

/** 读取当前 Agent 工作区的统计总览。 */
export function getWorkspaceOverview(agentID) {
    return Call.ByName(`${workspaceServiceName}.Overview`, agentID);
}

/** 懒加载一个目录的直接子项。path 使用 Workspace 相对路径，根目录为 "."。 */
export function listWorkspaceDirectory(agentID, path = ".") {
    return Call.ByName(`${workspaceServiceName}.ListDirectory`, agentID, path);
}

/** 读取一个文件的受限预览；后端负责文本/图片大小与类型安全边界。 */
export function previewWorkspaceFile(agentID, path) {
    return Call.ByName(`${workspaceServiceName}.PreviewFile`, agentID, path);
}

/** 读取最近可证明来源的 Agent 文件产物。 */
export function listWorkspaceArtifacts(agentID, limit = 100) {
    return Call.ByName(`${workspaceServiceName}.Artifacts`, agentID, limit);
}
