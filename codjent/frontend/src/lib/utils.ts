import type { Message, RunStatus, Thread, ToolCall } from "@/lib/types";

export function cn(...classes: Array<string | false | null | undefined>) {
  return classes.filter(Boolean).join(" ");
}

export function basename(path: string): string {
  const normalized = path.replace(/[\\/]+$/, "");
  const index = Math.max(
    normalized.lastIndexOf("/"),
    normalized.lastIndexOf("\\"),
  );
  return index >= 0 ? normalized.slice(index + 1) : normalized;
}

export function relativeTime(iso: string): string {
  const then = new Date(iso).getTime();
  if (!Number.isFinite(then)) {
    return "";
  }
  const seconds = Math.max(0, Math.round((Date.now() - then) / 1000));
  if (seconds < 45) {
    return "Just now";
  }
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) {
    return `${minutes}m ago`;
  }
  const hours = Math.round(minutes / 60);
  if (hours < 24) {
    return `${hours}h ago`;
  }
  const days = Math.round(hours / 24);
  if (days === 1) {
    return "Yesterday";
  }
  if (days < 7) {
    return `${days}d ago`;
  }
  return new Date(iso).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  });
}

export function formatElapsed(ms: number): string {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  if (seconds < 60) {
    return `${seconds}s`;
  }
  const minutes = Math.floor(seconds / 60);
  const remainder = seconds % 60;
  if (minutes < 60) {
    return remainder > 0 ? `${minutes}m ${remainder}s` : `${minutes}m`;
  }
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${minutes % 60}m`;
}

export function truncate(text: string, max = 60): string {
  const singleLine = text.replace(/\s+/g, " ").trim();
  if (singleLine.length <= max) {
    return singleLine;
  }
  return `${singleLine.slice(0, max).trimEnd()}…`;
}

export function messageText(message: Message): string {
  return message.content
    .filter((part) => part.type === "text")
    .map((part) => part.text ?? "")
    .join("");
}

export function toolResultMap(messages: Message[]): Map<string, Message> {
  const results = new Map<string, Message>();
  for (const message of messages) {
    if (message.role === "tool" && message.tool_call_id) {
      results.set(message.tool_call_id, message);
    }
  }
  return results;
}

export function sortThreads(threads: Thread[]): Thread[] {
  return [...threads].sort(
    (a, b) =>
      new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime(),
  );
}

export function upsertThread(threads: Thread[], thread: Thread): Thread[] {
  return sortThreads([
    thread,
    ...threads.filter((candidate) => candidate.id !== thread.id),
  ]);
}

export function parseCommand(argumentsText: string): string {
  try {
    const parsed = JSON.parse(argumentsText) as Record<string, unknown>;
    if (typeof parsed.command === "string") {
      return parsed.command;
    }
    if (typeof parsed.cmd === "string") {
      return parsed.cmd;
    }
  } catch {
    // Fall through to the raw arguments.
  }
  return argumentsText;
}

export function formatToolArguments(tool: ToolCall): string {
  try {
    const parsed = JSON.parse(tool.arguments) as Record<string, unknown>;
    if (tool.name === "execute_command") {
      return typeof parsed.command === "string"
        ? parsed.command
        : JSON.stringify(parsed, null, 2);
    }
    if (typeof parsed.path === "string") {
      return parsed.path;
    }
    return JSON.stringify(parsed, null, 2);
  } catch {
    return tool.arguments;
  }
}

export function toolLabel(name: string): string {
  switch (name) {
    case "execute_command":
      return "Command";
    case "read_file":
      return "Read file";
    case "write_file":
      return "Write file";
    case "edit_file":
      return "Edit file";
    case "list_files":
    case "list_directory":
      return "List files";
    case "search_files":
    case "grep":
      return "Search";
    default:
      return name
        .replace(/[_-]+/g, " ")
        .replace(/\b\w/g, (letter) => letter.toUpperCase());
  }
}

export function statusCopy(status: RunStatus): string {
  switch (status) {
    case "starting":
      return "Starting";
    case "running":
      return "Working";
    case "waiting_approval":
      return "Needs approval";
    case "completed":
      return "Done";
    case "failed":
      return "Failed";
    case "canceled":
      return "Canceled";
  }
}

export function errorMessage(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }
  return String(error);
}
