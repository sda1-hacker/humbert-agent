import {
    Call,
} from "@wailsio/runtime";

const permissionServiceName =
    "github.com/sda1-hacker/humbert-agent/internal/services.PermissionService";

/**
 * 读取 Permission 默认策略、长期 Agent Rule 与当前 Session 临时 Rule。
 *
 * sessionID 可以为空；后端不会在这种情况下返回其他 Session 的临时权限。
 */
export function getPermissionState(sessionID = "") {
    return Call.ByName(
        `${permissionServiceName}.State`,
        sessionID,
    );
}

/**
 * 更新 Permission 应用级默认策略。
 *
 * 后端会通过 Viper 写回 config.yaml，并在当前进程同步更新 PermissionEngine 与
 * ApprovalManager；前端校验只负责 UX，不是安全边界。
 */
export function updatePermissionSettings(settings) {
    return Call.ByName(
        `${permissionServiceName}.UpdateSettings`,
        settings,
    );
}

/**
 * 删除一条持久化 Agent Scope Permission Rule。
 */
export function deletePermissionRule(ruleID) {
    return Call.ByName(
        `${permissionServiceName}.DeleteRule`,
        ruleID,
    );
}

/**
 * 删除当前 Session 的一条临时 Rule。
 */
export function deleteSessionPermissionRule(sessionID, ruleID) {
    return Call.ByName(
        `${permissionServiceName}.DeleteSessionRule`,
        sessionID,
        ruleID,
    );
}

/**
 * 清除当前 Session 的全部临时 Rule。
 */
export function clearSessionPermissionRules(sessionID) {
    return Call.ByName(
        `${permissionServiceName}.ClearSessionRules`,
        sessionID,
    );
}

/**
 * 撤销全部持久化 Allow Rule。显式 Deny Rule 会保留。
 */
export function clearPersistentPermissionAllows() {
    return Call.ByName(
        `${permissionServiceName}.ClearPersistentAllows`,
    );
}
