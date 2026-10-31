import {
  FileCode2,
  FilePlus2,
  FolderOpen,
  type LucideIcon,
  PencilLine,
  Search,
  Terminal,
} from "lucide-react";
import type { ToolCall } from "@/lib/types";

export type ToolKind =
  | "command"
  | "read"
  | "write"
  | "edit"
  | "search"
  | "list"
  | "other";

export interface ToolPresentation {
  label: string;
  kind: ToolKind;
  icon: LucideIcon;
  target: string;
  command?: string;
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
    if (typeof parsed.path === "string") {
      return parsed.path;
    }
    return JSON.stringify(parsed, null, 2);
  } catch {
    return tool.arguments;
  }
}

function labelFromName(name: string): string {
  return name
    .replace(/[_-]+/g, " ")
    .replace(/\b\w/g, (letter) => letter.toUpperCase());
}

export function presentTool(call: ToolCall): ToolPresentation {
  switch (call.name) {
    case "execute_command": {
      const command = parseCommand(call.arguments);
      return {
        label: "Command",
        kind: "command",
        icon: Terminal,
        target: command,
        command,
      };
    }
    case "read_file":
      return {
        label: "Read file",
        kind: "read",
        icon: FileCode2,
        target: formatToolArguments(call),
      };
    case "write_file":
      return {
        label: "Write file",
        kind: "write",
        icon: FilePlus2,
        target: formatToolArguments(call),
      };
    case "edit_file":
      return {
        label: "Edit file",
        kind: "edit",
        icon: PencilLine,
        target: formatToolArguments(call),
      };
    case "search_files":
    case "grep":
      return {
        label: "Search",
        kind: "search",
        icon: Search,
        target: formatToolArguments(call),
      };
    case "list_files":
    case "list_directory":
      return {
        label: "List files",
        kind: "list",
        icon: FolderOpen,
        target: formatToolArguments(call),
      };
    default:
      return {
        label: labelFromName(call.name),
        kind: "other",
        icon: Terminal,
        target: formatToolArguments(call),
      };
  }
}
