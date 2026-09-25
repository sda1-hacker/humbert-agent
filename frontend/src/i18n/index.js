import { ref } from "vue";
import translations from "./translations.js";

export const supportedLanguages = Object.freeze([
  { value: "zh-CN", label: "简体中文" },
  { value: "en-US", label: "English" },
  { value: "ja-JP", label: "日本語" },
  { value: "ko-KR", label: "한국어" },
]);

const languageCodes = new Set(supportedLanguages.map((item) => item.value));
const cacheKey = "humbert.ui-language";

function cachedLanguage() {
  try {
    const value = localStorage.getItem(cacheKey);
    return languageCodes.has(value) ? value : "zh-CN";
  } catch {
    return "zh-CN";
  }
}

export const language = ref(cachedLanguage());

export function setLanguage(value) {
  if (!languageCodes.has(value)) throw new Error("Unsupported UI language");
  language.value = value;
  if (typeof document !== "undefined") document.documentElement.lang = value;
  try { localStorage.setItem(cacheKey, value); } catch { /* Backend remains authoritative. */ }
}

/** 静态界面文案使用原中文作为稳定键；模型和用户内容从不进入此函数。 */
export function t(key, values = {}) {
  const catalog = translations[language.value];
  const localized = catalog && Object.hasOwn(catalog, key) ? catalog[key] : key;
  return localized.replace(/\{([a-zA-Z][a-zA-Z0-9]*)\}/g, (match, name) =>
    Object.hasOwn(values, name) ? String(values[name]) : match);
}

export function formatDate(value, options) {
  const date = value instanceof Date ? value : new Date(value);
  return Number.isNaN(date.getTime()) ? String(value ?? "") :
    new Intl.DateTimeFormat(language.value, options).format(date);
}

setLanguage(language.value);
