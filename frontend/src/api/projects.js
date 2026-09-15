import { Call } from "@wailsio/runtime";

const serviceName =
    "github.com/sda1-hacker/humbert-agent/internal/services.ProjectService";

export function listProjects() {
    return Call.ByName(`${serviceName}.ListProjects`);
}

export function getProject(id) {
    return Call.ByName(`${serviceName}.GetProject`, id);
}

export function createProject(request) {
    return Call.ByName(`${serviceName}.CreateProject`, request);
}

export function updateProject(id, request) {
    return Call.ByName(`${serviceName}.UpdateProject`, id, request);
}

export function deleteProject(id) {
    return Call.ByName(`${serviceName}.DeleteProject`, id);
}

export function selectProjectWorkspaceDirectory(currentPath = "") {
    return Call.ByName(
        `${serviceName}.SelectWorkspaceDirectory`,
        currentPath,
    );
}
