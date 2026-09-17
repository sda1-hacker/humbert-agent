import { Call } from "@wailsio/runtime";

const serviceName =
    "github.com/sda1-hacker/humbert-agent/internal/services.PreferenceService";

export function getUserProfile() {
    return Call.ByName(`${serviceName}.GetUserProfile`);
}

export function updateUserProfile(request) {
    return Call.ByName(`${serviceName}.UpdateUserProfile`, request);
}
