"use client";

import { Folder, Plus, Search, SquarePen, X } from "lucide-react";
import { useDeferredValue, useEffect, useMemo, useState } from "react";
import { CodjentMark, CodjentWordmark } from "@/components/brand";
import { Spinner } from "@/components/spinner";
import type { LiveRun } from "@/hooks/use-codjent";
import type { Thread } from "@/lib/types";
import { isActiveStatus } from "@/lib/types";
import { cn, formatElapsed, relativeTime, statusCopy } from "@/lib/utils";

interface SidebarProps {
  threads: Thread[];
  selectedID: string | null;
  liveRuns: Record<string, LiveRun>;
  workspaceName: string;
  loading: boolean;
  open: boolean;
  onClose: () => void;
  onSelect: (id: string) => void;
  onNew: () => void;
}

function dayKey(date: Date): string {
  return `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`;
}

function threadGroup(iso: string): string {
  const date = new Date(iso);
  if (!Number.isFinite(date.getTime())) {
    return "Earlier";
  }
  const now = new Date();
  if (dayKey(date) === dayKey(now)) {
    return "Today";
  }
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  if (dayKey(date) === dayKey(yesterday)) {
    return "Yesterday";
  }
  if (now.getTime() - date.getTime() < 7 * 24 * 60 * 60 * 1000) {
    return "This week";
  }
  return "Earlier";
}

function ThreadStatus({
  thread,
  live,
  now,
}: {
  thread: Thread;
  live?: LiveRun;
  now: number;
}) {
  if (!live) {
    return <span className="truncate">{relativeTime(thread.updated_at)}</span>;
  }

  if (live.reconnecting) {
    return (
      <span className="inline-flex items-center gap-1.5 text-warning">
        <span className="size-1.5 rounded-full bg-warning" />
        Reconnecting
      </span>
    );
  }

  if (live.status === "waiting_approval") {
    return (
      <span className="inline-flex items-center gap-1.5 text-warning">
        <span className="size-1.5 rounded-full bg-warning" />
        Needs approval
      </span>
    );
  }

  if (isActiveStatus(live.status)) {
    return (
      <span className="inline-flex min-w-0 items-center gap-1.5 text-accent">
        <Spinner className="size-3" label="Working" />
        <span className="truncate">
          Working {formatElapsed(now - live.startedAt)}
        </span>
      </span>
    );
  }

  if (live.status === "failed") {
    return <span className="text-danger">{statusCopy(live.status)}</span>;
  }

  if (live.status === "canceled") {
    return <span className="text-muted">{statusCopy(live.status)}</span>;
  }

  return <span className="truncate">{relativeTime(thread.updated_at)}</span>;
}

