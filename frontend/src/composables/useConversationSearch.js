import { onScopeDispose, ref, watch } from "vue";
import { searchSessionMessages } from "../api/sessions.js";
import { pollIndexedSearch } from "../utils/searchPolling.js";
import { Message } from "../utils/uiMessage.js";

// 每个侧栏拥有独立的防抖和查询代次；旧索引更新与卸载后的响应不能覆盖当前结果。
export function useConversationSearch(search) {
  const contentResults = ref([]);
  const contentSearching = ref(false);
  let sequence = 0;
  let timer;
  watch(search, (value) => {
    clearTimeout(timer);
    const current = ++sequence;
    const query = value.trim();
    if (query.length < 2) {
      contentResults.value = [];
      contentSearching.value = false;
      return;
    }
    contentSearching.value = true;
    timer = setTimeout(async () => {
      try {
        await pollIndexedSearch(
          () => searchSessionMessages(query),
          () => current === sequence,
          (results) => {
            contentResults.value = results;
          },
        );
      } catch (error) {
        if (current === sequence)
          Message.error(error?.message ?? String(error));
      } finally {
        if (current === sequence) contentSearching.value = false;
      }
    }, 350);
  });
  onScopeDispose(() => {
    clearTimeout(timer);
    sequence++;
  });
  return { contentResults, contentSearching };
}
