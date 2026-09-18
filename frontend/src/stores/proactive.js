import { defineStore } from "pinia";
import { Events } from "@wailsio/runtime";

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
                        this.status = normalizeStatus(payload.status);
                    }
                    if (payload?.record) {
                        this.upsertRecord(payload.record);
                    }
                    if (refreshTimer !== null) clearTimeout(refreshTimer);
                    refreshTimer = setTimeout(() => {
                        refreshTimer = null;
                        void this.refreshStatus();
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
            this.loading = true;
            try {
                const [settings, status, records, notifications] = await Promise.all([
                    getProactiveSettings(),
                    getProactiveStatus(),
                    listProactiveRecords(50),
                    listRecentNotifications(20),
                ]);
                this.settings = normalizeSettings(settings);
                this.status = normalizeStatus(status);
                this.records = Array.isArray(records) ? records : [];
                for (const notification of (Array.isArray(notifications) ? notifications : [])) {
                    this.ingestNotification(notification);
                }
            } finally {
                this.loading = false;
            }
        },

        async save(value) {
            this.saving = true;
            try {
                const updated = await updateProactiveSettings(value);
                this.settings = normalizeSettings(updated);
                await this.refreshStatus();
                return this.settings;
            } finally {
                this.saving = false;
            }
        },

        async refreshStatus() {
            this.status = normalizeStatus(await getProactiveStatus());
            return this.status;
        },

        async refreshRecords() {
            const values = await listProactiveRecords(50);
            this.records = Array.isArray(values) ? values : [];
            return this.records;
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
