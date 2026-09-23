import { Call } from "@wailsio/runtime";

const service = "github.com/sda1-hacker/humbert-agent/internal/services.DeskService";
export const listDeskNotes = (agentID) => Call.ByName(`${service}.List`, agentID);
export const addDeskNote = (agentID, text) => Call.ByName(`${service}.Add`, agentID, text);
export const retryDeskNote = (taskID) => Call.ByName(`${service}.Retry`, taskID);
