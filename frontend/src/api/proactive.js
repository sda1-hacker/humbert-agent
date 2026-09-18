import { Call } from "@wailsio/runtime";

const serviceName =
    "github.com/sda1-hacker/humbert-agent/internal/services.ProactiveService";

export function getProactiveSettings() {
    return Call.ByName(`${serviceName}.GetSettings`);
}

export function updateProactiveSettings(value) {
    return Call.ByName(`${serviceName}.UpdateSettings`, value);
}

export function getProactiveStatus() {
    return Call.ByName(`${serviceName}.Status`);
}

export function listProactiveRecords(limit = 50) {
    return Call.ByName(`${serviceName}.Records`, limit);
}

export function listRecentNotifications(limit = 20) {
    return Call.ByName(`${serviceName}.Notifications`, limit);
}

export function runProactiveHeartbeat() {
    return Call.ByName(`${serviceName}.RunHeartbeat`);
}
