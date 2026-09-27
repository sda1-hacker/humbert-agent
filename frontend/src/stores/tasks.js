import {
    defineStore,
} from "pinia";

import {
    Events,
} from "@wailsio/runtime";

import {
    archiveTask,
    cancelTaskRun,
    clearTaskRuns,
    createTask,
    deleteTask,
    deleteTaskRun,
    listTaskRuns,
    listTasks,
    runTaskNow,
    setTaskStatus,
    updateTask,
} from "../api/tasks.js";
import {
    useSessionStore,
} from "./sessions.js";
import { beginLatestRequest, invalidateRequests } from "../utils/latestRequest.js";

const taskEventName = "humbert:task:event";

let unsubscribeTaskEvent = null;
let refreshTimer = null;

export const useTaskStore = defineStore("tasks", {
    state: () => ({
        items: [],
        runsByTask: {},
        selectedID: "",
        loading: false,
        loadingRuns: {},
        lastEvent: null,
        eventSequence: 0,
        refreshError: "",
    }),

    getters: {
        selectedTask(state) {
            return state.items.find((item) => item.id === state.selectedID) ?? null;
        },

        selectedRuns(state) {
            return state.runsByTask[state.selectedID] ?? [];
        },
    },

    actions: {
        initialiseEvents() {
            if (unsubscribeTaskEvent) {
                return;
            }
            unsubscribeTaskEvent = Events.On(taskEventName, (event) => {
                const payload = event?.data;
                if (payload && typeof payload === "object") {
                    this.lastEvent = payload;
                    this.eventSequence += 1;
                }
                if (refreshTimer !== null) {
                    clearTimeout(refreshTimer);
                }
                refreshTimer = setTimeout(() => {
                    refreshTimer = null;
                    void this.refresh().catch((error) => {
                        this.refreshError = error?.message ?? String(error);
                    });
                }, 120);
            });
        },

        disposeEvents() {
            if (unsubscribeTaskEvent) {
                unsubscribeTaskEvent();
                unsubscribeTaskEvent = null;
            }
            if (refreshTimer !== null) {
                clearTimeout(refreshTimer);
                refreshTimer = null;
            }
        },

        async load() {
            const isCurrent = beginLatestRequest(this, "tasks");
            this.loading = true;
            try {
                const values = await listTasks();
                if (!isCurrent()) return this.items;
                this.items = Array.isArray(values) ? values : [];
                this.refreshError = "";
                if (
                    this.selectedID &&
                    !this.items.some((item) => item.id === this.selectedID)
                ) {
                    this.selectedID = "";
                }
                if (!this.selectedID && this.items.length > 0) {
                    this.selectedID = this.items[0].id;
                }
                return this.items;
            } catch (error) {
                if (isCurrent()) throw error;
                return this.items;
            } finally {
                if (isCurrent()) this.loading = false;
            }
        },

        async refresh() {
            const isCurrent = beginLatestRequest(this, "refresh");
            try {
                await this.load();
                // 等待列表期间用户可能切换任务，继续加载当前选择，不恢复旧选择。
                if (isCurrent() && this.selectedID) {
                    await this.loadRuns(this.selectedID);
                }
            } catch (error) {
                if (isCurrent()) throw error;
            }
        },

        async select(id) {
            this.selectedID = id;
            if (id) {
                await this.loadRuns(id);
            }
        },

        async loadRuns(taskID) {
            if (!taskID) {
                return [];
            }
            const isCurrent = beginLatestRequest(this, `runs:${taskID}`);
            this.loadingRuns = {
                ...this.loadingRuns,
                [taskID]: true,
            };
            try {
                const values = await listTaskRuns(taskID);
                const runs = Array.isArray(values) ? values : [];
                if (!isCurrent()) return this.runsByTask[taskID] ?? [];
                this.runsByTask = {
                    ...this.runsByTask,
                    [taskID]: runs,
                };
                return runs;
            } catch (error) {
                if (isCurrent()) throw error;
                return this.runsByTask[taskID] ?? [];
            } finally {
                if (isCurrent()) {
                    this.loadingRuns = { ...this.loadingRuns, [taskID]: false };
                }
            }
        },

        invalidateRuns(taskID) {
            // 删除、清空成功后，仍在途的旧查询不能把已移除的记录放回页面。
            invalidateRequests(this, `runs:${taskID}`);
            this.loadingRuns = { ...this.loadingRuns, [taskID]: false };
        },

        async create(request) {
            const value = await createTask(request);
            await this.load();
            this.selectedID = value.id;
            this.runsByTask = {
                ...this.runsByTask,
                [value.id]: [],
            };
            return value;
        },

        async update(id, request) {
            const value = await updateTask(id, request);
            await this.load();
            return value;
        },

        async setStatus(id, status) {
            const value = await setTaskStatus(id, status);
            invalidateRequests(this, "tasks");
            this.loading = false;
            const index = this.items.findIndex((item) => item.id === id);
            if (index >= 0) {
                this.items[index] = value;
            } else {
                await this.load();
            }
            return value;
        },

        async archive(id) {
            await archiveTask(id);
            this.invalidateRuns(id);
            const nextRuns = { ...this.runsByTask };
            delete nextRuns[id];
            this.runsByTask = nextRuns;
            await this.load();
        },

        async remove(id) {
            const deletedSessionIDs = await deleteTask(id);
            this.invalidateRuns(id);
            const nextRuns = { ...this.runsByTask };
            delete nextRuns[id];
            this.runsByTask = nextRuns;
            await useSessionStore().forgetSessions(deletedSessionIDs);
            await this.load();
        },

        async removeRun(taskID, runID) {
            const deletedSessionIDs = await deleteTaskRun(runID);
            this.invalidateRuns(taskID);
            this.runsByTask = {
                ...this.runsByTask,
                [taskID]: (this.runsByTask[taskID] ?? []).filter((run) => run.id !== runID),
            };
            await useSessionStore().forgetSessions(deletedSessionIDs);
            await this.loadRuns(taskID);
        },

        async clearRuns(taskID) {
            const deletedSessionIDs = await clearTaskRuns(taskID);
            this.invalidateRuns(taskID);
            this.runsByTask = {
                ...this.runsByTask,
                [taskID]: [],
            };
            await useSessionStore().forgetSessions(deletedSessionIDs);
        },

        async runNow(id) {
            const value = await runTaskNow(id);
            await this.loadRuns(id);
            return value;
        },

        async cancelRun(taskID, runID) {
            const value = await cancelTaskRun(runID);
            await this.loadRuns(taskID);
            return value;
        },
    },
});
