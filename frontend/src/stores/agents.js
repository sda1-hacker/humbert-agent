import { defineStore } from "pinia";

import {
    createAgent,
    deleteAgent,
    listAgents,
    setAgentModel,
    updateAgent,
} from "../api/agents.js";

/**
 * Agent Store。
 *
 * UI 中仍把 Agent 展示为“项目”，但领域层只有 Agent：Agent 自己拥有 Workspace、
 * Model Roles、Skills 与安全配置，不存在独立 Project 实体。
 */
export const useAgentStore = defineStore("agents", {
    state: () => ({
        items: [],
        selectedID: "",
        loading: false,
    }),

    getters: {
        // UI 兼容名称：Project 就是 Agent 的产品称呼。
        selectedProject(state) {
            return state.items.find((agent) => agent.id === state.selectedID) ?? null;
        },

        selectedAgent(state) {
            return state.items.find((agent) => agent.id === state.selectedID) ?? null;
        },
    },

    actions: {
        async load() {
            this.loading = true;
            try {
                const result = await listAgents();
                this.items = Array.isArray(result) ? result : [];
                if (!this.items.some((agent) => agent.id === this.selectedID)) {
                    this.selectedID = this.items[0]?.id ?? "";
                }
            } finally {
                this.loading = false;
            }
        },

        select(id) {
            if (!this.items.some((agent) => agent.id === id)) return;
            this.selectedID = id;
        },

        async create(request) {
            const result = await createAgent(request);
            await this.load();
            this.selectedID = result.id;
            return result;
        },

        async update(id, request) {
            const result = await updateAgent(id, {
                ...request,
                modelRolesConfigured: true,
                sandboxConfigured: true,
            });
            const index = this.items.findIndex((item) => item.id === id);
            if (index >= 0) this.items[index] = result;
            else await this.load();
            return result;
        },

        async remove(id) {
            await deleteAgent(id);
            if (this.selectedID === id) this.selectedID = "";
            await this.load();
        },

        /**
         * Composer 的模型切换只表达“设置模型”这一件事。
         * 不会回传 Workspace、Sandbox、Skills 等其它 Agent 配置。
         */
        async switchSelectedModel(modelID) {
            const agent = this.selectedAgent;
            if (!agent) throw new Error("当前没有选择项目");
            if (agent.modelID === modelID) return agent;

            const updated = await setAgentModel(agent.id, modelID);
            const index = this.items.findIndex((item) => item.id === agent.id);
            if (index >= 0) {
                this.items[index] = {
                    ...this.items[index],
                    modelID: updated.modelID,
                    modelDisplayName: updated.modelDisplayName,
                    updatedAt: updated.updatedAt || this.items[index].updatedAt,
                };
                return this.items[index];
            }

            await this.load();
            return this.selectedAgent;
        },
    },
});
