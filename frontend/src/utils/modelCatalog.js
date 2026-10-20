import catalog from "../data/model-catalog.json" with { type: "json" };

export const providerPresets = catalog.providers;
export const catalogVerifiedAt = catalog.verifiedAt;
export const capabilityFields = [
  { key: "tools", label: "工具调用" }, { key: "vision", label: "图片理解" },
  { key: "reasoning", label: "推理" }, { key: "json", label: "JSON 输出" },
  { key: "files", label: "原生文件" }, { key: "audio", label: "音频" },
];

function endpoint(value) {
  try { return new URL(value).href.replace(/\/+$/, ""); }
  catch { return ""; }
}

// 只按协议和实际地址匹配，不用用户自定义名称猜测供应商，更不自动迁移已有模型。
export function presetForProvider(provider) {
  if (!provider) return null;
  if (provider.type === "ollama") return providerPresets.find((p) => p.type === "ollama");
  return providerPresets.find((p) => p.type === provider.type && [p.baseURL, ...(p.baseURLAliases || [])].some((url) => endpoint(url) === endpoint(provider.baseURL))) || null;
}

export function presetForModel(provider, modelName) {
  return presetForProvider(provider)?.models.find((model) => model.id === modelName) || null;
}

// 预设只填充新建表单。编辑时完整保留用户的输出预算、超时和三态能力覆盖。
export function modelDraft(providerID, model = null, preset = null) {
  const capabilityConfig = Object.fromEntries(capabilityFields.map(({ key }) => [key,
    model ? model.capabilityConfig?.[key] || "auto" : (preset && typeof preset.capabilities[key] === "boolean" ? (preset.capabilities[key] ? "enabled" : "disabled") : "auto"),
  ]));
  return {
    id: model?.id || "", providerID: model?.providerID || providerID,
    modelName: model?.modelName || preset?.id || "", displayName: model?.displayName || preset?.displayName || "",
    contextWindow: model?.contextWindow ?? preset?.defaultContextWindow ?? preset?.contextWindow ?? catalog.defaults.contextWindow,
    maxOutputTokens: model?.maxOutputTokens ?? preset?.defaultOutputTokens ?? catalog.defaults.maxOutputTokens,
    timeoutMS: model?.timeoutMS ?? catalog.defaults.timeoutMS, capabilityConfig, enabled: model?.enabled ?? true,
  };
}

export function formatTokens(value) {
  const number = Number(value);
  if (!number) return "—";
  const base = number % 1024 === 0 ? 1024 : 1000;
  const unit = number >= base ** 2 ? base ** 2 : base;
  return `${Number((number / unit).toFixed(2))}${unit === base ? "K" : "M"}`;
}
