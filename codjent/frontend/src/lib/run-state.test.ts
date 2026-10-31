import { describe, expect, it } from "vitest";
import {
  createLiveRun,
  createPendingRun,
  finishLiveRun,
  liveText,
  liveThinking,
  reduceRunEvent,
} from "./run-state";
import type { RunEvent, RunInfo } from "./types";

const run: RunInfo = {
  id: "run_1",
  thread_id: "th_1",
  status: "running",
  usage: { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 },
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

function event(value: { type: RunEvent["type"]; data: unknown }): RunEvent {
  return { id: 1, run_id: "run_1", ...value } as RunEvent;
}

const approval = {
  id: "apr_1",
  tool_call_id: "call_1",
  tool_name: "execute_command",
  arguments: '{"command":"ls"}',
  created_at: "2026-01-01T00:00:00Z",
};

describe("run-state", () => {
  it("creates a live run from a run snapshot", () => {
    const live = createLiveRun(run);
    expect(live.runID).toBe("run_1");
    expect(live.status).toBe("running");
    expect(liveText(live)).toBe("");
    expect(live.startedAt).toBe(Date.parse(run.created_at));
  });

  it("accumulates text and thinking chunks", () => {
    let live = createLiveRun(run);
    live = reduceRunEvent(
      live,
      event({ type: "text_delta", data: { text: "Hello" } }),
    );
    live = reduceRunEvent(
      live,
      event({ type: "text_delta", data: { text: " world" } }),
    );
    live = reduceRunEvent(
      live,
      event({ type: "thinking_delta", data: { text: "Plan" } }),
    );
    expect(liveText(live)).toBe("Hello world");
    expect(liveThinking(live)).toBe("Plan");
  });

  it("moves a tool through call, approval, denial, and result", () => {
    let live = createLiveRun(run);
    live = reduceRunEvent(
      live,
      event({
        type: "tool_call",
        data: {
          id: "call_1",
          name: "execute_command",
          arguments: '{"command":"ls"}',
        },
      }),
    );
    expect(live.tools[0]?.status).toBe("pending");
    expect(live.tools[0]?.startedAt).toBeTypeOf("number");

    live = reduceRunEvent(
      live,
      event({ type: "approval_required", data: { approval } }),
    );
    expect(live.status).toBe("waiting_approval");
    expect(live.approval?.id).toBe("apr_1");
    expect(live.tools[0]?.status).toBe("approval");

    live = reduceRunEvent(
      live,
      event({
        type: "approval_resolved",
        data: { approval_id: "apr_1", allow: false },
      }),
    );
    expect(live.status).toBe("running");
    expect(live.approval).toBeNull();
    expect(live.tools[0]?.status).toBe("denied");

    live = reduceRunEvent(
      live,
      event({
        type: "tool_result",
        data: { id: "call_1", name: "execute_command", output: "ok" },
      }),
    );
    expect(live.tools[0]?.status).toBe("done");
    expect(live.tools[0]?.output).toBe("ok");
    expect(live.tools[0]?.endedAt).toBeTypeOf("number");
  });

  it("keeps the denied state distinct from failure", () => {
    let live = createPendingRun("run_1", "th_1");
    live = reduceRunEvent(
      live,
      event({
        type: "tool_call",
        data: {
          id: "call_1",
          name: "execute_command",
          arguments: "{}",
        },
      }),
    );
    live = reduceRunEvent(
      live,
      event({ type: "approval_required", data: { approval } }),
    );
    live = reduceRunEvent(
      live,
      event({
        type: "approval_resolved",
        data: { approval_id: "apr_1", allow: false, note: "not now" },
      }),
    );
    expect(live.tools[0]?.status).toBe("denied");
    expect(live.tools[0]?.note).toBe("not now");
  });

  it("marks unfinished tools when a run is canceled", () => {
    let live = createPendingRun("run_1", "th_1");
    live = reduceRunEvent(
      live,
      event({
        type: "tool_call",
        data: { id: "call_1", name: "read_file", arguments: "{}" },
      }),
    );
    const finished = finishLiveRun(live, "canceled");
    expect(finished.status).toBe("canceled");
    expect(finished.tools[0]?.status).toBe("error");
    expect(finished.tools[0]?.skipped).toBe(true);
    expect(finished.tools[0]?.note).toContain("canceled");
  });

  it("does not apply events from another run", () => {
    const live = createLiveRun(run);
    const next = reduceRunEvent(live, {
      id: 1,
      run_id: "run_other",
      type: "text_delta",
      data: { text: "ignored" },
    });
    expect(next).toBe(live);
  });
});
