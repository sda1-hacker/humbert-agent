import { defineStore } from "pinia";
import { beginLatestRequest, invalidateRequests } from "../utils/latestRequest.js";
import * as api from "../api/models.js";
import { modelDraft, presetForModel } from "../utils/modelCatalog.js";

// 唯一模型状态源；配置写入与列表同步分开，防止保存成功、刷新失败时重复创建。
export const useModelStore = defineStore("models", {
  state: () => ({ revision: 0, providers: [], models: [], multimedia: { imageModelID: "" }, loading: false, syncError: "" }),
  getters: {
    enabledModels: state => state.models.filter(model => model.enabled),
    modelByID: state => id => state.models.find(model => model.id === id) ?? null,
  },
  actions: {
    async load() {
      const current = beginLatestRequest(this, "models");
      this.loading = true;
      try {
        const result = await api.getModelState();
        if (!current()) return;
        this.revision = Number(result.revision ?? 0);
        this.providers = Array.isArray(result.providers) ? result.providers : [];
        this.models = Array.isArray(result.models) ? result.models : [];
        this.multimedia = { imageModelID: result.multimedia?.imageModelID ?? "" };
        this.syncError = "";
      } catch (error) { if (current()) throw error; }
      finally { if (current()) this.loading = false; }
    },
    async sync() {
      try { await this.load(); }
      catch { this.syncError = "配置已保存，但列表同步失败。请刷新配置。"; }
    },
    async change(collection, id, operation, refresh = true) {
      const result = await operation();
      // 只有后端明确确认写入成功才修改本地状态；早先的快照不能覆盖本次提交。
      invalidateRequests(this, "models");
      this.loading = false;
      this.revision++;
      const values = this[collection].filter(value => value.id !== (id || result?.id));
      this[collection] = result ? [...values, result] : values;
      if (collection === "providers" && result) {
        this.models = this.models.map(model => model.providerID === result.id
          ? { ...model, providerName: result.name, providerType: result.type } : model);
      }
      if (refresh) await this.sync();
      return result;
    },
    createProvider(request) { return this.change("providers", "", () => api.createProvider(request)); },
    updateProvider(id, request) { return this.change("providers", id, () => api.updateProvider(id, request)); },
    deleteProvider(id) { return this.change("providers", id, () => api.deleteProvider(id)); },
    createModel(request) { return this.change("models", "", () => api.createModel(request)); },
    updateModel(id, request) { return this.change("models", id, () => api.updateModel(id, request)); },
    deleteModel(id) { return this.change("models", id, () => api.deleteModel(id)); },
    testModel(id) { return api.testModel(id); },
    diagnoseModel(id, kind = "text") { return kind === "text" ? api.diagnoseModel(id) : api.probeModel(id, kind); },
    async importModels(providerID, names) {
      const failures = [];
      for (const name of names) {
        if (this.models.some(model => model.providerID === providerID && model.modelName === name)) continue;
        const provider = this.providers.find(provider => provider.id === providerID);
        const preset = presetForModel(provider, name);
        const { id, ...request } = modelDraft(providerID, null, preset);
        if (!preset) Object.assign(request, { contextWindow: 4096, maxOutputTokens: 1024 });
        request.modelName = name;
        request.displayName ||= name;
        try { await this.change("models", "", () => api.createModel(request), false); }
        catch (error) { failures.push({ name, message: error?.message || String(error) }); }
      }
      await this.sync();
      return failures;
    },
    async updateMultimediaConfig(request) {
      const result = await api.updateMultimediaConfig(request);
      invalidateRequests(this, "models");
      this.loading = false;
      this.multimedia = { imageModelID: result?.imageModelID ?? "" };
      this.revision++;
      return result;
    },
  },
});
