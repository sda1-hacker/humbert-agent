import {
    defineStore,
} from "pinia";

import {
    deleteSkill,
    getSkillState,
} from "../api/skills.js";

/**
 * Skill Store 只保存设置页/Agent 编辑器需要的控制面状态。
 *
 * Skill 正文、references/scripts 内容与 Runtime Snapshot 都只存在 Go 后端，不复制到
 * WebView，避免把可能较大的本地指令包变成前端长期状态。
 */
export const useSkillStore =
    defineStore(
        "skills",
        {
            state: () => ({
                rootDir: "",
                sourceResolvers: [],
                items: [],
                loaded: false,
                loading: false,
                loadError: "",
                removingName: "",
            }),

            getters: {
                validSkills(state) {
                    return state.items.filter(
                        (item) => item.valid,
                    );
                },
            },

            actions: {
                /**
                 * 重新读取后端 Skill Catalog 与 Agent 引用关系。
                 */
                async load() {
                    if (this.loading) {
                        return;
                    }

                    this.loading = true;
                    this.loadError = "";
                    try {
                        const state =
                            await getSkillState();

                        this.rootDir =
                            state?.rootDir ?? "";
                        this.sourceResolvers =
                            Array.isArray(state?.sourceResolvers)
                                ? [...state.sourceResolvers]
                                : [];
                        this.items =
                            Array.isArray(state?.skills)
                                ? state.skills.map(
                                    (item) => ({
                                        ...item,
                                        usedByAgents:
                                            Array.isArray(item?.usedByAgents)
                                                ? item.usedByAgents
                                                : [],
                                    }),
                                )
                                : [];
                        this.loaded = true;
                    } catch (error) {
                        this.loadError =
                            error?.message ?? String(error);
                        throw error;
                    } finally {
                        this.loading = false;
                    }
                },

                /**
                 * 删除一个 Skill。后端会再次检查是否仍被 Agent 引用。
                 */
                async remove(name) {
                    if (!name || this.removingName) {
                        return;
                    }

                    this.removingName = name;
                    try {
                        await deleteSkill(name);
                        await this.load();
                    } finally {
                        this.removingName = "";
                    }
                },
            },
        },
    );
