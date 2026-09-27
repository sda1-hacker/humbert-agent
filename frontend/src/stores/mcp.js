import { defineStore } from "pinia";
import { reactive, ref } from "vue";
import { beginLatestRequest } from "../utils/latestRequest.js";
import { listMCPServers, testMCPConnection, discoverMCPTools, refreshMCPTools } from "../api/mcp.js";
import { mcpConnectionStateLabel, mcpConnectionStateTone } from "../utils/mcp.js";

// 设置页与能力页共用服务目录、连接状态和工具缓存，页面只保留自己的表单与选择状态。
export const useMCPStore = defineStore("mcp", () => {
    const servers = ref([]);
    const loading = ref(false);
    const connectionState = reactive({});
    const toolCatalog = reactive({});
    const catalogLoading = reactive({});
    let pendingLoad = null;
    const loadRequests = {};

    function setConnection(server, state, detail = "", error = "") {
        server = servers.value.find(value => value.id === server.id) ?? server;
        server.connectionState = state;
        server.connected = state === "connected";
        server.lastError = error;
        connectionState[server.id] = {
            ok: state === "connected",
            tone: mcpConnectionStateTone(state),
            text: mcpConnectionStateLabel(state),
            detail, error,
        };
    }

    async function load({ force = false } = {}) {
        // 普通页面读取可以去重；保存后的强制刷新必须使旧快照失效。
        if (pendingLoad && !force) return pendingLoad;
        const isCurrent = beginLatestRequest(loadRequests, "servers");
        loading.value = true;
        pendingLoad = (async () => {
            const values = await listMCPServers();
            if (!isCurrent()) return;
            const previous = new Map(servers.value.map(server => [server.id, server]));
            servers.value = Array.isArray(values) ? values : [];
            const ids = new Set(servers.value.map(server => server.id));
            for (const state of [connectionState, toolCatalog, catalogLoading]) {
                for (const id of Object.keys(state)) if (!ids.has(id)) delete state[id];
            }
            for (const server of servers.value) {
                // 配置变化使上次发现的 schema 失效，下一次展开会重新发现。
                const changed = previous.get(server.id)?.updatedAt !== server.updatedAt;
                if (changed) delete toolCatalog[server.id];
                if (catalogLoading[server.id] && !changed) continue;
                const state = server.connectionState || (server.enabled === false ? "disabled" : "disconnected");
                const error = ["error", "degraded"].includes(state) ? server.lastError || "" : "";
                setConnection(server, state, error, error);
            }
        })();
        try { return await pendingLoad; }
        catch (error) { if (isCurrent()) throw error; }
        finally { if (isCurrent()) { pendingLoad = null; loading.value = false; } }
    }

    async function operate(server, operation, force = true) {
        if (!server?.id || server.enabled === false) throw new Error("请先启用 MCP 服务");
        if (catalogLoading[server.id]) return null;
        const revision = server.updatedAt;
        const isCurrent = () => servers.value.some(value => value.id === server.id && value.updatedAt === revision);
        catalogLoading[server.id] = true;
        connectionState[server.id] = { ok: false, tone: "pending", text: "正在连接", detail: "", error: "" };
        try {
            const result = operation === "test"
                ? await testMCPConnection(server.id)
                : await (force ? refreshMCPTools : discoverMCPTools)(server.id);
            if (!isCurrent()) return result;
            if (operation !== "test") toolCatalog[server.id] = Array.isArray(result) ? result : [];
            const count = operation === "test" ? result?.toolCount ?? 0 : toolCatalog[server.id].length;
            setConnection(server, "connected", `${count} 个 Tool`);
            return result;
        } catch (error) {
            const message = error?.message ?? String(error);
            if (isCurrent()) setConnection(server, "error", message, message);
            throw error;
        } finally { delete catalogLoading[server.id]; }
    }
    return { servers, loading, connectionState, toolCatalog, catalogLoading, load, operate };
});
