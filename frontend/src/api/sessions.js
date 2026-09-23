import { Call } from "@wailsio/runtime";

const sessionServiceName =
    "github.com/sda1-hacker/humbert-agent/internal/services.SessionService";

export function listSessions(
    agentID,
) {
    return Call.ByName(`${sessionServiceName}.List`, agentID);
}

export function createSession(
    agentID,
    title,
) {
    return Call.ByName(`${sessionServiceName}.Create`, agentID, title);
}

export function renameSession(
    id,
    title,
) {
    return Call.ByName(`${sessionServiceName}.Rename`, id, title);
}

export function setSessionArchived(id, archived) {
    return Call.ByName(`${sessionServiceName}.SetArchived`, id, archived);
}

export function searchSessionMessages(query) {
    return Call.ByName(`${sessionServiceName}.Search`, query);
}

export function deleteSession(
    id,
) {
    return Call.ByName(`${sessionServiceName}.Delete`, id);
}

export function listMessages(
    sessionID,
    limit = 200,
) {
    return Call.ByName(`${sessionServiceName}.Messages`, sessionID, limit);
}

export function listMessagePage(
    sessionID,
    beforeEntryID = "",
    limit = 80,
) {
    return Call.ByName(
        `${sessionServiceName}.MessagePage`,
        sessionID,
        beforeEntryID,
        limit,
    );
}

export function getMessageWindow(sessionID, entryID) {
    return Call.ByName(`${sessionServiceName}.MessageWindow`, sessionID, entryID);
}

export function readAttachment(sessionID, attachmentID) {
    return Call.ByName(
        `${sessionServiceName}.ReadAttachment`,
        sessionID,
        attachmentID,
    );
}
