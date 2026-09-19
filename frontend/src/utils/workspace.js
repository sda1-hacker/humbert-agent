/**
 * 把字节数转换成工作区 UI 使用的紧凑文本。
 *
 * 后端始终返回原始 bytes；展示单位属于前端职责，不能把格式化后的字符串再传回业务 API。
 */
export function formatBytes(value) {
    const bytes = Number(value);
    if (!Number.isFinite(bytes) || bytes < 0) return "—";
    if (bytes < 1024) return `${bytes} B`;
    const units = ["KB", "MB", "GB", "TB"];
    let current = bytes / 1024;
    for (const unit of units) {
        if (current < 1024 || unit === units[units.length - 1]) {
            return `${current >= 100 ? current.toFixed(0) : current.toFixed(1)} ${unit}`;
        }
        current /= 1024;
    }
    return `${bytes} B`;
}

/** UTC/RFC3339 时间只在展示时转换到用户当前系统时区。 */
export function formatWorkspaceTime(value) {
    if (!value) return "—";
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    return new Intl.DateTimeFormat(undefined, {
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
    }).format(date);
}
