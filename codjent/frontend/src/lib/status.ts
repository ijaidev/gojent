import {
  Ban,
  Check,
  Circle,
  CircleAlert,
  Loader2,
  type LucideIcon,
  ShieldAlert,
  SkipForward,
  WifiOff,
} from "lucide-react";
import type { RunStatus } from "@/lib/types";

export type Tone = "neutral" | "accent" | "success" | "warning" | "danger";

export interface StatusMeta {
  label: string;
  shortLabel: string;
  tone: Tone;
  icon: LucideIcon;
  description: string;
  live: boolean;
}

export type ToolStatus =
  | "pending"
  | "approval"
  | "running"
  | "done"
  | "error"
  | "denied"
  | "skipped"
  | "incomplete";

export const runStatus: Record<RunStatus, StatusMeta> = {
  starting: {
    label: "Starting",
    shortLabel: "Starting",
    tone: "accent",
    icon: Loader2,
    description: "Codjent is getting ready.",
    live: true,
  },
  running: {
    label: "Working",
    shortLabel: "Working",
    tone: "accent",
    icon: Loader2,
    description: "Codjent is working.",
    live: true,
  },
  waiting_approval: {
    label: "Needs approval",
    shortLabel: "Approval",
    tone: "warning",
    icon: ShieldAlert,
    description: "A command is waiting for your decision.",
    live: true,
  },
  completed: {
    label: "Done",
    shortLabel: "Done",
    tone: "success",
    icon: Check,
    description: "The run finished.",
    live: false,
  },
  failed: {
    label: "Failed",
    shortLabel: "Failed",
    tone: "danger",
    icon: CircleAlert,
    description: "The run could not finish.",
    live: false,
  },
  canceled: {
    label: "Canceled",
    shortLabel: "Canceled",
    tone: "neutral",
    icon: Ban,
    description: "The run was stopped.",
    live: false,
  },
};

export const toolStatus: Record<ToolStatus, StatusMeta> = {
  pending: {
    label: "Queued",
    shortLabel: "Queued",
    tone: "neutral",
    icon: Circle,
    description: "Waiting to start.",
    live: true,
  },
  approval: {
    label: "Needs approval",
    shortLabel: "Approval",
    tone: "warning",
    icon: ShieldAlert,
    description: "Waiting for your decision.",
    live: true,
  },
  running: {
    label: "Running",
    shortLabel: "Running",
    tone: "accent",
    icon: Loader2,
    description: "Running now.",
    live: true,
  },
  done: {
    label: "Done",
    shortLabel: "Done",
    tone: "neutral",
    icon: Check,
    description: "Finished.",
    live: false,
  },
  error: {
    label: "Failed",
    shortLabel: "Failed",
    tone: "danger",
    icon: CircleAlert,
    description: "The tool failed.",
    live: false,
  },
  denied: {
    label: "Denied",
    shortLabel: "Denied",
    tone: "warning",
    icon: Ban,
    description: "You denied this call.",
    live: false,
  },
  skipped: {
    label: "Skipped",
    shortLabel: "Skipped",
    tone: "neutral",
    icon: SkipForward,
    description: "The run ended before this finished.",
    live: false,
  },
  incomplete: {
    label: "Interrupted",
    shortLabel: "Interrupted",
    tone: "warning",
    icon: CircleAlert,
    description: "Interrupted before it finished.",
    live: false,
  },
};

export const connectionStatus: Record<"online" | "reconnecting", StatusMeta> = {
  online: {
    label: "Connected",
    shortLabel: "Connected",
    tone: "neutral",
    icon: Check,
    description: "Connected to the run.",
    live: false,
  },
  reconnecting: {
    label: "Reconnecting",
    shortLabel: "Offline",
    tone: "warning",
    icon: WifiOff,
    description: "Trying to reconnect to the run.",
    live: true,
  },
};

const toneText: Record<Tone, string> = {
  neutral: "text-muted",
  accent: "text-accent",
  success: "text-success",
  warning: "text-warning",
  danger: "text-danger",
};

const toneDot: Record<Tone, string> = {
  neutral: "bg-line-strong",
  accent: "bg-accent",
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-danger",
};

const toneBadge: Record<Tone, string> = {
  neutral: "border-line bg-surface-muted text-muted",
  accent: "border-accent/20 bg-accent-soft text-accent",
  success: "border-success/20 bg-success-soft text-success",
  warning: "border-warning/25 bg-warning-soft text-warning",
  danger: "border-danger/20 bg-danger-soft text-danger",
};

export function statusTextClass(tone: Tone): string {
  return toneText[tone];
}

export function statusDotClass(tone: Tone): string {
  return toneDot[tone];
}

export function statusBadgeClass(tone: Tone): string {
  return toneBadge[tone];
}

export function runStatusCopy(status: RunStatus): string {
  return runStatus[status].label;
}
