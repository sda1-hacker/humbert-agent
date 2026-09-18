import { defineStore } from "pinia";
import { Events } from "@wailsio/runtime";

import {
    getWorkspaceOverview,
    listWorkspaceArtifacts,
    listWorkspaceDirectory,
    previewWorkspaceFile,
} from "../api/workspace.js";

const runtimeEventName = "humbert:runtime:event";
const proactiveEventName = "humbert:proactive:event";

let unsubscribeRuntime = null;
let unsubscribeProactive = null;
let invalidationTimer = null;

/**
 * 对 Wails/旧数据做最小归一化。
 *
 * 工作区页面是只读视图，因此遇到后端暂时缺少某个数组字段时优先显示空状态，
 * 而不是让 Vue 模板因为 undefined 直接中断整个页面。
 */
function normalizeArray(value) {
    return Array.isArray(value) ? value : [];
}

export const useWorkspaceStore = defineStore("workspace", {
    state: () => ({
        // agentID 表示当前这份文件树/产物缓存属于哪个 Agent。Agent 切换时整份状态重置。
        agentID: "",
        overview: null,
        artifacts: [],

        // directories 使用“相对路径 -> 一层目录结果”的懒加载缓存。
        // 这比一次从后端递归读取整棵树更适合大型仓库。
        directories: {},
        expandedPaths: ["."],

        selectedPath: "",
        selectedEntry: null,
        preview: null,

        loadingOverview: false,
        loadingArtifacts: false,
        loadingDirectories: {},
        loadingPreview: false,
        error: "",

        // revision 只表示“底层 Workspace 可能变化了”。真正刷新由 WorkspaceView 在可见时触发。
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
         * 成一次 revision 增量；可见的 WorkspaceView 会再做一次短延迟刷新。
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

        /** 切换 Agent 时丢弃旧 Workspace 的文件树和预览，避免路径串台。 */
        resetForAgent(agentID) {
            this.agentID = agentID || "";
            this.overview = null;
            this.artifacts = [];
            this.directories = {};
            this.expandedPaths = ["."];
            this.selectedPath = "";
            this.selectedEntry = null;
            this.preview = null;
            this.loadingDirectories = {};
            this.error = "";
        },

        /**
         * 首次进入工作区时并行读取总览、根目录和产物。
         * 任意一个查询失败都会保留错误文本，但不会主动清空另外已经成功的数据。
         */
        async load(agentID) {
            if (!agentID) {
                this.resetForAgent("");
                return;
            }
            if (this.agentID !== agentID) {
                this.resetForAgent(agentID);
            }
            this.error = "";
            const results = await Promise.allSettled([
                this.loadOverview(),
                this.loadDirectory(".", true),
                this.loadArtifacts(),
            ]);
            const rejected = results.find((item) => item.status === "rejected");
            if (rejected) {
                this.error = rejected.reason?.message ?? String(rejected.reason);
            }
        },

        async loadOverview() {
            if (!this.agentID) return null;
            this.loadingOverview = true;
            try {
                this.overview = await getWorkspaceOverview(this.agentID);
                return this.overview;
            } finally {
                this.loadingOverview = false;
            }
        },

        async loadArtifacts() {
            if (!this.agentID) return [];
            this.loadingArtifacts = true;
            try {
                const values = await listWorkspaceArtifacts(this.agentID, 120);
                this.artifacts = normalizeArray(values);
                return this.artifacts;
            } finally {
                this.loadingArtifacts = false;
            }
        },

        async loadDirectory(path = ".", force = false) {
            if (!this.agentID) return null;
            if (!force && this.directories[path]) {
                return this.directories[path];
            }
            this.loadingDirectories = { ...this.loadingDirectories, [path]: true };
            try {
                const value = await listWorkspaceDirectory(this.agentID, path);
                this.directories = {
                    ...this.directories,
                    [path]: {
                        ...value,
                        entries: normalizeArray(value?.entries),
                    },
                };
                return this.directories[path];
            } finally {
                this.loadingDirectories = { ...this.loadingDirectories, [path]: false };
            }
        },

        /**
         * 展开目录时才向后端请求它的子项；收起只改变 UI 状态，不删除缓存。
         */
        async toggleDirectory(path) {
            const expanded = this.expandedPaths.includes(path);
            if (expanded) {
                this.expandedPaths = this.expandedPaths.filter((item) => item !== path);
                return;
            }
            await this.loadDirectory(path);
            this.expandedPaths = [...this.expandedPaths, path];
        },

        /** 文件点击后读取预览；目录点击只更新选中信息，不读取文件内容。 */
        async selectEntry(entry) {
            this.selectedEntry = entry ?? null;
            this.selectedPath = entry?.path ?? "";
            this.preview = null;
            if (!entry || entry.type !== "file" || !this.agentID) {
                return null;
            }
            this.loadingPreview = true;
            try {
                this.preview = await previewWorkspaceFile(this.agentID, entry.path);
                return this.preview;
            } finally {
                this.loadingPreview = false;
            }
        },

        /** 从产物/最近文件跳到预览时，不要求对应父目录已经在文件树中展开。 */
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
         * 刷新保持当前选中路径，但重读已展开目录，确保外部编辑器修改后树状态及时更新。
         */
        async refresh() {
            if (!this.agentID) return;
            const expanded = [...this.expandedPaths];
            const selectedPath = this.selectedPath;
            const selectedEntry = this.selectedEntry;
            this.error = "";
            try {
                await Promise.all([
                    this.loadOverview(),
                    this.loadArtifacts(),
                    ...expanded.map((path) => this.loadDirectory(path, true)),
                ]);
                if (selectedPath && selectedEntry?.type === "file") {
                    await this.openPath(selectedPath);
                }
            } catch (error) {
                this.error = error?.message ?? String(error);
                throw error;
            }
        },
    },
});
