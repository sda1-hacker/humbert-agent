// 前端预检用于及时反馈；后端仍是文件类型、大小和模型能力的最终校验方。
export const MAX_ATTACHMENTS = 8;
const MAX_BYTES = 12 * 1024 * 1024;
const MAX_TOTAL_BYTES = 24 * 1024 * 1024;
const MAX_TEXT_BYTES = 512 * 1024;
const IMAGE_EXTENSIONS = new Set([".png", ".jpg", ".jpeg", ".gif", ".webp"]);
const DOCUMENT_EXTENSIONS = new Set([".pdf", ".docx", ".xlsx", ".pptx"]);
const TEXT_TYPES = new Set([
  "application/json", "application/ld+json", "application/xml", "application/javascript",
  "application/x-javascript", "application/yaml", "application/x-yaml", "application/toml",
  "application/sql", "application/graphql",
]);
const TEXT_EXTENSIONS = new Set([
  ".txt", ".md", ".markdown", ".json", ".jsonl", ".yaml", ".yml", ".xml", ".csv", ".tsv",
  ".go", ".js", ".jsx", ".ts", ".tsx", ".vue", ".py", ".rb", ".rs", ".java", ".kt",
  ".c", ".h", ".cc", ".cpp", ".cs", ".swift", ".sh", ".zsh", ".fish", ".ps1", ".sql",
  ".html", ".css", ".scss", ".less", ".toml", ".ini", ".conf", ".env", ".graphql",
]);

function extension(file) {
  const name = String(file?.name || "").toLowerCase();
  const dot = name.lastIndexOf(".");
  return dot < 0 ? "" : name.slice(dot);
}
function mime(file) {
  return String(file?.mimeType || file?.type || "").toLowerCase().split(";", 1)[0].trim();
}
function size(file) { return Number(file?.sizeBytes ?? file?.size ?? 0); }

export function isImageAttachment(file) {
  return mime(file).startsWith("image/") || IMAGE_EXTENSIONS.has(extension(file));
}

/** 图片需要 Chat 或视觉辅助模型；文本和 Office 文档由后端提取，不要求原生 Files 能力。 */
export function attachmentCapabilityError(items, chat, imageModel) {
  if (!items?.length || !chat) return "";
  if (!items.some(isImageAttachment) || chat.capabilities?.vision || imageModel?.capabilities?.vision) return "";
  return "当前 Chat 模型无法处理所选附件（需要 Vision），且没有可用的视觉辅助模型。请先在“设置 → 多媒体”中配置。";
}

/**
 * 同时接受浏览器 File 和已读取的附件 DTO。选择时先检查，异步读取结束后可再次用
 * 当前草稿检查总量，避免两批文件并发读取时分别通过限制、合并后却超限。
 */
export function validateComposerAttachments(files, existing = []) {
  if (existing.length + files.length > MAX_ATTACHMENTS) throw new Error(`单条消息最多允许 ${MAX_ATTACHMENTS} 个附件`);
  let total = existing.reduce((sum, file) => sum + size(file), 0);
  for (const file of files) {
    const name = String(file.name || "").trim() || "pasted-image.png";
    const bytes = size(file), type = mime(file);
    if (bytes <= 0) throw new Error(`${name} 是空文件`);
    if (bytes > MAX_BYTES) throw new Error(`${name} 超过 12 MiB 限制`);
    const image = isImageAttachment(file), document = DOCUMENT_EXTENSIONS.has(extension(file));
    const text = type.startsWith("text/") || TEXT_TYPES.has(type) ||
      (!type || type === "application/octet-stream") && TEXT_EXTENSIONS.has(extension(file));
    if (!image && !document && !text) throw new Error(`${name} 暂不支持；当前文件附件仅支持图片、PDF、Office 和 UTF-8 文本`);
    if (!image && !document && bytes > MAX_TEXT_BYTES) throw new Error(`${name} 超过文本附件 512 KiB 限制`);
    total += bytes;
    if (total > MAX_TOTAL_BYTES) throw new Error("单条消息附件总大小不能超过 24 MiB");
  }
}

/**
 * 整批读取成功后才返回，不向 Store 写入半批附件；其中一个文件失败时保持原草稿。
 * 返回原件 Base64，发送请求和预览各自使用该引用，不在这里改变图片或文档格式。
 */
export async function readComposerAttachments(files, existing = []) {
  validateComposerAttachments(files, existing);
  const result = [];
  for (const [index, file] of files.entries()) {
    const dataURL = await new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onerror = () => reject(reader.error || new Error(`读取 ${file.name} 失败`));
      reader.onload = () => resolve(String(reader.result || ""));
      reader.readAsDataURL(file);
    });
    result.push({
      name: String(file.name || "").trim() || `pasted-image-${Date.now()}-${index + 1}.png`,
      mimeType: file.type || "application/octet-stream", sizeBytes: file.size,
      base64Data: dataURL.includes(",") ? dataURL.slice(dataURL.indexOf(",") + 1) : dataURL,
    });
  }
  return result;
}
