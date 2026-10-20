import { Call } from "@wailsio/runtime";

const service = "github.com/sda1-hacker/humbert-agent/internal/services.KnowledgeService";
const call = (method, ...args) => Call.ByName(`${service}.${method}`, ...args);

export const knowledgeState = () => call("State");
export const saveKnowledgeSettings = (request) => call("SaveSettings", request);
export const initializeKnowledge = () => call("Initialize");
export const saveCollection = (collection) => call("SaveCollection", collection);
export const listDocuments = (collection, offset = 0) => call("Documents", collection, offset);
export const uploadDocuments = (collection) => call("Upload", collection);
export const cancelDocument = (collection, document) => call("Cancel", collection, document);
export const retryDocument = (collection, document) => call("Retry", collection, document);
export const reindexDocument = (collection, document) => call("Reindex", collection, document);
export const deleteDocument = (collection, document) => call("Delete", collection, document);
export const downloadDocument = (collection, document) => call("Download", collection, document);
export const previewDocument = (collection, document, offset = 0) => call("PreviewDocument", collection, document, offset);
export const searchKnowledge = (collection, query, mode = "hybrid") => call("Search", collection, query, mode);
export const readKnowledgeSource = (agent, id) => call("ReadSource", agent, id);
export const previewKnowledgeSource = (id) => call("PreviewSource", id);
