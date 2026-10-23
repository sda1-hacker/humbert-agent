import catalog from "../../../internal/models/catalog.json" with { type: "json" };

export const providerPresets = catalog.providers;
export const catalogVerifiedAt = catalog.verifiedAt;
export const capabilityFields = [
  { key: "tools", label: "工具调用" }, { key: "vision", label: "图片理解" },
  { key: "reasoning", label: "推理" }, { key: "json", label: "JSON 输出" },
  { key: "files", label: "原生文件" }, { key: "audio", label: "音频" },
];

export const reasoningReplayOptions = [
  { value: "auto", label: "跟随预设" }, { value: "include", label: "当前窗口全部" },
  { value: "current_turn", label: "仅当前用户轮次" }, { value: "omit", label: "不回传推理" },
];

// 与后端按协议和完整模型 ID 查表的规则一致；自建网关的改名模型可手动指定。
export function reasoningReplayFor(providerType, modelName, mode = "auto") {
  mode = String(mode || "auto").trim().toLowerCase();
  if (!reasoningReplayOptions.some(option => option.value === mode)) return "omit";
  if (mode !== "auto") return mode;
  for (const provider of providerPresets) {
    if (provider.type !== providerType) continue;
    const model = provider.models.find(model => model.id === modelName);
    if (model || provider.reasoningReplayAliases?.includes(modelName)) {
      return model?.reasoningReplay || provider.reasoningReplay || catalog.defaults.reasoningReplay[providerType] || "omit";
    }
  }
  return catalog.defaults.reasoningReplay[providerType] || "omit";
}

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
    timeoutMS: model?.timeoutMS ?? catalog.defaults.timeoutMS, capabilityConfig,
    reasoningReplay: model?.reasoningReplay || "auto", enabled: model?.enabled ?? true,
  };
}

export function formatTokens(value) {
  const number = Number(value);
  if (!number) return "—";
  const base = number % 1024 === 0 ? 1024 : 1000;
  const unit = number >= base ** 2 ? base ** 2 : base;
  return `${Number((number / unit).toFixed(2))}${unit === base ? "K" : "M"}`;
}
