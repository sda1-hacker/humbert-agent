import { getContextOverview, compactContext } from "../api/chat.js";
import {
  normalizeContextUsage,
  normalizeContextAssembly,
  normalizeRuntimeManifest,
  normalizeActiveRunStatus,
} from "./projections.js";

let contextRequestSerial = 0;
// Overview 请求拥有自己的序号；运行身份和 revision 共同阻止迟到快照覆盖事件。
export const contextActions = {
  /**
   * 从后端刷新指定 Session 的 Context Usage。
   *
   * silent=true 用于 Turn 完成、Session 切换等自动刷新：失败时只保留错误状态，
   * 不影响消息终态清理。用户显式操作则使用默认 false，让调用组件展示错误。
   */
  async refreshContextUsage(sessionID, options = {}) {
    if (!sessionID) {
      return null;
    }

    const requestToken = ++contextRequestSerial;
    const observedRun = this.runs[sessionID];
    const observedRevision = observedRun?.revision ?? 0;
    const observedRequestID = observedRun?.requestID;
    this.contextLoading[sessionID] = requestToken;

    try {
      const raw = await getContextOverview(sessionID);
      // IPC 返回期间可能已有新事件或新查询；旧快照不能覆盖更新的运行态。
      const current = this.runs[sessionID];
      if (
        this.contextLoading[sessionID] !== requestToken ||
        current !== observedRun ||
        (current?.revision ?? 0) !== observedRevision ||
        current?.requestID !== observedRequestID
      )
        return null;
      const usage = normalizeContextUsage(raw?.usage);
      const assembly = normalizeContextAssembly(raw?.assembly);
      const manifest = normalizeRuntimeManifest(raw?.runtime);
      const active = normalizeActiveRunStatus(raw?.active);

      if (!usage || !assembly || !manifest) {
        throw new Error("后端返回了无效的 Runtime Context Overview");
      }

      this.contextUsages[sessionID] = usage;
      this.contextAssemblies[sessionID] = assembly;
      this.contextManifests[sessionID] = manifest;

      // Overview.Active 是后端活动 Run 的恢复入口。它让 UI 在重新挂载、
      // 切换 Session 后不必完全依赖“恰好没有错过”的 turn.started event。
      if (
        active &&
        current?.finalEvent &&
        active.requestID !== current.requestID
      ) {
        // 真实的新任务复用 started 投影；旧任务的迟到快照不能复活已经结束的执行。
        void this.handleEvent({
          ...active,
          type: "turn.started",
          sessionID,
          modelID: active.runtime?.modelID,
        });
      }
      if (active && !current?.finalEvent) {
        this.ensureRun(sessionID).status = active;
        this.ensureRun(sessionID).requestID = active.requestID;
        this.ensureRun(sessionID).modelID = active.runtime?.modelID ?? "";
        if (active.approval?.id) {
          this.ensureRun(sessionID).approval = active.approval;
        } else {
          delete this.ensureRun(sessionID).approval;
        }
      } else if (!this.startingSessions[sessionID] && !current?.finalEvent) {
        // 如果 UI 错过了终态 Event（例如 WebView 重新挂载），Overview 是
        // 后端活动 Run 的恢复真相；没有 active 就清理前端陈旧运行态。
        delete this.ensureRun(sessionID).status;
        delete this.ensureRun(sessionID).requestID;
        delete this.ensureRun(sessionID).modelID;
        delete this.ensureRun(sessionID).tools;
        delete this.ensureRun(sessionID).activities;
        const pending = this.ensureRun(sessionID).approval;
        if (pending?.id) {
          delete this.approvalResolving[pending.id];
          delete this.approvalErrors[pending.id];
        }
        delete this.ensureRun(sessionID).approval;
      }

      delete this.contextErrors[sessionID];

      return usage;
    } catch (error) {
      if (this.contextLoading[sessionID] !== requestToken) return null;
      this.contextErrors[sessionID] = error?.message ?? String(error);

      if (!options.silent) {
        throw error;
      }

      return null;
    } finally {
      if (this.contextLoading[sessionID] === requestToken)
        delete this.contextLoading[sessionID];
    }
  },

  /**
   * 执行用户主动 Context Compaction。
   *
   * Runtime 后端会再次检查 Session Busy，因此这里的前端检查只是即时 UX，不能
   * 被当成权限或并发保护。成功后使用响应携带的最新 Usage，避免额外 IPC；如果
   * 响应没有合法 Usage，则退回一次只读刷新。
   */
  async compactSessionContext(sessionID) {
    if (!sessionID) {
      throw new Error("请先选择一个会话");
    }
    if (this.isSessionRunning(sessionID)) {
      throw new Error("当前会话仍在生成回复，暂时不能压缩 Context");
    }

    this.contextCompacting[sessionID] = true;

    try {
      const result = await compactContext(sessionID);
      const usage = normalizeContextUsage(result?.contextUsage);

      if (usage) {
        this.contextUsages[sessionID] = usage;
        delete this.contextErrors[sessionID];
      } else {
        await this.refreshContextUsage(sessionID);
      }

      return result;
    } finally {
      delete this.contextCompacting[sessionID];
    }
  },
};
