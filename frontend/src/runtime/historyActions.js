import { useSessionStore } from "../stores/sessions.js";

// 终态只触发历史重读；失败时保留流式投影，不能重放模型或工具。
export const historyActions = {
  /**
   * 处理所有 Runtime 终态。
   */
  async finalise(data) {
    const sessionID = data.sessionID;
    this.flushStreamingDelta(sessionID);
    this.terminalRequests[sessionID] = data.requestID;
    const run = this.ensureRun(sessionID);
    run.finalEvent = data;
    if (data.discardPartial) {
      run.text = "";
      run.activities = [];
    }
    const approval = run.approval;
    if (approval) {
      delete this.approvalResolving[approval.id];
      delete this.approvalErrors[approval.id];
      delete run.approval;
    }
    if (data.type !== "turn.completed") {
      this.terminalErrors[sessionID] =
        data.error || "这次回复已停止，可以从已保存的进度继续。";
      this.terminalErrorRequests[sessionID] = data.requestID || "";
      this.recoverableFailures[sessionID] = !data.discardPartial;
    } else {
      delete this.terminalErrors[sessionID];
      delete this.terminalErrorRequests[sessionID];
      delete this.recoverableFailures[sessionID];
    }
    await this.syncSessionMessages(sessionID);
  },

  // 只重读本地历史，不会重新调用模型。读失败时保留可见流式内容，供用户恢复。
  async syncSessionMessages(sessionID) {
    if (this.historyLoading[sessionID]) return false;
    const run = this.runs[sessionID];
    const requestID = run?.requestID;
    this.historyLoading[sessionID] = true;
    try {
      const sessions = useSessionStore();
      const refreshed = await sessions.refreshMessages(sessionID);
      // 切走的会话不会执行历史读取；未读取或被替代都不能作为清理流式缓冲的依据。
      if (refreshed === false)
        throw new Error("消息尚未读取或已被更新的请求替代");
      if (
        this.runs[sessionID] === run &&
        run?.requestID === requestID &&
        run?.finalEvent
      ) {
        this.discardStreamingDelta(sessionID);
        delete this.runs[sessionID];
      }
      delete this.historyErrors[sessionID];
      void this.refreshContextUsage(sessionID, { silent: true });
      return true;
    } catch {
      if (this.runs[sessionID] === run && run?.requestID === requestID) {
        this.historyErrors[sessionID] =
          "生成已结束，但历史暂时无法读取。当前内容会保留，请重新同步后继续。";
      }
      return false;
    } finally {
      delete this.historyLoading[sessionID];
      if (
        this.runs[sessionID]?.finalEvent &&
        this.runs[sessionID].requestID !== requestID
      )
        void this.syncSessionMessages(sessionID);
    }
  },
};
