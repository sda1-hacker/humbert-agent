import {
    AppService,
} from "../../bindings/github.com/sda1-hacker/humbert-agent/internal/services";

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
export async function getAppStatus() {
    return AppService.Status();
}