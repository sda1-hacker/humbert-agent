export function mcpTransportLabel(transport) {
    switch (transport) {
        case "streamable_http":
            return "Streamable HTTP";
        case "stdio":
            return "stdio";
        default:
            return String(transport || "未知传输方式");
    }
}

export function mcpRiskLabel(risk) {
    switch (risk) {
        case "read":
            return "Read";
        case "exec":
            return "Exec";
        case "write":
            return "Write";
        default:
            return String(risk || "未知风险");
    }
}

export function mcpConnectionStateLabel(state) {
    switch (state) {
        case "connecting":
            return "正在连接";
        case "connected":
            return "已连接";
        case "degraded":
            return "连接异常";
        case "error":
            return "连接失败";
        case "disabled":
            return "已停用";
        case "disconnected":
            return "未连接";
        default:
            return "未连接";
    }
}

export function mcpConnectionStateTone(state) {
    switch (state) {
        case "connected":
            return "ok";
        case "connecting":
            return "pending";
        case "degraded":
            return "warning";
        case "error":
            return "error";
        case "disabled":
            return "muted";
        default:
            return "muted";
    }
}
