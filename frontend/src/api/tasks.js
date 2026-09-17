import {
    Call,
} from "@wailsio/runtime";

const taskServiceName =
    "github.com/sda1-hacker/humbert-agent/internal/services.TaskService";

export function listTasks() {
    return Call.ByName(`${taskServiceName}.List`);
}

export function listTaskRuns(taskID) {
    return Call.ByName(`${taskServiceName}.Runs`, taskID);
}

export function createTask(request) {
    return Call.ByName(`${taskServiceName}.Create`, request);
}

export function updateTask(id, request) {
    return Call.ByName(`${taskServiceName}.Update`, id, request);
}

export function setTaskStatus(id, status) {
    return Call.ByName(`${taskServiceName}.SetStatus`, id, { status });
}

export function archiveTask(id) {
    return Call.ByName(`${taskServiceName}.Archive`, id);
}

export function deleteTask(id) {
    return Call.ByName(`${taskServiceName}.Delete`, id);
}

export function deleteTaskRun(id) {
    return Call.ByName(`${taskServiceName}.DeleteRun`, id);
}

export function clearTaskRuns(taskID) {
    return Call.ByName(`${taskServiceName}.ClearRuns`, taskID);
}

export function runTaskNow(id) {
    return Call.ByName(`${taskServiceName}.RunNow`, id);
}

export function cancelTaskRun(id) {
    return Call.ByName(`${taskServiceName}.CancelRun`, id);
}
