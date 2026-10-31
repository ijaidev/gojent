"use client";

import {
  Brain,
  ChevronDown,
  CircleAlert,
  Loader2,
  Maximize2,
  ShieldAlert,
  Terminal,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { ImageLightbox } from "@/components/image-lightbox";
import { Markdown } from "@/components/markdown";
import { Button } from "@/components/ui/button";
import { type LiveRun, liveText, liveThinking } from "@/lib/run-state";
import {
  connectionStatus,
  runStatus,
  type StatusMeta,
  statusTextClass,
  type ToolStatus,
  toolStatus,
} from "@/lib/status";
import {
  formatToolArguments,
  parseCommand,
  presentTool,
} from "@/lib/tool-presentation";
import type { Message, Part, PendingApproval, ToolCall } from "@/lib/types";
import { isActiveStatus } from "@/lib/types";
import {
  cn,
  formatElapsed,
  messageFiles,
  messageText,
  partFileURL,
  toolResultMap,
} from "@/lib/utils";

interface TranscriptProps {
  messages: Message[];
  live?: LiveRun;
  workspaceName: string;
  onResolveApproval: (approvalID: string, allow: boolean) => Promise<boolean>;
}

interface ToolRowProps {
  call: ToolCall;
  status: ToolStatus;
  output?: string;
  error?: string;
  skipped?: boolean;
  note?: string;
  startedAt?: number;
  endedAt?: number;
}

function durationLabel(tool: ToolRowProps): string | null {
  if (!tool.startedAt || !tool.endedAt) {
    return null;
  }
  const ms = Math.max(0, tool.endedAt - tool.startedAt);
  if (ms < 100) {
    return "<0.1s";
  }
  if (ms < 1000) {
    return `${(ms / 1000).toFixed(1)}s`;
  }
  return formatElapsed(ms);
}

function persistedToolStatus(status?: string): ToolStatus {
  switch (status) {
    case "incomplete":
      return "incomplete";
    case "error":
      return "error";
    case "denied":
      return "denied";
    case "skipped":
      return "skipped";
    default:
      return "done";
  }
}

function ToolState({ status }: { status: StatusMeta }) {
  const Icon = status.icon;
  const spinning = Icon === Loader2;
  const hideLabel = status.label === "Done";

  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 text-meta",
        statusTextClass(status.tone),
      )}
      title={status.label}
    >
      <Icon
        aria-hidden="true"
        className={cn("size-3.5 shrink-0", spinning && "animate-spin")}
      />
      <span className={hideLabel ? "sr-only" : ""}>{status.label}</span>
    </span>
  );
}

function ToolRow({
  call,
  status,
  output,
  error,
  skipped,
  note,
  startedAt,
  endedAt,
}: ToolRowProps) {
  const presentation = presentTool(call);
  const meta = toolStatus[status];
  const Icon = presentation.icon;
  const [open, setOpen] = useState(
    status === "error" || status === "denied" || Boolean(skipped),
  );

  useEffect(() => {
    if (status === "error" || status === "denied") {
      setOpen(true);
    }
  }, [status]);

  const duration = durationLabel({ call, status, startedAt, endedAt });
  const bodyText = presentation.command ?? formatToolArguments(call);
  const hasBody = Boolean(bodyText || output || error || note || skipped);

  return (
    <details
      className="group/tool"
      onToggle={(event) => setOpen(event.currentTarget.open)}
      open={open}
    >
      <summary className="flex min-h-10 cursor-pointer list-none items-center gap-2.5 px-1 py-2 [&::-webkit-details-marker]:hidden">
        <Icon aria-hidden="true" className="size-4 shrink-0 text-muted" />
        <span className="flex min-w-0 flex-1 items-baseline gap-2">
          <span className="shrink-0 text-body-sm font-medium text-ink">
            {presentation.label}
          </span>
          <span className="min-w-0 truncate font-mono text-meta text-faint">
            {presentation.target || "—"}
          </span>
        </span>
        <span className="flex shrink-0 items-center gap-2">
          {duration ? (
            <span className="text-meta tabular-nums text-faint">
              {duration}
            </span>
          ) : null}
          <ToolState status={meta} />
        </span>
        <ChevronDown className="size-3.5 shrink-0 text-faint transition group-open/tool:rotate-180" />
      </summary>

      <div className="border-t border-line px-1 py-2.5">
        {bodyText ? (
          <pre className="mb-2 overflow-x-auto whitespace-pre-wrap break-words rounded-field bg-code px-2.5 py-2 font-mono text-meta leading-5 text-code-ink">
            {presentation.command ? (
              <span className="select-none text-faint">$ </span>
            ) : null}
            {bodyText}
          </pre>
        ) : null}

        {note ? <p className="mb-2 text-meta text-warning">{note}</p> : null}

        {error ? (
          <pre className="max-h-[280px] overflow-auto whitespace-pre-wrap break-words font-mono text-meta leading-5 text-danger">
            {error}
          </pre>
        ) : output ? (
          <>
            <div className="mb-1 text-meta text-faint">Output</div>
            <pre className="max-h-[320px] overflow-auto whitespace-pre-wrap break-words font-mono text-meta leading-5 text-code-ink">
              {output}
            </pre>
          </>
        ) : hasBody ? (
          <p className="text-meta text-faint">No output.</p>
        ) : (
          <p className="text-meta text-faint">No result recorded.</p>
        )}
      </div>
    </details>
  );
}

