import { Message as ArcoMessage } from "@arco-design/web-vue";
import { t } from "../i18n/index.js";

// 只处理应用主动显示的通知。后端诊断或动态正文没有译文时保留原文。
function show(method, content, ...args) {
  const value = typeof content === "string" ? t(content) :
    content && typeof content.content === "string" ? { ...content, content: t(content.content) } : content;
  return ArcoMessage[method](value, ...args);
}

export const Message = {
  success: (content, ...args) => show("success", content, ...args),
  warning: (content, ...args) => show("warning", content, ...args),
  error: (content, ...args) => show("error", content, ...args),
  info: (content, ...args) => show("info", content, ...args),
};
