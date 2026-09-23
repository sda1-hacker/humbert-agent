import { Call } from "@wailsio/runtime";

// WorkspaceService 只提供指定 Agent Workspace 的只读浏览与预览。
// 前端永远只提交 agentID + 相对路径，不提交物理 Workspace Root。
const workspaceServiceName =
    "github.com/sda1-hacker/humbert-agent/internal/services.WorkspaceService";

/** 读取指定 Agent 工作区的统计总览。 */
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

export function searchWorkspaceDocuments(agentID, query) {
    return Call.ByName(`${workspaceServiceName}.SearchDocuments`, agentID, query);
}