function ToolLedger({
  tools,
  elapsed,
  defaultOpen = false,
}: {
  tools: ToolRowProps[];
  elapsed?: string;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const running = tools.some(
    (tool) =>
      tool.status === "running" ||
      tool.status === "approval" ||
      tool.status === "pending",
  );

  useEffect(() => {
    if (running) {
      setOpen(true);
    }
  }, [running]);

  if (tools.length === 1) {
    return (
      <div className="my-2 border-y border-line">
        <ToolRow {...tools[0]} />
      </div>
    );
  }

  return (
    <details
      className="group/ledger my-2 border-y border-line"
      onToggle={(event) => setOpen(event.currentTarget.open)}
      open={open}
    >
      <summary className="flex cursor-pointer list-none items-center gap-2 py-2 text-meta text-muted [&::-webkit-details-marker]:hidden">
        <span className="font-medium text-ink">
          {elapsed ? `Worked ${elapsed}` : `${tools.length} steps`}
        </span>
        {elapsed ? <span>· {tools.length} steps</span> : null}
        <ChevronDown className="ml-auto size-3.5 transition group-open/ledger:rotate-180" />
      </summary>
      <div className="divide-y divide-line">
        {tools.map((tool) => (
          <ToolRow key={tool.call.id} {...tool} />
        ))}
      </div>
    </details>
  );
}

function Thinking({ text }: { text: string }) {
  return (
    <details className="group/thinking my-2">
      <summary className="flex cursor-pointer list-none items-center gap-2 py-1 text-meta text-muted [&::-webkit-details-marker]:hidden">
        <Brain className="size-3.5" />
        Thinking
        <ChevronDown className="ml-auto size-3 transition group-open/thinking:rotate-180" />
      </summary>
      <pre className="mt-1 max-h-[280px] overflow-auto whitespace-pre-wrap break-words font-sans text-body-sm leading-5 text-muted">
        {text}
      </pre>
    </details>
  );
}

function FileGrid({ parts }: { parts: Part[] }) {
  const [previewIndex, setPreviewIndex] = useState<number | null>(null);
  const files = parts
    .map((part) => ({ part, url: partFileURL(part) }))
    .filter((file): file is { part: Part; url: string } => file.url !== null);
  const images = files.map((file) => ({
    url: file.url,
    filename: file.part.filename,
  }));
  const multiple = files.length > 1;

  if (files.length === 0) {
    return null;
  }

  return (
    <>
      <div
        className={cn(
          "grid w-full gap-2",
          multiple ? "max-w-[420px] grid-cols-2" : "max-w-[380px] grid-cols-1",
        )}
      >
        {files.map((file, index) => (
          <button
            aria-label={`Open ${file.part.filename ?? `image ${index + 1}`}`}
            className={cn(
              "group/image relative overflow-hidden rounded-panel border border-line/80 bg-code focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 focus-visible:ring-offset-2 focus-visible:ring-offset-surface",
              multiple && files.length % 2 === 1 && index === files.length - 1
                ? "col-span-2 aspect-[2/1]"
                : "aspect-[4/3]",
            )}
            key={`${file.part.filename ?? "image"}-${index}`}
            onClick={() => setPreviewIndex(index)}
            type="button"
          >
            {/* biome-ignore lint/performance/noImgElement: data URLs are local and cannot be optimized by next/image */}
            <img
              alt={file.part.filename ?? `Attachment ${index + 1}`}
              className="h-full w-full object-cover object-top transition duration-fast group-hover/image:scale-[1.015] motion-reduce:transition-none"
              height={480}
              loading="lazy"
              src={file.url}
              width={640}
            />
            <span className="pointer-events-none absolute inset-0 bg-black/0 transition group-hover/image:bg-black/10 motion-reduce:transition-none" />
            {multiple ? (
              <span className="absolute left-2 top-2 grid size-5 place-items-center rounded-full bg-black/60 text-meta font-semibold text-white backdrop-blur">
                {index + 1}
              </span>
            ) : null}
            <span className="absolute bottom-2 right-2 grid size-7 place-items-center rounded-field border border-white/15 bg-black/55 text-white/90 backdrop-blur transition group-hover/image:bg-black/70 motion-reduce:transition-none">
              <Maximize2 className="size-3.5" />
            </span>
          </button>
        ))}
      </div>
      <ImageLightbox
        images={images}
        index={previewIndex}
        onIndexChange={setPreviewIndex}
      />
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
  const ref = useRef<HTMLDivElement>(null);

  // biome-ignore lint/correctness/useExhaustiveDependencies: focus the block when a new approval arrives
  useEffect(() => {
    ref.current?.focus();
  }, [approval.id]);

  const decide = async (allow: boolean) => {
    setPending(allow ? "allow" : "deny");
    const resolved = await onResolve(approval.id, allow);
    if (!resolved) {
      setPending(null);
    }
  };

  return (
    <div
      className="mt-3 scroll-mt-20 rounded-panel border border-warning/25 border-l-[3px] border-l-warning bg-warning-soft px-3.5 py-3 outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
      data-approval
      ref={ref}
      tabIndex={-1}
    >
      <div className="flex items-start gap-2.5">
        <ShieldAlert className="mt-0.5 size-4 shrink-0 text-warning" />
        <div className="min-w-0 flex-1">
          <div className="text-body font-semibold text-ink">
            Run this command?
          </div>
          <p className="mt-0.5 text-body-sm leading-5 text-muted">
            Codjent wants to run this command in{" "}
            <span className="font-medium text-ink">{workspaceName}</span>.
          </p>
        </div>
      </div>
      <pre className="mt-2.5 overflow-x-auto whitespace-pre-wrap break-words rounded-field border border-warning/20 bg-surface/80 px-3 py-2 font-mono text-meta leading-5 text-code-ink">
        <span className="select-none text-faint">$ </span>
        {command}
      </pre>
      <div className="mt-3 flex items-center justify-end gap-2">
        <Button
          className="h-11 px-3 sm:h-8"
          disabled={pending !== null}
          loading={pending === "deny"}
          onClick={() => void decide(false)}
          size="sm"
          variant="secondary"
        >
          Deny
        </Button>
        <Button
          className="h-11 px-3 sm:h-8"
          disabled={pending !== null}
          loading={pending === "allow"}
          onClick={() => void decide(true)}
          size="sm"
          variant="primary"
        >
          Allow once
        </Button>
      </div>
    </div>
  );
}

function ErrorBlock({ error }: { error: string }) {
  return (
    <div className="mt-3 rounded-panel border border-danger/20 border-l-[3px] border-l-danger bg-danger-soft px-3.5 py-3">
      <div className="flex items-center gap-2 text-body-sm font-semibold text-danger">
        <CircleAlert className="size-4" />
        The run could not finish
      </div>
      <pre className="mt-2 max-h-[220px] overflow-auto whitespace-pre-wrap break-words font-mono text-meta leading-5 text-danger">
        {error}
      </pre>
      <p className="mt-2 text-meta text-muted">Send a new message to retry.</p>
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
  const text = liveText(live);
  const thinking = liveThinking(live);
  const status = live.reconnecting
    ? connectionStatus.reconnecting
    : runStatus[live.status];
  const stepCount = live.tools.length;
  const approvalToolID = live.approval?.tool_call_id;
  const tools: ToolRowProps[] = live.tools
    .filter((tool) => tool.call.id !== approvalToolID)
    .map((tool) => ({
      call: tool.call,
      status:
        tool.skipped && (tool.status === "done" || tool.status === "error")
          ? "skipped"
          : tool.status,
      output: tool.output,
      error: tool.error,
      skipped: tool.skipped,
      note: tool.note,
      startedAt: tool.startedAt,
      endedAt: tool.endedAt,
    }));

  return (
    <div>
      <div className="mb-2 flex flex-wrap items-center gap-2 text-meta">
        <span className="font-medium text-ink">codjent</span>
        <span className="text-faint">·</span>
        <span className={statusTextClass(status.tone)}>
          {status.label}
          {active ? ` · ${elapsed}` : ""}
        </span>
        {stepCount > 0 ? (
          <>
            <span className="text-faint">·</span>
            <span className="text-faint">
              {stepCount} {stepCount === 1 ? "step" : "steps"}
            </span>
          </>
        ) : null}
      </div>

      {thinking ? <Thinking text={thinking} /> : null}

      {tools.length > 0 ? (
        <ToolLedger
          defaultOpen={active}
          elapsed={active ? elapsed : undefined}
          tools={tools}
        />
      ) : null}

      {text ? <Markdown>{text}</Markdown> : null}

      {live.approval ? (
        <ApprovalCard
          approval={live.approval}
          onResolve={onResolveApproval}
          workspaceName={workspaceName}
        />
      ) : null}

      {live.status === "failed" && live.error ? (
        <ErrorBlock error={live.error} />
      ) : null}

      {live.status === "canceled" ? (
        <p className="mt-3 text-body-sm text-muted">
          Run canceled. Send a new message to continue.
        </p>
      ) : null}

      {active && !text && tools.length === 0 && !thinking && !live.approval ? (
        <div aria-hidden="true" className="mt-3 space-y-2">
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
      <div className="max-w-[80%] rounded-panel border border-line bg-surface-muted px-3.5 py-2.5 text-body leading-6 text-ink">
        <FileGrid parts={files} />
        {text ? (
          <div className="whitespace-pre-wrap px-0.5 py-0.5">{text}</div>
        ) : null}
      </div>
    </div>
  );
}

function EmptyThread() {
  return (
    <div className="py-16 text-center">
      <Terminal className="mx-auto size-5 text-faint" />
      <h2 className="mt-3 text-title font-semibold text-ink">
        Start with a task
      </h2>
      <p className="mx-auto mt-1 max-w-sm text-body-sm leading-5 text-muted">
        Describe what to change, or paste a screenshot. Codjent will inspect the
        workspace and ask before running commands.
      </p>
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
    <div
      aria-busy={live ? isActiveStatus(live.status) : undefined}
      className="mx-auto w-full max-w-[760px] space-y-6 px-4 pb-10 pt-6 sm:px-6"
    >
      {visibleMessages.length === 0 && !live ? <EmptyThread /> : null}

      {visibleMessages.map((message, index) => {
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
            <div className="my-2 border-y border-line" key={message.id}>
              <ToolRow
                call={call}
                output={messageText(message)}
                status={persistedToolStatus(message.status)}
              />
            </div>
          );
        }

        if (message.role !== "assistant") {
          return null;
        }

        const text = messageText(message);
        const files = messageFiles(message);
        const calls = message.tool_calls ?? [];
        const tools: ToolRowProps[] = calls.map((call) => {
          const result = results.get(call.id);
          return {
            call,
            status: persistedToolStatus(result?.status),
            output: result ? messageText(result) : undefined,
          };
        });
        if (
          !text &&
          !message.thinking &&
          tools.length === 0 &&
          files.length === 0
        ) {
          return null;
        }

        const showIdentity =
          index === 0 || visibleMessages[index - 1]?.role !== "assistant";

        return (
          <div className="space-y-2" key={message.id}>
            {showIdentity ? (
              <div className="flex flex-wrap items-center gap-2 text-meta">
                <span className="font-medium text-ink">codjent</span>
              </div>
            ) : null}

            {message.status === "incomplete" ? (
              <div className="inline-flex items-center gap-1.5 text-meta text-warning">
                <CircleAlert className="size-3.5" />
                Interrupted before completion
              </div>
            ) : message.status === "failed" ? (
              <div className="inline-flex items-center gap-1.5 text-meta text-danger">
                <CircleAlert className="size-3.5" />
                The run failed before this response finished
              </div>
            ) : null}

            {message.thinking ? <Thinking text={message.thinking} /> : null}

            {tools.length > 0 ? <ToolLedger tools={tools} /> : null}

            {text ? <Markdown>{text}</Markdown> : null}

            {files.length > 0 ? <FileGrid parts={files} /> : null}
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
