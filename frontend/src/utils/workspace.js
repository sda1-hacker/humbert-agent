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

/** 将后端稳定操作码映射成用户能直接理解的中文标签。 */
export function artifactOperationLabel(value) {
    switch (value) {
        case "created": return "新建";
        case "modified": return "修改";
        case "copied": return "复制";
        case "moved": return "移动";
        case "deleted": return "删除";
        default: return value || "文件操作";
    }
}

/** 产物操作按“生成/变化/删除”分组，供前端过滤而不修改后端审计语义。 */
export function artifactOperationGroup(value) {
    if (["created", "copied", "moved"].includes(value)) return "created";
    if (value === "deleted") return "deleted";
    return "modified";
}
