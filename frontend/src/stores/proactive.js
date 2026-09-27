import { defineStore } from "pinia";
import { Events } from "@wailsio/runtime";
import { beginLatestRequest, invalidateRequests } from "../utils/latestRequest.js";

import {
    getProactiveSettings,
    getProactiveStatus,
    listProactiveRecords,
    listRecentNotifications,
    runProactiveHeartbeat,
    updateProactiveSettings,
} from "../api/proactive.js";

const proactiveEventName = "humbert:proactive:event";
const notificationEventName = "humbert:notification";

let unsubscribeProactive = null;
let unsubscribeNotification = null;
let refreshTimer = null;
const seenNotificationIDs = new Set();

export const useProactiveStore = defineStore("proactive", {
    state: () => ({
        settings: null,
        status: null,
        records: [],
        notification: null,
        notificationSequence: 0,
        loading: false,
        saving: false,
        runningHeartbeat: false,
    }),

    actions: {
        initialiseEvents() {
            if (!unsubscribeProactive) {
                unsubscribeProactive = Events.On(proactiveEventName, (event) => {
                    const payload = event?.data;
                    if (payload?.status) {
                        invalidateRequests(this, "status");
                        this.status = normalizeStatus(payload.status);
                    }
                    if (payload?.record) {
                        this.upsertRecord(payload.record);
                    }
                    if (refreshTimer !== null) clearTimeout(refreshTimer);
                    refreshTimer = setTimeout(() => {
                        refreshTimer = null;
                        // 事件可能淘汰正在读取的列表；重新取完整目录以补齐其他记录。
                        void Promise.all([this.refreshStatus(), this.refreshRecords()]).catch(console.error);
                    }, 120);
                });
            }
            if (!unsubscribeNotification) {
                unsubscribeNotification = Events.On(notificationEventName, (event) => {
                    const payload = event?.data;
                    if (!payload || typeof payload !== "object") return;
                    this.ingestNotification(payload);
                });
            }
        },

        disposeEvents() {
            invalidateRequests(this);
            this.loading = false;
            if (unsubscribeProactive) {
                unsubscribeProactive();
                unsubscribeProactive = null;
            }
            if (unsubscribeNotification) {
                unsubscribeNotification();
                unsubscribeNotification = null;
            }
            if (refreshTimer !== null) {
                clearTimeout(refreshTimer);
                refreshTimer = null;
            }
        },

        async load() {
            const isCurrent = beginLatestRequest(this, "load");
            this.loading = true;
            try {
                const [, , , notifications] = await Promise.all([
                    this.refreshSettings(),
                    this.refreshStatus(),
                    this.refreshRecords(),
                    listRecentNotifications(20),
                ]);
                if (!isCurrent()) return;
                for (const notification of (Array.isArray(notifications) ? notifications : [])) {
                    this.ingestNotification(notification);
                }
            } catch (error) {
                if (isCurrent()) throw error;
            } finally {
                if (isCurrent()) this.loading = false;
            }
        },

        async refreshSettings() {
            const isCurrent = beginLatestRequest(this, "settings");
            try {
                const value = await getProactiveSettings();
                if (isCurrent()) this.settings = normalizeSettings(value);
                return this.settings;
            } catch (error) { if (isCurrent()) throw error; }
        },

        async save(value) {
            this.saving = true;
            try {
                const updated = await updateProactiveSettings(value);
                invalidateRequests(this, "settings");
                this.settings = normalizeSettings(updated);
                await this.refreshStatus();
                return this.settings;
            } finally {
                this.saving = false;
            }
        },

        async refreshStatus() {
            const isCurrent = beginLatestRequest(this, "status");
            try {
                const value = await getProactiveStatus();
                if (isCurrent()) this.status = normalizeStatus(value);
                return this.status;
            } catch (error) { if (isCurrent()) throw error; }
        },

        async refreshRecords() {
            const isCurrent = beginLatestRequest(this, "records");
            try {
                const values = await listProactiveRecords(50);
                if (isCurrent()) this.records = Array.isArray(values) ? values : [];
                return this.records;
            } catch (error) { if (isCurrent()) throw error; }
        },

        async runHeartbeat() {
            this.runningHeartbeat = true;
            try {
                await runProactiveHeartbeat();
                await Promise.all([this.refreshStatus(), this.refreshRecords()]);
            } finally {
                this.runningHeartbeat = false;
            }
        },

        ingestNotification(value) {
            if (!value || typeof value !== "object") return;
            const id = value.id || `${value.title || ""}:${value.createdAt || ""}`;
            if (seenNotificationIDs.has(id)) return;
            seenNotificationIDs.add(id);
            if (seenNotificationIDs.size > 200) {
                const first = seenNotificationIDs.values().next().value;
                if (first) seenNotificationIDs.delete(first);
            }
            this.notification = value;
            this.notificationSequence += 1;
        },

        upsertRecord(value) {
            if (!value?.id) return;
            // 实时状态已比正在读取的快照更新，旧列表不能把终态改回执行中。
            invalidateRequests(this, "records");
            const next = [...this.records];
            const index = next.findIndex((item) => item.id === value.id);
            if (index >= 0) next[index] = value;
            else next.unshift(value);
            this.records = next.slice(0, 50);
        },
    },
});

function normalizeStatus(value) {
    if (!value || typeof value !== "object") {
        return { running: false, lastHeartbeatAt: "", nextHeartbeatAt: "", queuedEvents: 0 };
    }
    return {
        running: Boolean(value.running),
        lastHeartbeatAt: value.lastHeartbeatAt ?? value.last_heartbeat_at ?? "",
        nextHeartbeatAt: value.nextHeartbeatAt ?? value.next_heartbeat_at ?? "",
        queuedEvents: Number(value.queuedEvents ?? value.queued_events) || 0,
    };
}

function normalizeSettings(value) {
    const rules = value?.rules && typeof value.rules === "object" ? value.rules : {};
    return {
        enabled: Boolean(value?.enabled),
        heartbeatIntervalMinutes: Number(value?.heartbeatIntervalMinutes) || 5,
        quietHours: {
            enabled: Boolean(value?.quietHours?.enabled),
            start: value?.quietHours?.start ?? "22:00",
            end: value?.quietHours?.end ?? "08:00",
            timeZone: value?.quietHours?.timeZone ?? "",
        },
        rules: { ...rules },
    };
}
