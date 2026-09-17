import { defineStore } from "pinia";

import {
    createModel as apiCreateModel,
    createProvider as apiCreateProvider,
    deleteModel as apiDeleteModel,
    deleteProvider as apiDeleteProvider,
    getModelState,
    testModel as apiTestModel,
    updateModel as apiUpdateModel,
    updateProvider as apiUpdateProvider,
    updateMultimediaConfig as apiUpdateMultimediaConfig,
} from "../api/models.js";

/**
 * ModelStore 是前端 Model Registry 的唯一状态来源。
 *
 * 以前 ModelSettings 和 AgentWorkspace 各自保存一份 models，
 * 导致 ModelSettings 新增模型后 Agent 表单仍然看到旧数据。
 *
 * 从现在开始：
 *
 *   ModelSettings
 *   Agent 编辑表单
 *   Composer
 *
 * 全部读取同一个 Store。
 */
export const useModelStore =
    defineStore(
        "models",
        {
            state: () => ({
                revision: 0,

                providers: [],

                models: [],

                multimedia: {
                    imageModelID: "",
                },

                loading: false,
            }),

            getters: {
                /**
                 * Agent 只允许选择 Enabled Model。
                 */
                enabledModels(state) {
                    return state.models.filter(
                        (model) => model.enabled,
                    );
                },

                /**
                 * 根据 ID 查找模型。
                 */
                modelByID: (state) => (
                    id,
                ) => {
                    return (
                        state.models.find(
                            (model) =>
                                model.id === id,
                        ) ?? null
                    );
                },
            },

            actions: {
                /**
                 * 从 Go ModelRegistry 重新读取完整状态。
                 */
                async load() {
                    this.loading = true;

                    try {
                        const result =
                            await getModelState();

                        this.revision =
                            Number(
                                result.revision ?? 0,
                            );

                        this.providers =
                            Array.isArray(
                                result.providers,
                            )
                                ? result.providers
                                : [];

                        this.models =
                            Array.isArray(
                                result.models,
                            )
                                ? result.models
                                : [];

                        this.multimedia = {
                            imageModelID: result.multimedia?.imageModelID ?? "",
                        };
                    } finally {
                        this.loading = false;
                    }
                },

                async createProvider(
                    request,
                ) {
                    const result =
                        await apiCreateProvider(
                            request,
                        );

                    await this.load();

                    return result;
                },

                async updateProvider(
                    id,
                    request,
                ) {
                    const result =
                        await apiUpdateProvider(
                            id,
                            request,
                        );

                    await this.load();

                    return result;
                },

                async deleteProvider(id) {
                    await apiDeleteProvider(id);

                    await this.load();
                },

                async createModel(request) {
                    const result =
                        await apiCreateModel(
                            request,
                        );

                    await this.load();

                    return result;
                },

                async updateModel(
                    id,
                    request,
                ) {
                    const result =
                        await apiUpdateModel(
                            id,
                            request,
                        );

                    await this.load();

                    return result;
                },

                async deleteModel(id) {
                    await apiDeleteModel(id);

                    await this.load();
                },

                async testModel(id) {
                    return apiTestModel(id);
                },

                async updateMultimediaConfig(request) {
                    const result = await apiUpdateMultimediaConfig(request);
                    this.multimedia = {
                        imageModelID: result?.imageModelID ?? "",
                    };
                    this.revision += 1;
                    return result;
                },
            },
        },
    );
