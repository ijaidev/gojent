import type { ToolStatus } from "@/lib/status";
import type {
  PendingApproval,
  RunEvent,
  RunInfo,
  RunStatus,
  ToolCall,
} from "@/lib/types";

export interface LiveTool {
  call: ToolCall;
  status: ToolStatus;
  output?: string;
  error?: string;
  skipped?: boolean;
  note?: string;
  startedAt?: number;
  endedAt?: number;
}

export interface LiveRun {
  runID: string;
  threadID: string;
  status: RunStatus;
  textChunks: string[];
  thinkingChunks: string[];
  tools: LiveTool[];
  approval: PendingApproval | null;
  error?: string;
  reconnecting: boolean;
  startedAt: number;
}

export function liveText(live: LiveRun): string {
  return live.textChunks.join("");
}

export function liveThinking(live: LiveRun): string {
  return live.thinkingChunks.join("");
}

export function createPendingRun(runID: string, threadID: string): LiveRun {
  return {
    runID,
    threadID,
    status: "running",
    textChunks: [],
    thinkingChunks: [],
    tools: [],
    approval: null,
    reconnecting: false,
    startedAt: Date.now(),
  };
}

export function createLiveRun(run: RunInfo): LiveRun {
  const startedAt = Date.parse(run.created_at);
  return {
    runID: run.id,
    threadID: run.thread_id,
    status: run.status,
    textChunks: [],
    thinkingChunks: [],
    tools: [],
    approval: run.pending_approval ?? null,
    error: run.error,
    reconnecting: false,
    startedAt: Number.isFinite(startedAt) ? startedAt : Date.now(),
  };
}

function updateTool(
  tools: LiveTool[],
  id: string,
  update: (tool: LiveTool) => LiveTool,
): LiveTool[] {
  return tools.map((tool) => (tool.call.id === id ? update(tool) : tool));
}

export function reduceRunEvent(current: LiveRun, event: RunEvent): LiveRun {
  if (event.run_id !== current.runID) {
    return current;
  }

  switch (event.type) {
    case "run_started":
      return { ...current, status: "running", reconnecting: false };

    case "text_delta":
      return {
        ...current,
        textChunks: [...current.textChunks, event.data.text],
      };

    case "thinking_delta":
      return {
        ...current,
        thinkingChunks: [...current.thinkingChunks, event.data.text],
      };

    case "tool_call": {
      const call = event.data;
      const exists = current.tools.some((tool) => tool.call.id === call.id);
      return {
        ...current,
        tools: exists
          ? updateTool(current.tools, call.id, (tool) => ({ ...tool, call }))
          : [
              ...current.tools,
              {
                call,
                status: "pending",
                startedAt: Date.now(),
              },
            ],
      };
    }

    case "approval_required": {
      const approval = event.data.approval;
      return {
        ...current,
        status: "waiting_approval",
        approval,
        tools: updateTool(current.tools, approval.tool_call_id, (tool) => ({
          ...tool,
          status: "approval",
        })),
      };
    }

    case "approval_resolved": {
      const resolved = event.data;
      const toolID =
        current.approval?.id === resolved.approval_id
          ? current.approval.tool_call_id
          : null;
      return {
        ...current,
        status:
          current.status === "waiting_approval" ? "running" : current.status,
        approval:
          current.approval?.id === resolved.approval_id
            ? null
            : current.approval,
        tools: toolID
          ? updateTool(current.tools, toolID, (tool) => ({
              ...tool,
              status: resolved.allow ? "running" : "denied",
              note: resolved.note,
            }))
          : current.tools,
      };
    }

    case "tool_result": {
      const result = event.data;
      const endedAt = Date.now();
      const existing = current.tools.find((tool) => tool.call.id === result.id);
      if (existing) {
        return {
          ...current,
          tools: updateTool(current.tools, result.id, (tool) => ({
            ...tool,
            status: result.error ? "error" : "done",
            output: result.output,
            error: result.error,
            skipped: result.skipped,
            endedAt,
          })),
        };
      }
      return {
        ...current,
        tools: [
          ...current.tools,
          {
            call: {
              id: result.id,
              name: result.name,
              arguments: "",
            },
            status: result.error ? "error" : "done",
            output: result.output,
            error: result.error,
            skipped: result.skipped,
            endedAt,
          },
        ],
      };
    }

    default:
      return current;
  }
}

export function finishLiveRun(
  current: LiveRun,
  status: RunStatus,
  error?: string,
): LiveRun {
  const interruption =
    status === "canceled"
      ? "The run was canceled before this tool finished."
      : status === "failed"
        ? "The run failed before this tool finished."
        : "The run ended before this tool finished.";

  return {
    ...current,
    status,
    error: error ?? current.error,
    approval: null,
    reconnecting: false,
    tools: current.tools.map((tool) =>
      tool.status === "done" ||
      tool.status === "error" ||
      tool.status === "denied" ||
      tool.status === "skipped"
        ? tool
        : {
            ...tool,
            status: "error",
            skipped: true,
            note: interruption,
          },
    ),
  };
}
