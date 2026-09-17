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
            this.loading = true;
            try {
                const values = await listTasks();
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
            } finally {
                this.loading = false;
            }
        },

        async refresh() {
            const selectedID = this.selectedID;
            await this.load();
            if (selectedID && this.items.some((item) => item.id === selectedID)) {
                this.selectedID = selectedID;
            }
            if (this.selectedID) {
                await this.loadRuns(this.selectedID);
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
            this.loadingRuns = {
                ...this.loadingRuns,
                [taskID]: true,
            };
            try {
                const values = await listTaskRuns(taskID);
                const runs = Array.isArray(values) ? values : [];
                this.runsByTask = {
                    ...this.runsByTask,
                    [taskID]: runs,
                };
                return runs;
            } finally {
                this.loadingRuns = {
                    ...this.loadingRuns,
                    [taskID]: false,
                };
            }
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
            const nextRuns = { ...this.runsByTask };
            delete nextRuns[id];
            this.runsByTask = nextRuns;
            await this.load();
        },

        async remove(id) {
            await deleteTask(id);
            const nextRuns = { ...this.runsByTask };
            delete nextRuns[id];
            this.runsByTask = nextRuns;
            await this.load();
        },

        async removeRun(taskID, runID) {
            await deleteTaskRun(runID);
            await this.loadRuns(taskID);
        },

        async clearRuns(taskID) {
            await clearTaskRuns(taskID);
            this.runsByTask = {
                ...this.runsByTask,
                [taskID]: [],
            };
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
