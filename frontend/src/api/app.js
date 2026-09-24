import { Call } from "@wailsio/runtime";

const appServiceName =
    "github.com/sda1-hacker/humbert-agent/internal/services.AppService";

/**
 * 获取 Humbert Core 当前状态。
 *
 * Vue Component 不应该直接依赖 Wails 自动生成的 binding 路径。
 *
 * 这样做的原因是：
 *
 * 1. 隔离 Wails generated code；
 * 2. 后续 Go Service 改名时只需要修改 API 层；
 * 3. Component 可以保持纯 Vue；
 * 4. 后续测试 Component 时可以更容易 mock API。
 *
 * @returns {Promise<object>}
 */
export function getAppStatus() {
    return Call.ByName(`${appServiceName}.Status`);
}

export function exportBackup(passphrase) {
    return Call.ByName(`${appServiceName}.ExportBackup`, passphrase);
}

export function scheduleRestore(passphrase) {
    return Call.ByName(`${appServiceName}.ScheduleRestore`, passphrase);
}

export function getPendingRestoreStatus() {
    return Call.ByName(`${appServiceName}.PendingRestoreStatus`);
}

export function cancelPendingRestore() {
    return Call.ByName(`${appServiceName}.CancelPendingRestore`);
}

export function getPendingBackupStatus() {
    return Call.ByName(`${appServiceName}.PendingBackupStatus`);
}

export function cancelPendingBackup() {
    return Call.ByName(`${appServiceName}.CancelPendingBackup`);
}
