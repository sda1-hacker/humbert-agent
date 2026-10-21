import { defineStore } from "pinia";
import { toRaw } from "vue";
import { beginLatestRequest } from "../utils/latestRequest.js";
import { deleteSkill, getSkillState } from "../api/skills.js";

const pendingLoads = new WeakMap();

// Catalog is shared control-plane state. File bodies and editor drafts stay local.
function normalizeCatalog(state) {
  const items = (Array.isArray(state?.skills) ? state.skills : []).map(
    (skill) => ({
      ...skill,
      alias: typeof skill?.alias === "string" ? skill.alias : "",
      diagnostics: Array.isArray(skill?.diagnostics) ? skill.diagnostics : [],
      scriptRuntimes: Array.isArray(skill?.scriptRuntimes)
        ? skill.scriptRuntimes
        : [],
      usedByAgents: Array.isArray(skill?.usedByAgents)
        ? skill.usedByAgents
        : [],
      source:
        skill?.source && typeof skill.source === "object"
          ? skill.source
          : { known: false },
    }),
  );
  const inferred = new Map();
  for (const skill of items) {
    if (!skill?.name) continue;
    for (const agent of skill.usedByAgents) {
      if (!agent?.id) continue;
      if (!inferred.has(agent.id)) inferred.set(agent.id, new Set());
      inferred.get(agent.id).add(skill.name);
    }
  }
  const agents = (Array.isArray(state?.agents) ? state.agents : [])
    .map((agent) => ({
      id: agent?.id ?? "",
      name: agent?.name ?? "",
      enabledSkills: [
        ...new Set([
          ...(Array.isArray(agent?.enabledSkills)
            ? agent.enabledSkills.filter(Boolean)
            : []),
          ...(inferred.get(agent?.id) ?? []),
        ]),
      ],
    }))
    .filter((agent) => agent.id);
  return {
    rootDir: state?.rootDir ?? "",
    sourceError:
      typeof state?.sourceError === "string" ? state.sourceError : "",
    sourceResolvers: Array.isArray(state?.sourceResolvers)
      ? [...state.sourceResolvers]
      : [],
    items,
    agents,
  };
}

export const useSkillStore = defineStore("skills", {
  state: () => ({
    rootDir: "",
    sourceError: "",
    sourceResolvers: [],
    items: [],
    agents: [],
    loaded: false,
    loading: false,
    loadError: "",
    removingName: "",
  }),
  getters: {
    validSkills: (state) => state.items.filter((item) => item.valid),
    findSkill: (state) => (name) =>
      state.items.find(
        (item) => item.name === name || item.directoryName === name,
      ),
  },
  actions: {
    async load({ force = true } = {}) {
      const owner = toRaw(this);
      if (!force && pendingLoads.has(owner)) return pendingLoads.get(owner);
      const isCurrent = beginLatestRequest(this, "skills");
      this.loading = true;
      this.loadError = "";
      const request = Promise.resolve().then(async () => {
        try {
          const state = await getSkillState();
          if (!isCurrent()) return pendingLoads.get(owner);
          Object.assign(this, normalizeCatalog(state), { loaded: true });
        } catch (error) {
          if (!isCurrent()) return pendingLoads.get(owner);
          this.loadError = error?.message ?? String(error);
          throw error;
        } finally {
          if (isCurrent()) {
            this.loading = false;
            pendingLoads.delete(owner);
          }
        }
      });
      pendingLoads.set(owner, request);
      return request;
    },
    async remove(name) {
      if (!name || this.removingName) return;
      this.removingName = name;
      try {
        await deleteSkill(name);
        await this.load();
      } finally {
        this.removingName = "";
      }
    },
  },
});
