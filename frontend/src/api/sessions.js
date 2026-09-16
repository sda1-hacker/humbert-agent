import { Call } from "@wailsio/runtime";

const sessionServiceName =
    "github.com/sda1-hacker/humbert-agent/internal/services.SessionService";

let bindingPromise = null;

/**
 * 延迟加载 SessionService Wails Binding。
 */
function loadBinding() {
    if (!bindingPromise) {
        bindingPromise = import(
            "../../bindings/github.com/sda1-hacker/humbert-agent/internal/services/sessionservice.js"
            ).catch((error) => {
            bindingPromise = null;

            throw new Error(
                "无法加载 SessionService Binding",
                {
                    cause: error,
                },
            );
        });
    }

    return bindingPromise;
}

export async function listSessions(
    agentID,
) {
    const binding = await loadBinding();

    return binding.List(agentID);
}

export async function createSession(
    agentID,
    title,
) {
    const binding = await loadBinding();

    return binding.Create(
        agentID,
        title,
    );
}

export async function renameSession(
    id,
    title,
) {
    const binding = await loadBinding();

    return binding.Rename(
        id,
        title,
    );
}

export async function deleteSession(
    id,
) {
    const binding = await loadBinding();

    return binding.Delete(id);
}

export async function listMessages(
    sessionID,
    limit = 200,
) {
    const binding = await loadBinding();

    return binding.Messages(
        sessionID,
        limit,
    );
}
export function readAttachment(sessionID, attachmentID) {
    return Call.ByName(
        `${sessionServiceName}.ReadAttachment`,
        sessionID,
        attachmentID,
    );
}
