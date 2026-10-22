import {
  normalizeRuntimeManifest,
  normalizeContextAssembly,
  normalizeContextUsage,
  userInputSignature,
} from "../runtime/projections.js";
import { defineStore } from "pinia";
import {
  applyRuntimeEvent,
  acceptsRuntimeEvent,
  needsDeltaFlush,
  applyToolLifecycleEvent,
} from "../runtime/runEvents.js";
import { streamingFor, ensureThinkingStep } from "../runtime/streaming.js";
import { contextActions } from "../runtime/contextActions.js";
import { historyActions } from "../runtime/historyActions.js";

import { Events } from "@wailsio/runtime";

import { cancelTurn, resolveApproval, startTurn } from "../api/chat.js";

import { useSessionStore } from "./sessions.js";

const runtimeEventName = "humbert:runtime:event";

let unsubscribeRuntimeEvent = null;
/**
 * RuntimeStore 只保存尚未持久化完成的前端实时状态。
 *
 * Session JSONL v3 永远是历史消息的唯一真实数据源：
 *
 *   - assistant.delta / assistant.reasoning.delta 只用于实时 UI；
 *   - Assistant Step 完成后由后端一次性持久化完整 schema.Message；
 *   - Turn 进入终态后重新读取 Session JSONL，再清理 Streaming State。
 */
