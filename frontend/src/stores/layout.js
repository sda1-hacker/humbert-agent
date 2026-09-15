import { defineStore } from "pinia";

const DEFAULT_SIDEBAR_WIDTH = 268;

const MIN_SIDEBAR_WIDTH = 220;

const MAX_SIDEBAR_WIDTH = 420;

const STORAGE_KEY =
    "humbert.ui.sidebar-width";

/**
 * 将 Sidebar 宽度限制在桌面 UI 的合理范围。
 *
 * Sidebar 太窄会导致：
 *
 * - Agent 名称无法阅读；
 * - Session 操作按钮挤压；
 *
 * Sidebar 太宽则会严重侵占聊天区域。
 */
function clampWidth(value) {
    const number = Number(value);

    if (!Number.isFinite(number)) {
        return DEFAULT_SIDEBAR_WIDTH;
    }

    return Math.min(
        MAX_SIDEBAR_WIDTH,
        Math.max(
            MIN_SIDEBAR_WIDTH,
            Math.round(number),
        ),
    );
}

/**
 * Sidebar 宽度属于纯前端 UI 偏好，而不是 Humbert Core 配置。
 *
 * 因此这里使用 WebView localStorage 保存，
 * 不进入 Viper，也不进入 Agent/Session 领域持久化数据。
 */
function readStoredWidth() {
    try {
        const value =
            window.localStorage.getItem(
                STORAGE_KEY,
            );

        if (!value) {
            return DEFAULT_SIDEBAR_WIDTH;
        }

        return clampWidth(value);
    } catch (error) {
        console.warn(
            "[Humbert] 读取 Sidebar UI 偏好失败",
            error,
        );

        return DEFAULT_SIDEBAR_WIDTH;
    }
}

function persistWidth(width) {
    try {
        window.localStorage.setItem(
            STORAGE_KEY,
            String(width),
        );
    } catch (error) {
        console.warn(
            "[Humbert] 保存 Sidebar UI 偏好失败",
            error,
        );
    }
}

/**
 * LayoutStore 保存纯 UI Layout State。
 *
 * 不允许把 Agent、Model、Session 等业务状态放进这里。
 */
export const useLayoutStore =
    defineStore(
        "layout",
        {
            state: () => ({
                sidebarWidth:
                    readStoredWidth(),
            }),

            actions: {
                setSidebarWidth(width) {
                    const normalized =
                        clampWidth(width);

                    this.sidebarWidth =
                        normalized;

                    persistWidth(normalized);
                },

                resetSidebarWidth() {
                    this.setSidebarWidth(
                        DEFAULT_SIDEBAR_WIDTH,
                    );
                },
            },
        },
    );