import { normalizeRuntimeManifest, createLiveToolCall } from "./projections.js";
import {
  formatStructuredText,
  toolActionLabel,
  toolDisplayName,
} from "../utils/toolTrace.js";

export function applyToolLifecycleEvent(run, event) {
  const sessionID = event?.sessionID;
  const callID = event?.toolCallID;
  if (!sessionID || !callID) {
    return;
  }

  const calls = Array.isArray(run.tools) ? run.tools : [];
  let call = calls.find((item) => item.id === callID) ?? null;
  if (!call) {
    call = createLiveToolCall(event);
    calls.push(call);
    run.tools = calls;
  }

  if (event.toolName) {
    call.name = event.toolName;
    call.displayName = toolDisplayName(event.toolName);
  }
  if (typeof event.toolArguments === "string" && event.toolArguments) {
    call.arguments = event.toolArguments;
    call.formattedArguments = formatStructuredText(event.toolArguments);
  }
  call.actionLabel = toolActionLabel(call);

  switch (event.type) {
    case "tool.started":
      call.status = "running";
      call.requestedAt = event.occurredAt || call.requestedAt;
      break;
    case "tool.completed":
      call.status = "completed";
      call.durationMS = Number.isFinite(event.durationMS)
        ? event.durationMS
        : 0;
      call.completedAt = event.occurredAt || "";
      break;
    case "tool.failed":
      call.status = "failed";
      call.durationMS = Number.isFinite(event.durationMS)
        ? event.durationMS
        : 0;
      call.error = event.error || "";
      call.completedAt = event.occurredAt || "";
      break;
    default:
      break;
  }

  const steps = run.activities ?? [];
  if (steps.at(-1)?.type === "thinking") {
    steps.at(-1).status = "completed";
  }
  if (!steps.some((step) => step.type === "tool" && step.call?.id === callID)) {
    steps.push({ type: "tool", key: `tool:live:${callID}`, call });
    run.activities = steps;
  }
}

// 这里只转换显式传入的状态，不订阅事件、不调 IPC，也不安排 UI 帧。
export function acceptsRuntimeEvent(state, event) {
  if (!event || typeof event !== "object" || !event.sessionID) return false;
  if (
    event.requestID &&
    state.terminalRequests[event.sessionID] === event.requestID
  )
    return false;
  const current = state.runs[event.sessionID];
  return !(
    event.type !== "turn.started" &&
    current?.requestID &&
    event.requestID &&
    current.requestID !== event.requestID
  );
}

export function needsDeltaFlush(run, event) {
  return (
    ["tool.started", "tool.completed", "tool.failed"].includes(event.type) ||
    (event.type === "assistant.delta" &&
      run?.activities?.at(-1)?.type === "thinking" &&
      run.activities.at(-1).status === "running")
  );
}

export function applyRuntimeEvent(state, event) {
  const sessionID = event.sessionID;
  const run = (state.runs[sessionID] ??= {});
  run.revision = (run.revision ?? 0) + 1;
  switch (event.type) {
    case "turn.maintaining":
      if (
        run.status?.requestID === event.requestID &&
        run.status.phase !== "cancelling"
      )
        run.status.phase = "maintaining";
      break;
    case "turn.started": {
      delete run.finalEvent;
      delete state.historyErrors[sessionID];
      const manifest = normalizeRuntimeManifest(event.runtime);
      if (manifest) state.contextManifests[sessionID] = manifest;
      Object.assign(run, {
        requestID: event.requestID,
        modelID: event.modelID ?? "",
        tools: [],
        activities: [],
        text: "",
        status: {
          requestID: event.requestID ?? "",
          runID: event.runID ?? "",
          sessionID,
          phase: "running",
          startedAt: event.occurredAt ?? "",
          waitingApprovalID: "",
          approval: null,
          runtime: manifest,
        },
      });
      break;
    }
    case "assistant.reasoning.delta":
    case "assistant.delta": {
      run.requestID = event.requestID;
      run.modelID = event.modelID ?? "";
      if (run.status?.requestID === event.requestID)
        run.status.phase = "running";
      const last = run.activities?.at(-1);
      if (
        event.type === "assistant.delta" &&
        last?.type === "thinking" &&
        last.status === "running"
      )
        last.status = "completed";
      return {
        delta: event.type === "assistant.delta" ? "content" : "reasoning",
      };
    }
    case "tool.started":
    case "tool.completed":
    case "tool.failed":
      run.requestID = event.requestID;
      applyToolLifecycleEvent(run, event);
      if (run.status?.requestID === event.requestID)
        run.status.phase = "running";
      break;
    case "approval.requested": {
      const approval = event.approval;
      if (approval && typeof approval === "object" && approval.id) {
        run.approval = approval;
        delete state.approvalErrors[approval.id];
        if (run.status?.requestID === event.requestID)
          Object.assign(run.status, {
            phase: "waiting_approval",
            waitingApprovalID: approval.id,
            approval,
          });
      }
      break;
    }
    case "approval.resolved":
    case "approval.expired": {
      const approvalID = event.approval?.id;
      if (run.approval && (!approvalID || run.approval.id === approvalID))
        delete run.approval;
      if (approvalID) {
        delete state.approvalResolving[approvalID];
        delete state.approvalErrors[approvalID];
      }
      if (run.status?.requestID === event.requestID)
        Object.assign(run.status, {
          phase: "running",
          waitingApprovalID: "",
          approval: null,
        });
      break;
    }
    case "turn.completed":
    case "turn.failed":
    case "turn.cancelled":
      return { terminal: true };
  }
}