export const useRuntimeStore = defineStore("runtime", {
  state: () => ({
    // 单个会话的活动请求、模型、审批、工具和流式内容一起创建、一起清理。
    runs: {},

    /**
     * StartTurn binding 尚未返回的 Session。
     */
    startingSessions: {},

    /**
     * sessionID -> 最近进入终态的 requestID。
     *
     * 用于解决 completion event 比 StartTurn Promise 更早到达的竞态。
     */
    terminalRequests: {},

    /**
     * sessionID -> 最近一次 Runtime Failure 的用户可见错误。
     *
     * 错误不是 Session 持久化事实，因此只保存在当前前端进程；下一次发送会清理。
     */
    terminalErrors: {},

    /** sessionID -> 失败请求编号；只用于在页面上定位本地日志。 */
    terminalErrorRequests: {},

    // 保存初始化失败的消息收据；同一内容重试时复用后端消息，避免重复追加。
    failedStarts: {},
    historyErrors: {},
    historyLoading: {},
    recoverableFailures: {},

    /**
     * sessionID -> Context Usage。
     *
     * 这是后端 ContextEngine 的展示快照，不是历史事实；切换模型、完成 Turn 或
     * 手动压缩后都可以直接覆盖。
     */
    contextUsages: {},

    /**
     * sessionID -> 与 Context Usage 同一次 Build 得到的安全 Context Assembly 摘要。
     */
    contextAssemblies: {},

    /**
     * sessionID -> 与 Context Usage 同一次 Resolve 得到的 Runtime Manifest。
     */
    contextManifests: {},

    /**
     * sessionID -> 是否正在读取 Context Usage。
     */
    contextLoading: {},

    /**
     * sessionID -> 是否正在执行手动 Context Compaction。
     */
    contextCompacting: {},

    /**
     * sessionID -> 最近一次 Context 状态读取错误。
     * 自动刷新失败不会改变 Turn 状态，Composer 只把它作为非阻塞提示。
     */
    contextErrors: {},

    /** approvalID -> 是否正在提交用户决策。 */
    approvalResolving: {},

    /** approvalID -> 最近一次提交错误。 */
    approvalErrors: {},
  }),

  getters: {
    isSessionRunning: (state) => (sessionID) => {
      return Boolean(
        (state.runs[sessionID]?.requestID &&
          !state.runs[sessionID]?.finalEvent) ||
          state.startingSessions[sessionID],
      );
    },

    needsHistorySync: (state) => (sessionID) =>
      Boolean(state.runs[sessionID]?.finalEvent),

    streamingContent: (state) => (sessionID) => {
      return state.runs[sessionID]?.text ?? "";
    },

    terminalError: (state) => (sessionID) => {
      return state.terminalErrors[sessionID] ?? "";
    },

    terminalErrorRequestID: (state) => (sessionID) =>
      state.terminalErrorRequests[sessionID] ?? "",

    modelIDForSession: (state) => (sessionID) => {
      return state.runs[sessionID]?.modelID ?? "";
    },

    runState: (state) => (sessionID) =>
      state.runs[sessionID]?.finalEvent
        ? null
        : (state.runs[sessionID]?.status ?? null),

    liveTools: (state) => (sessionID) => state.runs[sessionID]?.tools ?? [],

    liveActivity: (state) => (sessionID) =>
      state.runs[sessionID]?.activities ?? [],

    contextUsage: (state) => (sessionID) => {
      return state.contextUsages[sessionID] ?? null;
    },

    contextAssembly: (state) => (sessionID) => {
      return state.contextAssemblies[sessionID] ?? null;
    },

    contextManifest: (state) => (sessionID) => {
      return state.contextManifests[sessionID] ?? null;
    },

    isContextLoading: (state) => (sessionID) =>
      Boolean(state.contextLoading[sessionID]),

    isContextCompacting: (state) => (sessionID) =>
      Boolean(state.contextCompacting[sessionID]),

    contextError: (state) => (sessionID) =>
      state.contextErrors[sessionID] ?? "",

    pendingApproval: (state) => (sessionID) =>
      state.runs[sessionID]?.approval ?? null,

    isApprovalResolving: (state) => (approvalID) =>
      Boolean(state.approvalResolving[approvalID]),

    approvalError: (state) => (approvalID) =>
      state.approvalErrors[approvalID] ?? "",
  },

  actions: {
    ...contextActions,
    ...historyActions,
    ensureRun(sessionID) {
      return (this.runs[sessionID] ??= {});
    },
    dismissTerminalError(sessionID) {
      delete this.terminalErrors[sessionID];
      delete this.terminalErrorRequests[sessionID];
    },

    /**
     * 全应用只注册一个 Wails Runtime Listener。
     */
    initialiseEvents() {
      if (unsubscribeRuntimeEvent) {
        return;
      }

      unsubscribeRuntimeEvent = Events.On(runtimeEventName, (event) => {
        void this.handleEvent(event?.data);
      });
    },

    /**
     * 释放 Runtime Listener 和所有尚未执行的动画帧刷新任务。
     */
    disposeEvents() {
      if (unsubscribeRuntimeEvent) {
        unsubscribeRuntimeEvent();
        unsubscribeRuntimeEvent = null;
      }

      streamingFor(this).dispose();
    },

    queueStreamingDelta(sessionID, kind, delta) {
      if (!sessionID || !delta) return;
      if (kind === "reasoning") ensureThinkingStep(this.ensureRun(sessionID));
      streamingFor(this).queue(sessionID, kind, delta);
    },
    flushStreamingDelta(sessionID) {
      streamingFor(this).flush(sessionID);
    },
    discardStreamingDelta(sessionID) {
      streamingFor(this).discard(sessionID);
    },

    /**
     * 统一处理实时 Tool Lifecycle。
     *
     * MessageList 不再单独订阅 Runtime Event，因此同一个 Session 的 Turn、Approval、
     * Streaming 与 Tool 状态都由 RuntimeStore 维护一份事实源。
     */
    applyToolLifecycleEvent(event) {
      if (event?.sessionID)
        applyToolLifecycleEvent(this.ensureRun(event.sessionID), event);
    },

    /**
     * 发起新 Turn。
     */
    async send(sessionID, content, attachments = []) {
      if (this.needsHistorySync(sessionID))
        throw new Error("请先重新同步对话记录，再继续发送。");
      if (this.isSessionRunning(sessionID)) {
        throw new Error("当前会话仍在生成回复");
      }

      this.discardStreamingDelta(sessionID);

      this.startingSessions[sessionID] = true;

      this.ensureRun(sessionID).text = "";

      this.ensureRun(sessionID).tools = [];
      this.ensureRun(sessionID).activities = [];
      delete this.ensureRun(sessionID).status;

      delete this.terminalErrors[sessionID];
      delete this.terminalErrorRequests[sessionID];
      delete this.recoverableFailures[sessionID];

      try {
        const retry = this.failedStarts[sessionID];
        const signature = userInputSignature(content, attachments);
        const result = await startTurn(
          sessionID,
          content,
          attachments,
          retry?.signature === signature ? retry.userMessageID : "",
        );

        if (result.startError) {
          this.failedStarts[sessionID] = {
            signature,
            userMessageID: result.userMessageID,
          };
          this.terminalErrors[sessionID] = result.startError;
          this.terminalErrorRequests[sessionID] = result.requestID ?? "";
          throw new Error(result.startError);
        }
        delete this.failedStarts[sessionID];
        // 终态可能先于 Bridge 收据返回，不能再用启动快照覆盖完成后的状态。
        if (this.terminalRequests[sessionID] === result.requestID)
          return result;

        if (this.terminalRequests[sessionID] !== result.requestID) {
          this.ensureRun(sessionID).requestID = result.requestID;
        }

        const initialUsage = normalizeContextUsage(result.contextUsage);
        if (initialUsage) {
          this.contextUsages[sessionID] = initialUsage;
          delete this.contextErrors[sessionID];
        }

        const initialAssembly = normalizeContextAssembly(
          result.contextAssembly,
        );
        if (initialAssembly) {
          this.contextAssemblies[sessionID] = initialAssembly;
        }

        const frozenManifest = normalizeRuntimeManifest(result.runtime);
        if (frozenManifest) {
          this.contextManifests[sessionID] = frozenManifest;
          if (this.terminalRequests[sessionID] !== result.requestID) {
            this.ensureRun(sessionID).status = {
              requestID: result.requestID,
              runID: result.runID ?? "",
              sessionID,
              phase:
                this.ensureRun(sessionID).status?.requestID === result.requestID
                  ? this.ensureRun(sessionID).status.phase
                  : "running",
              startedAt: "",
              waitingApprovalID: "",
              approval: null,
              runtime: frozenManifest,
            };
          }
        }

        const sessionStore = useSessionStore();

        try {
          await sessionStore.refreshMessages(sessionID);
        } catch {
          // 运行已被接受，读取历史失败不能伪装成发送失败并恢复重复草稿。
          // 后续终态事件仍会重新同步历史。
          this.terminalErrors[sessionID] = "消息已发送，暂时无法刷新历史。";
        }

        return result;
      } catch (error) {
        // 发送失败也可能已经写入历史，刷新后让用户看到真实已保存的输入。
        // 刷新失败保留原始发送错误；收据不清除，下一次重试仍可复用。
        if (!this.terminalErrors[sessionID]) {
          this.terminalErrors[sessionID] =
            "消息发送失败。请检查会话或模型设置后重试。";
        }
        try {
          await useSessionStore().refreshMessages(sessionID);
        } catch {
          this.terminalErrors[sessionID] =
            "消息发送或历史刷新失败，请检查会话后重试。";
        }
        this.discardStreamingDelta(sessionID);

        delete this.ensureRun(sessionID).text;
        delete this.ensureRun(sessionID).activities;

        throw error;
      } finally {
        delete this.startingSessions[sessionID];
      }
    },

    /**
     * 提交一次 Human Approval 决策。
     *
     * 只把 approvalID + decision 发送给后端；Tool 原始参数不进入 WebView。后端
     * 成功接受后不会等待整个 Agent Turn 完成，后续状态继续通过 Runtime Event
     * 驱动。重复点击由前端 loading 和后端 Pending→Resolving 两层共同防护。
     */
    async resolveToolApproval(approvalID, decision) {
      if (!approvalID) {
        throw new Error("Approval ID 不能为空");
      }
      if (this.approvalResolving[approvalID]) {
        return null;
      }

      this.approvalResolving[approvalID] = true;
      delete this.approvalErrors[approvalID];

      try {
        return await resolveApproval(approvalID, decision);
      } catch (error) {
        this.approvalErrors[approvalID] = error?.message ?? String(error);
        throw error;
      } finally {
        delete this.approvalResolving[approvalID];
      }
    },

    async cancel(sessionID) {
      const requestID = this.ensureRun(sessionID).requestID;

      if (!requestID) {
        return;
      }

      const previousPhase =
        this.ensureRun(sessionID).status?.phase ?? "running";
      if (this.ensureRun(sessionID).status) {
        this.ensureRun(sessionID).status.phase = "cancelling";
      }

      try {
        await cancelTurn(requestID);
      } catch (error) {
        if (this.ensureRun(sessionID).status?.requestID === requestID) {
          this.ensureRun(sessionID).status.phase = previousPhase;
        }
        throw error;
      }
    },

    /**
     * 处理 Go Runtime Event。
     */
    async handleEvent(data) {
      if (!acceptsRuntimeEvent(this, data)) return;
      if (data.type === "turn.started")
        this.discardStreamingDelta(data.sessionID);
      if (needsDeltaFlush(this.runs[data.sessionID], data))
        this.flushStreamingDelta(data.sessionID);
      const effect = applyRuntimeEvent(this, data);
      if (effect?.delta)
        this.queueStreamingDelta(
          data.sessionID,
          effect.delta,
          data.delta ?? "",
        );
      if (effect?.terminal) await this.finalise(data);
    },

    async continueSession(sessionID) {
      if (!this.recoverableFailures[sessionID]) return;
      // 新增一条明确的继续指令，沿用持久化轨迹；不回滚历史或自动重放有副作用的工具。
      return this.send(
        sessionID,
        "请根据上次已保存的对话和工具结果，从中断处继续完成任务。不要重复已经确认完成的操作；对没有保存结果的操作，先检查实际状态再决定是否执行。",
      );
    },
  },
});
