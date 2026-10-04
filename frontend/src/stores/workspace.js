import { defineStore } from "pinia";
import { Events } from "@wailsio/runtime";
import { beginLatestRequest, invalidateRequests } from "../utils/latestRequest.js";

import {
    getWorkspaceOverview,
    listWorkspaceDirectory,
    previewWorkspaceFile,
} from "../api/workspace.js";

const runtimeEventName = "humbert:runtime:event";
const proactiveEventName = "humbert:proactive:event";

let unsubscribeRuntime = null;
let unsubscribeProactive = null;
let invalidationTimer = null;

/**
 * 对 Wails 返回数组做最小归一化。
 *
 * 工作区是只读浏览页，后端临时返回 null/undefined 时应该退化为空目录，
 * 不能因为一个字段异常让整个 Vue 页面中断。
 */
function normalizeArray(value) {
    return Array.isArray(value) ? value : [];
}

function parentDirectory(path) {
    const separator = path.lastIndexOf("/");
    return separator < 0 ? "." : path.slice(0, separator);
}

function directChildPath(directory, path) {
    const prefix = directory === "." ? "" : `${directory}/`;
    if (!path || path === directory || !path.startsWith(prefix)) return "";
    return prefix + path.slice(prefix.length).split("/")[0];
}

export const useWorkspaceStore = defineStore("workspace", {
    state: () => ({
        /**
         * 右侧文件面板当前加载的 Agent。它由面板监听选中的聊天 Agent 更新。
         */
        agentID: "",
        overview: null,

        // directories 使用“相对路径 -> 一层目录结果”的懒加载缓存。
        // 这比一次从后端递归读取整棵树更适合大型仓库。
        directories: {},
        expandedPaths: ["."],

        selectedPath: "",
        selectedEntry: null,
        preview: null,

        loadingOverview: false,
        loadingDirectories: {},
        loadingPreview: false,
        error: "",

        // revision 只表示“底层 Workspace 可能变化了”。可见的右侧文件面板按需刷新。
        revision: 0,
    }),

    getters: {
        rootEntries(state) {
            return state.directories["."]?.entries ?? [];
        },
    },

    actions: {
        /**
         * 监听 Runtime 完成事件和主动助手的工作区变化事件。
         *
         * Tool 执行期间文件可能连续变化很多次，所以这里不直接发 IPC，而是把多次事件合并
         * 成一次 revision 增量；可见的右侧文件面板会再做一次短延迟刷新。
         */
        initialiseEvents() {
            if (!unsubscribeRuntime) {
                unsubscribeRuntime = Events.On(runtimeEventName, (event) => {
                    const payload = event?.data;
                    if (!payload || payload.agentID !== this.agentID) return;
                    if (!["turn.completed", "turn.failed", "turn.cancelled"].includes(payload.type)) {
                        return;
                    }
                    this.scheduleInvalidation();
                });
            }
            if (!unsubscribeProactive) {
                unsubscribeProactive = Events.On(proactiveEventName, (event) => {
                    const payload = event?.data;
                    const record = payload?.record;
                    if (
                        record?.event?.kind === "workspace_changed" &&
                        record?.event?.agent_id === this.agentID
                    ) {
                        this.scheduleInvalidation();
                    }
                });
            }
        },

        disposeEvents() {
            if (unsubscribeRuntime) {
                unsubscribeRuntime();
                unsubscribeRuntime = null;
            }
            if (unsubscribeProactive) {
                unsubscribeProactive();
                unsubscribeProactive = null;
            }
            if (invalidationTimer !== null) {
                clearTimeout(invalidationTimer);
                invalidationTimer = null;
            }
        },

        scheduleInvalidation() {
            if (invalidationTimer !== null) clearTimeout(invalidationTimer);
            invalidationTimer = setTimeout(() => {
                invalidationTimer = null;
                this.revision += 1;
            }, 180);
        },

        /** 切换工作区 Agent 时完整清空旧目录缓存，避免两个项目中同名路径串台。 */
        resetForAgent(agentID) {
            // 即使 A → B → A 切回同一个 Agent，第一次 A 的请求也已经失效。
            invalidateRequests(this);
            this.agentID = agentID || "";
            this.overview = null;
            this.directories = {};
            this.expandedPaths = ["."];
            this.selectedPath = "";
            this.selectedEntry = null;
            this.preview = null;
            this.loadingDirectories = {};
            this.loadingOverview = false;
            this.loadingPreview = false;
            this.error = "";
        },

        /**
         * 加载一个 Agent 对应的 Workspace。
         *
         * 右侧文件面板只读取当前文件系统：总览 + 根目录。
         */
        async load(agentID) {
            if (!agentID) {
                this.resetForAgent("");
                return;
            }
            if (this.agentID !== agentID) {
                this.resetForAgent(agentID);
            }
            const isCurrent = beginLatestRequest(this, "workspace");
            this.error = "";
            const results = await Promise.allSettled([
                this.loadOverview(),
                this.loadDirectory(".", true),
            ]);
            const rejected = results.find((item) => item.status === "rejected");
            if (rejected && isCurrent()) {
                this.error = rejected.reason?.message ?? String(rejected.reason);
            }
        },

        async loadOverview() {
            if (!this.agentID) return null;
            const agentID = this.agentID;
            const isCurrent = beginLatestRequest(this, "overview");
            this.loadingOverview = true;
            try {
                const value = await getWorkspaceOverview(agentID);
                if (!isCurrent()) return null;
                this.overview = value;
                return value;
            } catch (error) {
                if (isCurrent()) throw error;
                return null;
            } finally {
                if (isCurrent()) this.loadingOverview = false;
            }
        },

        async loadDirectory(path = ".", force = false) {
            if (!this.agentID) return null;
            const agentID = this.agentID;
            if (!force && this.directories[path]) {
                return this.directories[path];
            }
            const isCurrent = beginLatestRequest(this, `directory:${path}`);
            const selection = this.selectedEntry;
            this.loadingDirectories = { ...this.loadingDirectories, [path]: true };
            try {
                const value = await listWorkspaceDirectory(agentID, path);
                if (!isCurrent()) return null;
                const normalized = { ...value, entries: normalizeArray(value?.entries) };
                this.directories = { ...this.directories, [path]: normalized };
                this.reconcileDirectory(path, normalized, selection);
                return normalized;
            } catch (error) {
                // 目录可能刚被外部删除；由父目录确认，不能根据错误文本猜测或隐藏权限错误。
                if (isCurrent() && path !== ".") {
                    try { await this.loadDirectory(parentDirectory(path), true); }
                    catch { /* 父目录无法确认时仍报告原读取错误。 */ }
                }
                if (isCurrent()) throw error;
                return null;
            } finally {
                if (isCurrent()) {
                    this.loadingDirectories = { ...this.loadingDirectories, [path]: false };
                }
            }
        },

        /** 用完整目录结果清理失效子树；截断列表中的缺项不能证明文件已删除。 */
        reconcileDirectory(path, listing, selection) {
            const entries = new Map(listing.entries.map((entry) => [entry.path, entry]));
            const missing = (target, type) => {
                const child = directChildPath(path, target);
                if (!child) return false;
                const entry = entries.get(child);
                if (!entry) return !listing.truncated;
                return entry.type !== (child === target ? type : "directory");
            };
            const directories = { ...this.directories };
            const loading = { ...this.loadingDirectories };
            for (const cached of new Set([...Object.keys(directories), ...Object.keys(loading), ...this.expandedPaths])) {
                if (!missing(cached, "directory")) continue;
                invalidateRequests(this, `directory:${cached}`);
                delete directories[cached];
                delete loading[cached];
            }
            this.directories = directories;
            this.loadingDirectories = loading;
            this.expandedPaths = this.expandedPaths.filter((item) => !missing(item, "directory"));
            // 请求发出后用户可能换了文件，旧目录结果不能清掉新的选择。
            if (selection && this.selectedEntry === selection && missing(selection.path, selection.type)) {
                invalidateRequests(this, "preview");
                this.selectedEntry = null;
                this.selectedPath = "";
                this.preview = null;
                this.loadingPreview = false;
            }
        },

        /** 展开目录时才向后端请求它的子项；收起只改变 UI 状态，不删除缓存。 */
        async toggleDirectory(path) {
            const expanded = this.expandedPaths.includes(path);
            if (expanded) {
                this.expandedPaths = this.expandedPaths.filter((item) => item !== path);
                return;
            }
            // 展开选择立即生效，目录加载结束不能撤销用户随后执行的收起操作。
            this.expandedPaths = [...this.expandedPaths, path];
            await this.loadDirectory(path, true);
        },

        /** 文件点击后读取预览；目录点击只更新选中信息，不读取文件内容。 */
        async selectEntry(entry) {
            invalidateRequests(this, "preview");
            this.selectedEntry = entry ?? null;
            this.selectedPath = entry?.path ?? "";
            this.preview = null;
            this.loadingPreview = false;
            return this.loadPreview();
        },

        /** 只刷新当前文件内容，不改变用户的选择；新选择会使旧预览请求失效。 */
        async loadPreview() {
            const agentID = this.agentID;
            const entry = this.selectedEntry;
            if (!entry || entry.type !== "file" || !this.agentID) {
                return null;
            }
            const isCurrent = beginLatestRequest(this, "preview");
            this.loadingPreview = true;
            try {
                const value = await previewWorkspaceFile(agentID, entry.path);
                if (!isCurrent()) return null;
                this.preview = value;
                return value;
            } catch (error) {
                if (isCurrent()) {
                    try { await this.loadDirectory(parentDirectory(entry.path), true); }
                    catch { /* 未确认删除时保留原预览错误。 */ }
                }
                if (isCurrent()) throw error;
                return null;
            } finally {
                if (isCurrent()) this.loadingPreview = false;
            }
        },

        /**
         * 从聊天里的“本轮文件变化”直接打开一个文件。
         *
         * 这里不要求父目录已经在左侧文件树展开；预览区可以独立读取相对路径。
         * 之后用户若继续浏览目录，文件树仍按原来的懒加载方式工作。
         */
        async openPath(path) {
            if (!path || !this.agentID) return null;
            const entry = {
                name: path.split("/").pop() || path,
                path,
                type: "file",
            };
            return this.selectEntry(entry);
        },

        /**
         * 重读可见目录和选中文件的父目录，删除失效选择后再刷新预览。
         * 后台同步不重复扫描总览，也不重读未变化的大文件/图片。
         */
        async refresh({ background = false } = {}) {
            if (!this.agentID) return;
            const isCurrent = beginLatestRequest(this, "workspace");
            const selection = this.selectedEntry;
            const paths = new Set(this.expandedPaths);
            if (selection) paths.add(parentDirectory(selection.path));
            this.error = "";
            try {
                const results = await Promise.allSettled([
                    ...(background ? [] : [this.loadOverview()]),
                    ...[...paths].map((path) => this.loadDirectory(path, true)),
                ]);
                if (!isCurrent()) return;
                const rejected = results.find((item) => item.status === "rejected");
                if (rejected) throw rejected.reason;
                if (selection && this.selectedEntry === selection && selection.type === "file") {
                    const current = this.directories[parentDirectory(selection.path)]?.entries
                        .find((entry) => entry.path === selection.path);
                    if (!background || !this.preview || current && (
                        current.size !== this.preview.size || current.modifiedAt !== this.preview.modifiedAt
                    )) {
                        await this.loadPreview();
                    }
                }
            } catch (error) {
                if (isCurrent()) {
                    this.error = error?.message ?? String(error);
                    throw error;
                }
            }
        },
    },
});
