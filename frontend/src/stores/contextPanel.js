import { defineStore } from "pinia";

const STORAGE_KEY = "humbert.ui.context-panel.v4";
const DEFAULT_WIDTH = 500;

function readPreference() {
  try {
    const value = JSON.parse(window.localStorage.getItem(STORAGE_KEY)
      || window.localStorage.getItem("humbert.ui.context-panel.v3")
      || window.localStorage.getItem("humbert.ui.context-panel.v2")
      || window.localStorage.getItem("humbert.ui.context-panel.v1") || "{}");
    const storedWidth = Number(value.width);
    return {
      open: typeof value.open === "boolean" ? value.open : false,
      width: Number.isFinite(storedWidth) && storedWidth >= 380
        ? Math.min(800, storedWidth) : DEFAULT_WIDTH,
      treeWidth: Math.min(400, Math.max(125, Number(value.treeWidth) || 175)),
    };
  } catch {
    return { open: false, width: DEFAULT_WIDTH, treeWidth: 175 };
  }
}
function savePreference(state) {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify({
      open: state.open, width: state.width, treeWidth: state.treeWidth,
    }));
  } catch { /* Layout preferences are optional. */ }
}

const preference = readPreference();
export const useContextPanelStore = defineStore("contextPanel", {
  state: () => ({ ...preference }),
  actions: {
    setOpen(value) { this.open = Boolean(value); savePreference(this); },
    setWidth(value) { this.width = Math.min(800, Math.max(380, Math.round(Number(value) || DEFAULT_WIDTH))); savePreference(this); },
    setTreeWidth(value) { this.treeWidth = Math.min(400, Math.max(125, Math.round(Number(value) || 175))); savePreference(this); },
  },
});
