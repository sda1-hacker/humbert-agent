import { Call } from "@wailsio/runtime";

const mcpServiceName =
    "github.com/sda1-hacker/humbert-agent/internal/services.MCPService";

export function listMCPServers() {
    return Call.ByName(`${mcpServiceName}.ListServers`);
}

export function createMCPServer(request) {
    return Call.ByName(`${mcpServiceName}.CreateServer`, request);
}

export function updateMCPServer(id, request) {
    return Call.ByName(`${mcpServiceName}.UpdateServer`, id, request);
}

export function deleteMCPServer(id) {
    return Call.ByName(`${mcpServiceName}.DeleteServer`, id);
}

export function testMCPConnection(id) {
    return Call.ByName(`${mcpServiceName}.TestConnection`, id);
}

export function discoverMCPTools(id) {
    return Call.ByName(`${mcpServiceName}.DiscoverTools`, id);
}

export function getAgentMCPTools(agentID) {
    return Call.ByName(`${mcpServiceName}.GetAgentTools`, agentID);
}

export function setAgentMCPTools(agentID, selections) {
    return Call.ByName(`${mcpServiceName}.SetAgentTools`, agentID, selections);
}

export function setMCPToolRisk(serverID, rawToolName, risk) {
    return Call.ByName(`${mcpServiceName}.SetToolRisk`, serverID, rawToolName, risk);
}

export function setMCPServerEnabled(id, enabled) {
    return Call.ByName(`${mcpServiceName}.SetServerEnabled`, id, enabled);
}

export function disconnectMCPServer(id) {
    return Call.ByName(`${mcpServiceName}.DisconnectServer`, id);
}

export function refreshMCPTools(id) {
    return Call.ByName(`${mcpServiceName}.RefreshTools`, id);
}

export function exportMCPServersConfig() {
    return Call.ByName(`${mcpServiceName}.ExportServersConfig`);
}

export function importMCPServersConfig(payload) {
    return Call.ByName(`${mcpServiceName}.ImportServersConfig`, payload);
}
