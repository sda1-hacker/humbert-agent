/**
 * 从已完成的工具调用投影用户可见的副作用。
 *
 * 只消费成功的结构化 ToolResult；展示文件路径时再次检查是否属于可浏览的
 * Workspace 相对路径。这里不写入第二份状态，也不赋予 UI 额外文件权限。
 */
import { isObject, readStringProperty, parseToolArguments } from "./toolProtocol.js";

/**
 * 把 Tool Result 正文尽量解析成普通 JSON Object。
 *
 * 文件变化展示只消费 Humbert 内置文件工具的结构化结果；解析失败时不会猜测工具已经
 * 对哪个文件产生了成功副作用，而是退回到 Tool Arguments 中可以确定的路径信息。
 */
function parseToolResultObject(call) {
    const raw =
        typeof call?.result === "string"
            ? call.result.trim()
            : "";

    if (!raw) {
        return {};
    }

    try {
        const parsed = JSON.parse(raw);
        return isObject(parsed) ? parsed : {};
    } catch {
        return {};
    }
}

/**
 * 判断一个 Tool Path 是否能直接交给 WorkspaceService 作为相对路径打开。
 *
 * Additional Write Path 等 Workspace 外绝对路径仍然会出现在本轮文件变化里，
 * 但不会提供“点击预览”，避免前端通过绝对路径扩大 Workspace 的安全读取边界。
 */
function normalizeBrowsableWorkspacePath(value) {
    if (typeof value !== "string") {
        return { path: "", browsable: false };
    }

    const original = value.trim();
    if (!original) {
        return { path: "", browsable: false };
    }

    const normalized = original.replaceAll("\\", "/");
    const absolute =
        normalized.startsWith("/") ||
        normalized.startsWith("~/") ||
        /^[a-zA-Z]:\//.test(normalized) ||
        normalized.startsWith("//");

    const parts = normalized
        .split("/")
        .filter((part) => part && part !== ".");
    const escapesRoot = parts.some((part) => part === "..");

    return {
        path: normalized.replace(/^\.\//, ""),
        browsable: !absolute && !escapesRoot,
    };
}

/**
 * 合并同一个 Turn 内对同一路径的连续操作。
 *
 * UI 关注的是“这一轮最终涉及了哪些文件”，而不是把底层每一次工具调用重复平铺：
 *
 *   新建 hello.go -> 再编辑 hello.go
 *
 * 最终仍显示“生成 hello.go”；如果随后又删除，则显示“删除 hello.go”。
 */
function mergeTurnFileChange(target, change) {
    if (!change?.path) {
        return;
    }

    const key = change.path;
    const previous = target.get(key);

    if (
        previous?.operation === "created" &&
        change.operation === "modified"
    ) {
        return;
    }

    target.set(key, change);
}

/**
 * 从一次 Assistant Turn 的成功文件工具调用中提取“本轮文件变化”。
 *
 * 这份数据不持久化第二份数据库：历史页面直接从该 Turn 已经持久化的 ToolCall / ToolResult
 * 生成，实时页面也可以使用相同 Call 结构。这样文件操作结果和聊天天然拥有同一生命周期，
 * 而“工作区”页面只负责查看当前文件系统。
 *
 * 返回项：
 *   operation: created / modified / copied / moved / deleted / written
 *   path:      当前/目标路径
 *   fromPath:  move_file 的原路径，其它操作为空
 *   browsable: 是否可以安全地按当前 Workspace 相对路径打开
 */
export function fileChangesOfCalls(calls) {
    const values = Array.isArray(calls) ? calls : [];
    const changes = new Map();

    for (const call of values) {
        // 失败、仍在运行、等待审批的 Tool 都不能展示成已经发生的文件变化。
        if (call?.status !== "completed") {
            continue;
        }

        const name = call?.name || "";
        const input = parseToolArguments(call?.arguments || "");
        const output = parseToolResultObject(call);

        const append = (rawPath, operation, extra = {}) => {
            const normalized = normalizeBrowsableWorkspacePath(rawPath);
            if (!normalized.path) {
                return;
            }
            mergeTurnFileChange(changes, {
                operation,
                path: normalized.path,
                browsable: normalized.browsable && operation !== "deleted",
                ...extra,
            });
        };

        switch (name) {
            case "write_file": {
                const rawPath =
                    readStringProperty(output, "path") ||
                    readStringProperty(input, "path");
                let operation = "written";
                if (output.created === true) {
                    operation = "created";
                } else if (output.overwritten === true) {
                    operation = "modified";
                }
                append(rawPath, operation);
                break;
            }

            case "edit_file": {
                append(
                    readStringProperty(output, "path") ||
                    readStringProperty(input, "path"),
                    "modified",
                );
                break;
            }

            case "apply_patch": {
                const patchChanges = Array.isArray(input.changes)
                    ? input.changes
                    : [];
                const resultFiles = Array.isArray(output.files)
                    ? output.files
                    : [];

                for (let index = 0; index < patchChanges.length; index += 1) {
                    const patch = patchChanges[index];
                    if (!isObject(patch)) continue;

                    // apply_patch 的 result.files 使用 Sandbox 归一化后的 display path，
                    // 优先采用它可以让“绝对 Workspace 输入”仍然得到安全可点击的相对路径。
                    const resultPath =
                        typeof resultFiles[index] === "string"
                            ? resultFiles[index]
                            : "";
                    append(
                        resultPath || readStringProperty(patch, "path"),
                        patch.create === true ? "created" : "modified",
                    );
                }
                break;
            }

            case "copy_file": {
                append(
                    readStringProperty(output, "destination") ||
                    readStringProperty(input, "destination"),
                    "copied",
                );
                break;
            }

            case "move_file": {
                const destination =
                    readStringProperty(output, "destination") ||
                    readStringProperty(input, "destination");
                const source =
                    readStringProperty(output, "source") ||
                    readStringProperty(input, "source");
                const normalizedSource = normalizeBrowsableWorkspacePath(source);
                append(destination, "moved", {
                    fromPath: normalizedSource.path,
                });
                break;
            }

            case "delete_file": {
                append(
                    readStringProperty(output, "path") ||
                    readStringProperty(input, "path"),
                    "deleted",
                );
                break;
            }

            case "browser": {
                if (input.action === "screenshot") {
                    // 后端只在截图成功保存后返回路径；不能根据调用参数猜测产物。
                    append(readStringProperty(output, "screenshot_path"), "created");
                }
                break;
            }

            default:
                break;
        }
    }

    return [...changes.values()];
}

// 只根据成功的 schedule_task ToolResult 展示可点击的任务入口。
// 模型在普通回答中写出的 ID 不会被当成已创建任务。
export function scheduledTasksOfCalls(calls) {
    const result = [];
    const seen = new Set();
    for (const call of Array.isArray(calls) ? calls : []) {
        if (call?.name !== "schedule_task" || call?.status !== "completed") continue;
        const output = parseToolResultObject(call);
        const id = readStringProperty(output, "task_id");
        if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id) || seen.has(id)) continue;
        seen.add(id);
        result.push({
            id,
            name: readStringProperty(output, "name") || "已安排的任务",
            nextRunAt: readStringProperty(output, "next_run_at"),
        });
    }
    return result;
}
