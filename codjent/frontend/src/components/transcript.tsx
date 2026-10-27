"use client";

import {
  Brain,
  Check,
  ChevronDown,
  CircleAlert,
  FileCode2,
  FilePlus2,
  FolderOpen,
  PencilLine,
  Search,
  ShieldAlert,
  ShieldCheck,
  SkipForward,
  Terminal,
  X,
} from "lucide-react";
import { type ReactNode, useEffect, useMemo, useState } from "react";
import { Markdown } from "@/components/markdown";
import { Spinner } from "@/components/spinner";
import type { LiveRun, LiveTool } from "@/hooks/use-codjent";
import type { Message, Part, PendingApproval, ToolCall } from "@/lib/types";
import { isActiveStatus } from "@/lib/types";
import {
  cn,
  formatElapsed,
  formatToolArguments,
  messageFiles,
  messageText,
  parseCommand,
  partFileURL,
  toolLabel,
  toolResultMap,
} from "@/lib/utils";

interface TranscriptProps {
  messages: Message[];
  live?: LiveRun;
  workspaceName: string;
  onResolveApproval: (approvalID: string, allow: boolean) => Promise<boolean>;
}

interface ToolCardProps {
  call: ToolCall;
  status: LiveTool["status"] | "done";
  output?: string;
  error?: string;
  skipped?: boolean;
  note?: string;
}

function toolIcon(name: string): ReactNode {
  switch (name) {
    case "execute_command":
      return <Terminal className="size-3.5" />;
    case "read_file":
      return <FileCode2 className="size-3.5" />;
    case "write_file":
      return <FilePlus2 className="size-3.5" />;
    case "edit_file":
      return <PencilLine className="size-3.5" />;
    case "search_files":
    case "grep":
      return <Search className="size-3.5" />;
    case "list_files":
    case "list_directory":
      return <FolderOpen className="size-3.5" />;
    default:
      return <Terminal className="size-3.5" />;
  }
}

