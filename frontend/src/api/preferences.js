import { Call } from "@wailsio/runtime";

const serviceName =
    "github.com/sda1-hacker/humbert-agent/internal/services.PreferenceService";

export function getUserProfile() {
    return Call.ByName(`${serviceName}.GetUserProfile`);
}

export function updateUserProfile(request) {
    return Call.ByName(`${serviceName}.UpdateUserProfile`, request);
}

export function saveLanguage(language) {
    return Call.ByName(`${serviceName}.SetLanguage`, language);
}

export function listPersonalMemories() {
    return Call.ByName(`${serviceName}.ListPersonalMemories`);
}

export function addPersonalMemory(text) {
    return Call.ByName(`${serviceName}.AddPersonalMemory`, text);
}

export function addPersonalMemoryFromMessage(text, sessionID, entryID) {
    return Call.ByName(`${serviceName}.AddPersonalMemoryFromMessage`, text, sessionID, entryID);
}

export function updatePersonalMemory(id, text) {
    return Call.ByName(`${serviceName}.UpdatePersonalMemory`, id, text);
}

export function deletePersonalMemory(id) {
    return Call.ByName(`${serviceName}.DeletePersonalMemory`, id);
}