export function Sidebar({
  threads,
  selectedID,
  liveRuns,
  workspaceName,
  loading,
  open,
  onClose,
  onSelect,
  onNew,
}: SidebarProps) {
  const [query, setQuery] = useState("");
  const deferredQuery = useDeferredValue(query.trim().toLowerCase());
  const hasActiveRun = Object.values(liveRuns).some((live) =>
    isActiveStatus(live.status),
  );
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    if (!hasActiveRun) {
      return;
    }
    const interval = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(interval);
  }, [hasActiveRun]);

  const groups = useMemo(() => {
    const filtered = deferredQuery
      ? threads.filter((thread) =>
          thread.title.toLowerCase().includes(deferredQuery),
        )
      : threads;
    const order = ["Today", "Yesterday", "This week", "Earlier"];
    const buckets = new Map<string, Thread[]>();
    for (const thread of filtered) {
      const key = threadGroup(thread.updated_at);
      const bucket = buckets.get(key) ?? [];
      bucket.push(thread);
      buckets.set(key, bucket);
    }
    return order
      .map((label) => ({ label, threads: buckets.get(label) ?? [] }))
      .filter((group) => group.threads.length > 0);
  }, [deferredQuery, threads]);

  const choose = (id: string) => {
    onSelect(id);
    onClose();
  };

  const create = () => {
    onNew();
    onClose();
  };

  return (
    <>
      <div
        aria-hidden="true"
        className={cn(
          "fixed inset-0 z-30 bg-black/25 backdrop-blur-[1px] transition-opacity lg:hidden",
          open ? "opacity-100" : "pointer-events-none opacity-0",
        )}
        onClick={onClose}
      />

      <aside
        aria-label="Threads"
        className={cn(
          "fixed inset-y-0 left-0 z-40 flex w-[302px] shrink-0 flex-col border-r border-line bg-sidebar transition-transform duration-200 ease-out lg:static lg:translate-x-0",
          open ? "translate-x-0" : "-translate-x-full",
        )}
      >
        <div className="flex h-[60px] shrink-0 items-center justify-between px-4">
          <div className="flex items-center gap-2.5">
            <CodjentMark className="size-7 text-ink" />
            <CodjentWordmark />
          </div>
          <button
            type="button"
            aria-label="Close sidebar"
            className="grid size-8 place-items-center rounded-lg text-muted transition hover:bg-surface-hover hover:text-ink lg:hidden"
            onClick={onClose}
          >
            <X className="size-4" />
          </button>
        </div>

        <div className="px-3 pb-3">
          <div className="flex items-center gap-2 rounded-xl border border-transparent bg-surface-muted px-2.5 py-1.5 transition focus-within:border-line-strong focus-within:bg-surface">
            <Search className="size-3.5 shrink-0 text-faint" />
            <input
              aria-label="Search threads"
              className="min-w-0 flex-1 bg-transparent text-[12.5px] text-ink outline-none placeholder:text-faint"
              placeholder="Search threads"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
            <button
              type="button"
              aria-label="New thread"
              className="grid size-7 shrink-0 place-items-center rounded-lg text-muted transition hover:bg-surface-hover hover:text-ink"
              onClick={create}
            >
              <SquarePen className="size-3.5" />
            </button>
          </div>
        </div>

        <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-3 pb-4">
          <button
            type="button"
            className="mb-3 flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-[12px] font-medium text-muted transition hover:bg-surface-hover hover:text-ink"
            onClick={create}
          >
            <span className="grid size-5 place-items-center text-muted">
              <Plus className="size-3.5" />
            </span>
            New thread
          </button>

          <div className="mb-4 flex items-center gap-2 px-2 text-[12px] text-muted">
            <Folder className="size-3.5 shrink-0" />
            <span className="min-w-0 flex-1 truncate font-medium text-ink/80">
              {workspaceName || "Workspace"}
            </span>
            <span className="rounded-full border border-line px-1.5 py-0.5 text-[9px] font-medium uppercase tracking-[0.12em] text-faint">
              local
            </span>
          </div>

          {loading ? (
            <div className="space-y-1.5 px-1">
              {[0, 1, 2].map((item) => (
                <div
                  className="h-[62px] animate-pulse rounded-xl bg-surface-muted"
                  key={item}
                />
              ))}
            </div>
          ) : groups.length === 0 ? (
            <div className="px-3 py-8 text-center text-[12px] leading-5 text-faint">
              {deferredQuery ? (
                "No threads match that search."
              ) : (
                <>
                  No threads yet.
                  <br />
                  Start one with the pencil above.
                </>
              )}
            </div>
          ) : (
            <div className="space-y-5">
              {groups.map((group) => (
                <section key={group.label}>
                  <h2 className="mb-1.5 px-2 text-[10px] font-medium uppercase tracking-[0.14em] text-faint">
                    {group.label}
                  </h2>
                  <div className="space-y-1">
                    {group.threads.map((thread) => {
                      const live = liveRuns[thread.id];
                      const selected = selectedID === thread.id;
                      return (
                        <button
                          type="button"
                          aria-current={selected ? "page" : undefined}
                          className={cn(
                            "group w-full rounded-xl border px-3 py-2.5 text-left transition",
                            selected
                              ? "border-line bg-surface shadow-[0_1px_2px_rgba(28,25,23,0.05)]"
                              : "border-transparent hover:bg-surface-hover",
                          )}
                          key={thread.id}
                          onClick={() => choose(thread.id)}
                        >
                          <span
                            className={cn(
                              "block truncate text-[12.5px] leading-5",
                              selected
                                ? "font-semibold text-ink"
                                : "font-medium text-ink/85",
                            )}
                          >
                            {thread.title}
                          </span>
                          <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-[10.5px] text-faint">
                            <span className="size-1.5 shrink-0 rounded-full bg-line-strong" />
                            <ThreadStatus
                              thread={thread}
                              live={live}
                              now={now}
                            />
                          </span>
                        </button>
                      );
                    })}
                  </div>
                </section>
              ))}
            </div>
          )}
        </div>

        <div className="shrink-0 border-t border-line px-4 py-3">
          <div className="flex items-center justify-between text-[10.5px] text-faint">
            <span>Built on gojent</span>
            <span className="font-mono">v0.1</span>
          </div>
        </div>
      </aside>
    </>
  );
}
