const streams = new WeakMap();

const scheduleFrame = (callback) =>
  typeof globalThis.requestAnimationFrame === "function"
    ? globalThis.requestAnimationFrame(callback)
    : globalThis.setTimeout(callback, 16);
const cancelFrame = (handle) =>
  typeof globalThis.cancelAnimationFrame === "function"
    ? globalThis.cancelAnimationFrame(handle)
    : globalThis.clearTimeout(handle);

// 缓冲只拥有未提交的 delta 和调度句柄。每个 Store 独立，终态先 flush 再同步历史。
export function createDeltaBuffer(
  commit,
  schedule = scheduleFrame,
  cancel = cancelFrame,
) {
  const pending = new Map();
  const frames = new Map();
  function discard(sessionID) {
    if (frames.has(sessionID)) cancel(frames.get(sessionID));
    frames.delete(sessionID);
    pending.delete(sessionID);
  }
  function flush(sessionID) {
    const delta = pending.get(sessionID);
    discard(sessionID);
    if (delta) commit(sessionID, delta);
  }
  return {
    queue(sessionID, kind, delta) {
      if (!sessionID || !delta) return;
      const buffered = pending.get(sessionID) ?? { content: "", reasoning: "" };
      buffered[kind] += delta;
      pending.set(sessionID, buffered);
      if (!frames.has(sessionID))
        frames.set(
          sessionID,
          schedule(() => flush(sessionID)),
        );
    },
    flush,
    discard,
    dispose() {
      for (const sessionID of pending.keys()) discard(sessionID);
    },
  };
}

export function ensureThinkingStep(run) {
  const steps = (run.activities ??= []);
  if (steps.at(-1)?.type !== "thinking") {
    steps.push({
      type: "thinking",
      key: `thinking:live:${steps.length}`,
      content: "",
      note: "",
      status: "running",
    });
  }
}

export function streamingFor(store) {
  if (!streams.has(store)) {
    streams.set(
      store,
      createDeltaBuffer((sessionID, delta) => {
        const run = store.ensureRun(sessionID);
        if (delta.content) run.text = (run.text ?? "") + delta.content;
        const step = run.activities?.at(-1);
        if (delta.reasoning && step?.type === "thinking")
          step.content += delta.reasoning;
      }),
    );
  }
  return streams.get(store);
}
