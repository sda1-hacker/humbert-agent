import { defineStore } from "pinia";
import { beginLatestRequest, invalidateRequests } from "../utils/latestRequest.js";

import {
    createAgent,
    deleteAgent,
    listAgents,
    setAgentModel,
    updateAgent,
} from "../api/agents.js";

// 保存成功后，所有在途列表都可能包含修改前的数据，不能再提交到页面。
function invalidateAgentList(store) {
    invalidateRequests(store, "agents");
    store.loading = false;
}

/**
 * Agent Store。Agent 自己拥有 Workspace、Model Roles、Skills 与安全配置。
 */
export const useAgentStore = defineStore("agents", {
    state: () => ({
        items: [],
        selectedID: "",
        loading: false,
    }),

    getters: {
        selectedAgent(state) {
            return state.items.find((agent) => agent.id === state.selectedID) ?? null;
        },
    },

    actions: {
        async load() {
            const isCurrent = beginLatestRequest(this, "agents");
            this.loading = true;
            try {
                const result = await listAgents();
                if (!isCurrent()) return this.items;
                this.items = Array.isArray(result) ? result : [];
                if (!this.items.some((agent) => agent.id === this.selectedID)) {
                    this.selectedID = this.items[0]?.id ?? "";
                }
                return this.items;
            } catch (error) {
                if (isCurrent()) throw error;
                return this.items;
            } finally {
                if (isCurrent()) this.loading = false;
            }
        },

        select(id) {
            if (!this.items.some((agent) => agent.id === id)) return;
            this.selectedID = id;
        },

        // Skill 页面完成领域更新后从这里提交，避免绕过 Store 的旧请求失效规则。
        applySkillSelection(id, enabledSkills) {
            invalidateAgentList(this);
            const index = this.items.findIndex((item) => item.id === id);
            if (index >= 0) this.items[index] = { ...this.items[index], enabledSkills: [...enabledSkills] };
        },

        async create(request) {
            const result = await createAgent(request);
            invalidateAgentList(this);
            await this.load();
            this.selectedID = result.id;
            return result;
        },

        async update(id, request) {
            // 新建时的默认能力选择开关不属于更新协议，更新只提交明确字段。
            const fields = { ...request };
            delete fields.builtinToolsConfigured;
            const result = await updateAgent(id, fields);
            invalidateAgentList(this);
            const index = this.items.findIndex((item) => item.id === id);
            if (index >= 0) this.items[index] = result;
            else await this.load();
            return result;
        },

        async remove(id) {
            await deleteAgent(id);
            invalidateAgentList(this);
            this.items = this.items.filter((agent) => agent.id !== id);
            if (this.selectedID === id) this.selectedID = this.items[0]?.id ?? "";
            await this.load();
        },

        /**
         * Composer 的模型切换只表达“设置模型”这一件事。
         * 不会回传 Workspace、Sandbox、Skills 等其它 Agent 配置。
         */
        async switchSelectedModel(modelID) {
            const agent = this.selectedAgent;
            if (!agent) throw new Error("当前没有选择 Agent");
            if (agent.modelID === modelID) return agent;

            const updated = await setAgentModel(agent.id, modelID);
            invalidateAgentList(this);
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
            return this.items.find((item) => item.id === agent.id) ?? updated;
        },
    },
});
