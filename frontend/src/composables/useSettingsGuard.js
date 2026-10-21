import { h, inject, onUnmounted, ref } from "vue";
import { Button, Modal } from "@arco-design/web-vue";
import { t } from "../i18n/index.js";

export const settingsGuardKey = Symbol("settings-guard");

// 配置页共用三个明确出口；保存失败保留表单与对话框，不把失败当成离开许可。
export function confirmUnsaved(save) {
  return new Promise(resolve => {
    const busy = ref(false);
    let settled = false;
    const finish = allowed => { if (!settled) { settled = true; resolve(allowed); dialog.close(); } };
    const dialog = Modal.open({
      title: t("有未保存的修改"), content: t("离开前可以保存修改，也可以放弃或继续编辑。"),
      width: 460, modalStyle: { maxWidth: "calc(100vw - 32px)" },
      maskClosable: false, closable: false, escToClose: false,
      onClose: () => { if (!settled) { settled = true; resolve(false); } },
      footer: () => h("div", { style: "display:flex;flex-wrap:wrap;justify-content:flex-end;gap:8px" }, [
        h(Button, { disabled: busy.value, onClick: () => finish(false) }, () => t("继续编辑")),
        h(Button, { disabled: busy.value, onClick: () => finish(true) }, () => t("放弃修改")),
        h(Button, { type: "primary", loading: busy.value, onClick: async () => {
          if (busy.value) return;
          busy.value = true;
          try { if (await save()) finish(true); } finally { busy.value = false; }
        } }, () => t("保存并继续")),
      ]),
    });
  });
}

export function useSettingsGuard(dirty, save, busy = () => false) {
  let pending;
  const guard = () => {
    if (busy()) return Promise.resolve(false);
    if (!dirty()) return Promise.resolve(true);
    pending ||= confirmUnsaved(save).finally(() => { pending = null; });
    return pending;
  };
  const unregister = inject(settingsGuardKey, null)?.(guard);
  onUnmounted(() => unregister?.());
  return guard;
}