function ToolStatus({ status }: { status: ToolCardProps["status"] }) {
  if (status === "approval") {
    return (
      <span className="inline-flex items-center gap-1 text-warning">
        <ShieldAlert className="size-3" />
        Awaiting approval
      </span>
    );
  }
  if (status === "pending" || status === "running") {
    return (
      <span className="inline-flex items-center gap-1 text-muted">
        <Spinner className="size-3" />
        {status === "pending" ? "Queued" : "Running"}
      </span>
    );
  }
  if (status === "error") {
    return (
      <span className="inline-flex items-center gap-1 text-danger">
        <X className="size-3" />
        Failed
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 text-success">
      <Check className="size-3" />
      Done
    </span>
  );
}

function ToolCard({
  call,
  status,
  output,
  error,
  skipped,
  note,
}: ToolCardProps) {
  const command =
    call.name === "execute_command" ? parseCommand(call.arguments) : null;
  const detail = command ?? formatToolArguments(call);
  const hasBody = Boolean(output || error || note || skipped);
  const [open, setOpen] = useState(status === "error" || Boolean(skipped));

  useEffect(() => {
    if (status === "error" && !skipped) {
      setOpen(true);
    }
  }, [skipped, status]);

  return (
    <details
      className="group/tool overflow-hidden rounded-xl border border-line bg-surface"
      onToggle={(event) => setOpen(event.currentTarget.open)}
      open={open}
    >
      <summary className="flex cursor-pointer list-none items-center gap-2.5 px-3 py-2.5 [&::-webkit-details-marker]:hidden">
        <span className="grid size-6 shrink-0 place-items-center rounded-md bg-surface-muted text-muted">
          {toolIcon(call.name)}
        </span>
        <span className="min-w-0 flex-1">
          <span className="flex min-w-0 items-baseline gap-2">
            <span className="shrink-0 text-[11.5px] font-semibold text-ink">
              {toolLabel(call.name)}
            </span>
            <span className="min-w-0 truncate font-mono text-[10.5px] text-faint">
              {detail}
            </span>
          </span>
        </span>
        <span className="shrink-0 text-[10px]">
          {skipped ? (
            <span className="inline-flex items-center gap-1 text-muted">
              <SkipForward className="size-3" />
              Skipped
            </span>
          ) : (
            <ToolStatus status={status} />
          )}
        </span>
        <ChevronDown className="size-3 shrink-0 text-faint transition group-open/tool:rotate-180" />
      </summary>

      <div className="border-t border-line bg-code px-3 py-2.5">
        {command ? (
          <pre className="mb-2 overflow-x-auto whitespace-pre-wrap break-words font-mono text-[11px] leading-5 text-code-ink">
            <span className="select-none text-faint">$ </span>
            {command}
          </pre>
        ) : call.name !== "execute_command" ? (
          <pre className="mb-2 overflow-x-auto whitespace-pre-wrap break-words font-mono text-[11px] leading-5 text-code-ink">
            {detail}
          </pre>
        ) : null}

        {note ? (
          <p className="mb-2 text-[10.5px] text-warning">{note}</p>
        ) : null}

        {error ? (
          <pre className="max-h-[280px] overflow-auto whitespace-pre-wrap break-words font-mono text-[11px] leading-5 text-danger">
            {error}
          </pre>
        ) : output ? (
          <>
            <div className="mb-1 text-[9px] font-medium uppercase tracking-[0.14em] text-faint">
              Output
            </div>
            <pre className="max-h-[320px] overflow-auto whitespace-pre-wrap break-words font-mono text-[11px] leading-5 text-code-ink">
              {output}
            </pre>
          </>
        ) : hasBody ? (
          <p className="text-[11px] text-faint">No output.</p>
        ) : (
          <p className="text-[11px] text-faint">No result recorded.</p>
        )}
      </div>
    </details>
  );
}

function Thinking({ text }: { text: string }) {
  return (
    <details className="group/thinking rounded-xl border border-line bg-surface-muted/60">
      <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2 text-[11px] font-medium text-muted [&::-webkit-details-marker]:hidden">
        <Brain className="size-3.5" />
        Thinking
        <ChevronDown className="ml-auto size-3 transition group-open/thinking:rotate-180" />
      </summary>
      <div className="border-t border-line px-4 py-3">
        <pre className="max-h-[280px] overflow-auto whitespace-pre-wrap break-words font-sans text-[11.5px] leading-5 text-muted">
          {text}
        </pre>
      </div>
    </details>
  );
}

function FileGrid({ parts }: { parts: Part[] }) {
  const [preview, setPreview] = useState<string | null>(null);
  const files = parts
    .map((part) => ({ part, url: partFileURL(part) }))
    .filter((file): file is { part: Part; url: string } => file.url !== null);

  useEffect(() => {
    if (!preview) {
      return;
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setPreview(null);
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [preview]);

  if (files.length === 0) {
    return null;
  }

  return (
    <>
      <div
        className={cn(
          "grid gap-2",
          files.length > 1 ? "grid-cols-2" : "grid-cols-1",
        )}
      >
        {files.map((file, index) => (
          <button
            className="overflow-hidden rounded-xl border border-line bg-code"
            key={`${file.part.filename ?? "image"}-${index}`}
            onClick={() => setPreview(file.url)}
            type="button"
          >
            {/* biome-ignore lint/performance/noImgElement: data URLs are local and cannot be optimized by next/image */}
            <img
              alt={file.part.filename ?? `Attachment ${index + 1}`}
              className={cn(
                "w-full object-contain",
                files.length > 1 ? "max-h-[220px]" : "max-h-[360px]",
              )}
              loading="lazy"
              src={file.url}
            />
          </button>
        ))}
      </div>
      {preview ? (
        <button
          aria-label="Close image preview"
          className="fixed inset-0 z-50 grid place-items-center bg-black/70 p-6"
          onClick={() => setPreview(null)}
          type="button"
        >
          {/* biome-ignore lint/performance/noImgElement: data URLs are local and cannot be optimized by next/image */}
          <img
            alt=""
            className="max-h-[90vh] max-w-[90vw] rounded-xl object-contain"
            src={preview}
          />
        </button>
      ) : null}
    </>
  );
}

function ApprovalCard({
  approval,
  workspaceName,
  onResolve,
}: {
  approval: PendingApproval;
  workspaceName: string;
  onResolve: (approvalID: string, allow: boolean) => Promise<boolean>;
}) {
  const [pending, setPending] = useState<"allow" | "deny" | null>(null);
  const command = parseCommand(approval.arguments);

  const decide = async (allow: boolean) => {
    setPending(allow ? "allow" : "deny");
    const resolved = await onResolve(approval.id, allow);
    if (!resolved) {
      setPending(null);
    }
  };

  return (
    <div className="mt-3 overflow-hidden rounded-xl border border-warning/30 bg-warning-soft">
      <div className="flex items-start gap-3 px-4 py-3">
        <span className="mt-0.5 grid size-7 shrink-0 place-items-center rounded-lg bg-warning/10 text-warning">
          <ShieldAlert className="size-4" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="text-[12px] font-semibold text-ink">
            Command approval required
          </div>
          <p className="mt-0.5 text-[11px] leading-5 text-muted">
            Codjent wants to run this command in{" "}
            <span className="font-medium text-ink">{workspaceName}</span>.
          </p>
        </div>
      </div>
      <pre className="mx-4 mb-3 overflow-x-auto whitespace-pre-wrap break-words rounded-lg border border-warning/20 bg-surface/70 px-3 py-2.5 font-mono text-[11.5px] leading-5 text-code-ink">
        <span className="select-none text-faint">$ </span>
        {command}
      </pre>
      <div className="flex items-center justify-end gap-2 border-t border-warning/20 px-4 py-2.5">
        <button
          type="button"
          className="rounded-lg border border-line-strong bg-surface px-3 py-1.5 text-[11px] font-medium text-ink transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
          disabled={pending !== null}
          onClick={() => void decide(false)}
        >
          {pending === "deny" ? "Denying…" : "Deny"}
        </button>
        <button
          type="button"
          className="inline-flex items-center gap-1.5 rounded-lg bg-ink px-3 py-1.5 text-[11px] font-medium text-app transition hover:opacity-85 disabled:cursor-not-allowed disabled:opacity-50"
          disabled={pending !== null}
          onClick={() => void decide(true)}
        >
          {pending === "allow" ? <Spinner className="size-3" /> : null}
          Allow once
        </button>
      </div>
    </div>
  );
}

function LiveTurn({
  live,
  workspaceName,
  onResolveApproval,
}: {
  live: LiveRun;
  workspaceName: string;
  onResolveApproval: TranscriptProps["onResolveApproval"];
}) {
  const active = isActiveStatus(live.status);
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    if (!active) {
      return;
    }
    const interval = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(interval);
  }, [active]);

  const elapsed = formatElapsed(now - live.startedAt);

  return (
    <div className="animate-in">
      <div className="mb-3 flex items-center gap-2 text-[11px]">
        {live.reconnecting ? (
          <span className="inline-flex items-center gap-2 text-warning">
            <CircleAlert className="size-3.5" />
            Reconnecting to the run…
          </span>
        ) : live.status === "waiting_approval" ? (
          <span className="inline-flex items-center gap-2 text-warning">
            <ShieldAlert className="size-3.5" />
            Waiting for your approval · {elapsed}
          </span>
        ) : active ? (
          <span className="inline-flex items-center gap-2 text-accent">
            <Spinner className="size-3.5" label="Working" />
            Working for {elapsed}
          </span>
        ) : live.status === "failed" ? (
          <span className="inline-flex items-center gap-2 text-danger">
            <CircleAlert className="size-3.5" />
            Run failed
          </span>
        ) : live.status === "canceled" ? (
          <span className="text-muted">Run canceled</span>
        ) : (
          <span className="inline-flex items-center gap-2 text-success">
            <ShieldCheck className="size-3.5" />
            Done
          </span>
        )}
      </div>

      {live.thinking ? (
        <div className="mb-3">
          <Thinking text={live.thinking} />
        </div>
      ) : null}

      {live.text ? <Markdown>{live.text}</Markdown> : null}

      {live.tools.length > 0 ? (
        <div className="mt-3 space-y-2">
          {live.tools.map((tool) => (
            <ToolCard
              call={tool.call}
              error={tool.error}
              key={tool.call.id}
              note={tool.note}
              output={tool.output}
              skipped={tool.skipped}
              status={tool.status}
            />
          ))}
        </div>
      ) : null}

      {live.approval ? (
        <ApprovalCard
          approval={live.approval}
          onResolve={onResolveApproval}
          workspaceName={workspaceName}
        />
      ) : null}

      {live.status === "failed" && live.error ? (
        <div className="mt-3 rounded-xl border border-danger/20 bg-danger-soft px-4 py-3">
          <div className="flex items-center gap-2 text-[11.5px] font-semibold text-danger">
            <CircleAlert className="size-3.5" />
            The run could not finish
          </div>
          <pre className="mt-2 max-h-[220px] overflow-auto whitespace-pre-wrap break-words font-mono text-[10.5px] leading-5 text-danger/90">
            {live.error}
          </pre>
        </div>
      ) : null}

      {active && !live.text && live.tools.length === 0 && !live.thinking ? (
        <div className="mt-4 space-y-2" aria-hidden="true">
          <div className="h-2.5 w-[58%] animate-pulse rounded-full bg-surface-muted" />
          <div className="h-2.5 w-[42%] animate-pulse rounded-full bg-surface-muted" />
        </div>
      ) : null}
    </div>
  );
}

function UserMessage({ message }: { message: Message }) {
  const text = messageText(message);
  const files = messageFiles(message);
  if (!text && files.length === 0) {
    return null;
  }
  return (
    <div className="flex justify-end">
      <div className="max-w-[88%] rounded-[20px] bg-surface-muted px-3 py-3 text-[13.5px] leading-6 text-ink">
        <FileGrid parts={files} />
        {text ? (
          <div className="whitespace-pre-wrap px-2 py-1.5">{text}</div>
        ) : null}
      </div>
    </div>
  );
}

export function Transcript({
  messages,
  live,
  workspaceName,
  onResolveApproval,
}: TranscriptProps) {
  const results = useMemo(() => toolResultMap(messages), [messages]);
  const pairedToolIDs = useMemo(() => {
    const ids = new Set<string>();
    for (const message of messages) {
      for (const call of message.tool_calls ?? []) {
        ids.add(call.id);
      }
    }
    return ids;
  }, [messages]);

  const visibleMessages = messages.filter(
    (message) =>
      message.role !== "system" &&
      !(
        message.role === "tool" &&
        message.tool_call_id &&
        pairedToolIDs.has(message.tool_call_id)
      ),
  );

  return (
    <div className="mx-auto w-full max-w-[848px] space-y-8 px-4 pb-10 pt-8 sm:px-6">
      {visibleMessages.length === 0 && !live ? (
        <div className="py-16 text-center">
          <div className="mx-auto grid size-10 place-items-center rounded-xl border border-line bg-surface text-muted">
            <Terminal className="size-4" />
          </div>
          <h2 className="mt-3 text-[14px] font-semibold text-ink">
            This thread is ready
          </h2>
          <p className="mx-auto mt-1 max-w-sm text-[12px] leading-5 text-faint">
            Send a prompt below. Codjent will inspect the workspace, make
            changes, and ask before running commands.
          </p>
        </div>
      ) : null}

      {visibleMessages.map((message) => {
        if (message.role === "user") {
          return <UserMessage key={message.id} message={message} />;
        }
        if (message.role === "tool") {
          const call: ToolCall = {
            id: message.tool_call_id ?? message.id,
            name: message.name ?? "tool",
            arguments: "",
          };
          return (
            <ToolCard
              call={call}
              key={message.id}
              output={messageText(message)}
              status="done"
            />
          );
        }
        if (message.role !== "assistant") {
          return null;
        }

        const text = messageText(message);
        const files = messageFiles(message);
        const hasTools = (message.tool_calls?.length ?? 0) > 0;
        if (!text && !message.thinking && !hasTools && files.length === 0) {
          return null;
        }

        return (
          <div className="space-y-3" key={message.id}>
            {message.thinking ? <Thinking text={message.thinking} /> : null}
            {text ? <Markdown>{text}</Markdown> : null}
            {files.length > 0 ? <FileGrid parts={files} /> : null}
            {hasTools ? (
              <div className="space-y-2">
                {message.tool_calls?.map((call) => {
                  const result = results.get(call.id);
                  return (
                    <ToolCard
                      call={call}
                      key={call.id}
                      output={result ? messageText(result) : undefined}
                      status="done"
                    />
                  );
                })}
              </div>
            ) : null}
          </div>
        );
      })}

      {live ? (
        <LiveTurn
          live={live}
          onResolveApproval={onResolveApproval}
          workspaceName={workspaceName}
        />
      ) : null}
    </div>
  );
}
