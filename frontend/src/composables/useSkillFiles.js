import { computed, onScopeDispose, ref } from "vue";
import { getSkillDetail, readSkillFile } from "../api/skills.js";
import {
  beginLatestRequest,
  invalidateRequests,
} from "../utils/latestRequest.js";
import { Message } from "../utils/uiMessage.js";

// File previews belong to this view. Stale detail/file replies cannot overwrite a new selection.
export function useSkillFiles(skill) {
  const requests = {};
  const detail = ref(null),
    loading = ref(false),
    detailError = ref("");
  const selectedPath = ref(""),
    fileContent = ref(""),
    fileLoading = ref(false),
    fileError = ref("");
  const files = computed(() =>
    Array.isArray(detail.value?.files) ? detail.value.files : [],
  );
  onScopeDispose(() => invalidateRequests(requests));

  async function loadFile(path) {
    const isCurrent = beginLatestRequest(requests, "file");
    const name = skill()?.name;
    const file = files.value.find((item) => item.path === path);
    selectedPath.value = path;
    fileContent.value = "";
    fileError.value = "";
    fileLoading.value = false;
    if (!file?.text) {
      fileError.value =
        "这个文件不是 UTF-8 文本。详情页只展示文件信息，不读取二进制内容。";
      return;
    }
    fileLoading.value = true;
    try {
      const result = await readSkillFile(name, path);
      if (isCurrent() && skill()?.name === name)
        fileContent.value = result?.content ?? "";
    } catch (error) {
      if (!isCurrent() || skill()?.name !== name) return;
      fileError.value = error?.message ?? String(error);
      Message.error(fileError.value);
    } finally {
      if (isCurrent()) fileLoading.value = false;
    }
  }

  async function loadDetail() {
    const isCurrent = beginLatestRequest(requests, "detail");
    invalidateRequests(requests, "file");
    detail.value = null;
    detailError.value = "";
    selectedPath.value = "";
    fileContent.value = "";
    fileError.value = "";
    fileLoading.value = false;
    loading.value = false;
    const current = skill();
    if (!current?.name || !current.valid) return;
    const name = current.name;
    loading.value = true;
    try {
      const result = await getSkillDetail(name);
      if (!isCurrent() || skill()?.name !== name) return;
      detail.value = result;
      const first =
        result?.files?.find((file) => file.path === "SKILL.md" && file.text) ??
        result?.files?.find((file) => file.text);
      if (first?.path) await loadFile(first.path);
    } catch (error) {
      if (isCurrent() && skill()?.name === name)
        detailError.value = error?.message ?? String(error);
    } finally {
      if (isCurrent()) loading.value = false;
    }
  }
  return {
    detail,
    loading,
    detailError,
    selectedPath,
    fileContent,
    fileLoading,
    fileError,
    loadFile,
    loadDetail,
  };
}
