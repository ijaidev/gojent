export type AccessMode = "ask" | "full";

export type Role = "system" | "user" | "assistant" | "tool";

export type RunStatus =
  | "starting"
  | "running"
  | "waiting_approval"
  | "completed"
  | "failed"
  | "canceled";

export interface Part {
  type: string;
  text?: string;
  mime_type?: string;
  data?: string;
  url?: string;
}

export interface ToolCall {
  id: string;
  name: string;
  arguments: string;
}

export interface Message {
  id: string;
  thread_id: string;
  role: Role;
  content: Part[];
  thinking?: string;
  name?: string;
  tool_calls?: ToolCall[];
  tool_call_id?: string;
  created_at: string;
}

export interface Thread {
  id: string;
  title: string;
  provider: string;
  model: string;
  access_mode: AccessMode;
  created_at: string;
  updated_at: string;
}

export interface ThreadDetail {
  thread: Thread;
  messages: Message[];
  active_run?: RunInfo;
}

export interface CreateThreadInput {
  title?: string;
  provider?: string;
  model?: string;
  access_mode?: AccessMode;
}

export interface UpdateThreadInput {
  title?: string;
  provider?: string;
  model?: string;
  access_mode?: AccessMode;
}

export interface ModelInfo {
  id: string;
  name: string;
}

export interface ProviderInfo {
  id: string;
  name: string;
  default_model: string;
  models: ModelInfo[];
}

export type ProviderFormat =
  | "openai-responses"
  | "openai-completion"
  | "anthropic"
  | "google";

export interface ProviderConfig {
  id: string;
  name: string;
  format: ProviderFormat;
  base_url: string;
  default_model: string;
  models: ModelInfo[];
  api_key_set: boolean;
  created_at: string;
  updated_at: string;
}

export interface ProviderConfigInput {
  name: string;
  format: ProviderFormat;
  base_url: string;
  default_model: string;
  models: ModelInfo[];
  api_key?: string;
  clear_api_key?: boolean;
}

export interface Usage {
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  reasoning_tokens?: number;
}

export interface PendingApproval {
  id: string;
  tool_call_id: string;
  tool_name: string;
  arguments: string;
  created_at: string;
}

export interface RunInfo {
  id: string;
  thread_id: string;
  status: RunStatus;
  error?: string;
  pending_approval?: PendingApproval;
  usage: Usage;
  created_at: string;
  updated_at: string;
}

export interface HealthInfo {
  status: string;
  workspace: string;
}

export interface RunStartedEvent {
  thread_id: string;
  provider: string;
  model: string;
  access_mode: AccessMode;
}

export interface TextDeltaEvent {
  text: string;
}

export interface ToolCallEvent {
  id: string;
  name: string;
  arguments: string;
}

export interface ToolResultEvent {
  id: string;
  name: string;
  output?: string;
  error?: string;
  skipped?: boolean;
}

export interface ApprovalRequiredEvent {
  approval: PendingApproval;
}

export interface ApprovalResolvedEvent {
  approval_id: string;
  allow: boolean;
  note?: string;
}

export interface RunCompletedEvent {
  usage: Usage;
  finish_reason: string;
  message_ids?: string[];
}

export interface RunFailedEvent {
  error: string;
}

export interface RunCanceledEvent {
  reason?: string;
}

interface EventBase {
  id: number;
  run_id: string;
}

export type RunEvent =
  | (EventBase & { type: "run_started"; data: RunStartedEvent })
  | (EventBase & { type: "text_delta"; data: TextDeltaEvent })
  | (EventBase & { type: "thinking_delta"; data: TextDeltaEvent })
  | (EventBase & { type: "tool_call"; data: ToolCallEvent })
  | (EventBase & { type: "tool_result"; data: ToolResultEvent })
  | (EventBase & { type: "approval_required"; data: ApprovalRequiredEvent })
  | (EventBase & { type: "approval_resolved"; data: ApprovalResolvedEvent })
  | (EventBase & { type: "run_completed"; data: RunCompletedEvent })
  | (EventBase & { type: "run_failed"; data: RunFailedEvent })
  | (EventBase & { type: "run_canceled"; data: RunCanceledEvent });

export function isActiveStatus(status: RunStatus): boolean {
  return (
    status === "starting" ||
    status === "running" ||
    status === "waiting_approval"
  );
}
