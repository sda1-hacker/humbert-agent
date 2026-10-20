import { computed, onMounted, onUnmounted, provide, ref, watch } from "vue";
import { Events } from "@wailsio/runtime";
import { readKnowledgeSource } from "../api/knowledge.js";
import { useRuntimeStore } from "../stores/runtime.js";
import { knowledgeCitationContext, knowledgeSourcesOfCalls, normalizeKnowledgeSource } from "../utils/knowledgeSources.js";

export function useKnowledgeCitations(calls, agentID, sessionID, live = false) {
  const runtime = useRuntimeStore();
  const received = ref([]);
  const preview = ref(null);
  const previewOpen = ref(false);
  const previewLoading = ref(false);
  const previewError = ref("");
  let serial = 0, unsubscribe, pending;
  const sources = computed(() => {
    const result = new Map(knowledgeSourcesOfCalls(calls.value).map((s) => [s.id, s]));
    for (const source of received.value) result.set(source.id, source);
    return [...result.values()];
  });
  async function open(id) {
    const source = sources.value.find((s) => s.id === id);
    if (!source) return;
    pending?.cancel?.();
    const request = ++serial, owner = agentID();
    preview.value = source; previewOpen.value = true; previewLoading.value = true; previewError.value = "";
    try {
      pending = readKnowledgeSource(owner, id);
      const value = await pending;
      if (request === serial && owner === agentID()) preview.value = normalizeKnowledgeSource(value) || source;
    } catch (e) { if (request === serial) previewError.value = e?.message || String(e); }
    finally { if (request === serial) { previewLoading.value = false; pending = null; } }
  }
  function reset() { serial++; pending?.cancel?.(); pending = null; received.value = []; previewOpen.value = false; }
  watch(() => [agentID(), sessionID()], reset, { flush: "sync" });
  watch(previewOpen, (visible) => { if (!visible) { serial++; pending?.cancel?.(); pending = null; previewLoading.value = false; } });
  if (live) {
    watch(() => runtime.runs[sessionID()]?.requestID, (id, old) => { if (id && id !== old) reset(); }, { flush: "sync" });
    onMounted(() => {
      unsubscribe = Events.On("knowledge.sources", (event) => {
        const data = event?.data;
        const request = runtime.runs[sessionID()]?.requestID;
        if (!request || data?.request_id !== request || data.session_id !== sessionID() || data.agent_id !== agentID()) return;
        const merged = new Map(received.value.map((s) => [s.id, s]));
        for (const value of data.sources || []) { const source = normalizeKnowledgeSource(value); if (source) merged.set(source.id, source); }
        received.value = [...merged.values()].slice(-30);
      });
    });
  }
  onUnmounted(() => { reset(); unsubscribe?.(); });
  provide(knowledgeCitationContext, { sources, open });
  return { sources, preview, previewOpen, previewLoading, previewError, open };
}
