import { defineStore } from "pinia";

import {
    createProject,
    deleteProject,
    listProjects,
    updateProject,
} from "../api/projects.js";
import { setAgentModel } from "../api/agents.js";

/**
 * ProjectStore 的兼容门面。
 *
 * 历史 UI 仍通过 useAgentStore() 访问“项目列表”，因此暂时保留 store id/name；
 * 但 items 从现在起来自 ProjectService。Project 拥有 Workspace，Agent 拥有
 * Instruction/Model/Skills/Security。后端写操作已经按领域命令拆分。
 */
export const useAgentStore = defineStore("agents", {
    state: () => ({
        items: [],
        selectedID: "",
        loading: false,
    }),

    getters: {
        selectedProject(state) {
            return state.items.find((project) => project.id === state.selectedID) ?? null;
        },

        // 兼容现有 Chat/Settings 组件；返回的是 Project 聚合 DTO。
        selectedAgent(state) {
            return state.items.find((project) => project.id === state.selectedID) ?? null;
        },
    },

    actions: {
        async load() {
            this.loading = true;
            try {
                const result = await listProjects();
                this.items = Array.isArray(result) ? result : [];
                if (!this.items.some((project) => project.id === this.selectedID)) {
                    this.selectedID = this.items[0]?.id ?? "";
                }
            } finally {
                this.loading = false;
            }
        },

        select(id) {
            if (!this.items.some((project) => project.id === id)) return;
            this.selectedID = id;
        },

        async create(request) {
            const result = await createProject(request);
            await this.load();
            this.selectedID = result.id;
            return result;
        },

        async update(id, request) {
            const result = await updateProject(id, request);
            const index = this.items.findIndex((item) => item.id === id);
            if (index >= 0) this.items[index] = result;
            else await this.load();
            return result;
        },

        async remove(id) {
            await deleteProject(id);
            if (this.selectedID === id) this.selectedID = "";
            await this.load();
        },

        /**
         * Composer 的模型切换只表达“设置模型”这一件事。
         * 不再为了改 modelID 回传完整 Agent/Profile。
         */
        async switchSelectedModel(modelID) {
            const project = this.selectedProject;
            if (!project) throw new Error("当前没有选择 Project");
            if (project.modelID === modelID) return project;

            const agentID = project.agentID || project.id;
            const agent = await setAgentModel(agentID, modelID);
            const index = this.items.findIndex((item) => item.id === project.id);
            if (index >= 0) {
                this.items[index] = {
                    ...this.items[index],
                    modelID: agent.modelID,
                    modelDisplayName: agent.modelDisplayName,
                    updatedAt: agent.updatedAt || this.items[index].updatedAt,
                };
                return this.items[index];
            }

            await this.load();
            return this.selectedProject;
        },
    },
});
