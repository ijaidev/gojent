"use client";

import { Folder, Plus, Search, Settings2, X } from "lucide-react";
import Link from "next/link";
import { useDeferredValue, useEffect, useMemo, useRef, useState } from "react";
import { CodjentMark, CodjentWordmark } from "@/components/brand";
import { Spinner } from "@/components/spinner";
import { IconButton } from "@/components/ui/icon-button";
import { StatusDot } from "@/components/ui/status-badge";
import type { LiveRun } from "@/hooks/use-codjent";
import type { ProviderInfo, Thread } from "@/lib/types";
import { isActiveStatus } from "@/lib/types";
import {
  accessLabel,
  cn,
  formatElapsed,
  modelName,
  relativeTime,
} from "@/lib/utils";

interface SidebarProps {
  threads: Thread[];
  selectedID: string | null;
  liveRuns: Record<string, LiveRun>;
  providers: ProviderInfo[];
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
    return "Last 7 days";
  }
  return "Earlier";
}

function ThreadLive({ live, now }: { live: LiveRun; now: number }) {
  if (live.reconnecting) {
    return (
      <span className="inline-flex items-center gap-1.5 text-warning">
        <StatusDot tone="warning" />
        Reconnecting
      </span>
    );
  }

  if (live.status === "waiting_approval") {
    return (
      <span className="inline-flex items-center gap-1.5 text-warning">
        <StatusDot tone="warning" />
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
    return <span className="text-danger">Failed</span>;
  }

  if (live.status === "canceled") {
    return <span className="text-muted">Canceled</span>;
  }

  return null;
}

export function Sidebar({
  threads,
  selectedID,
  liveRuns,
  providers,
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
  const closeRef = useRef<HTMLButtonElement>(null);
  const wasOpen = useRef(false);
  const onCloseRef = useRef(onClose);

  useEffect(() => {
    onCloseRef.current = onClose;
  }, [onClose]);

  useEffect(() => {
    if (!hasActiveRun) {
      return;
    }
    const interval = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(interval);
  }, [hasActiveRun]);

  useEffect(() => {
    if (open) {
      wasOpen.current = true;
      closeRef.current?.focus();
      const onKeyDown = (event: KeyboardEvent) => {
        if (event.key === "Escape") {
          onCloseRef.current();
        }
      };
      window.addEventListener("keydown", onKeyDown);
      return () => window.removeEventListener("keydown", onKeyDown);
    }
    if (wasOpen.current) {
      wasOpen.current = false;
      document
        .querySelector<HTMLButtonElement>('[aria-label="Open sidebar"]')
        ?.focus();
    }
  }, [open]);

  const groups = useMemo(() => {
    const filtered = deferredQuery
      ? threads.filter((thread) =>
          thread.title.toLowerCase().includes(deferredQuery),
        )
      : threads;
    const order = ["Today", "Yesterday", "Last 7 days", "Earlier"];
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
          "fixed inset-0 z-30 bg-black/30 transition-opacity duration-fast lg:hidden",
          open ? "opacity-100" : "pointer-events-none opacity-0",
        )}
        onClick={onClose}
      />

      <aside
        aria-label="Threads"
        role={open ? "dialog" : undefined}
        className={cn(
          "fixed inset-y-0 left-0 z-40 flex w-[272px] shrink-0 flex-col border-r border-line bg-sidebar transition-transform duration-base ease-out lg:static lg:translate-x-0",
          open ? "translate-x-0" : "-translate-x-full",
        )}
      >
        <div className="flex h-[52px] shrink-0 items-center justify-between px-3.5">
          <div className="flex items-center gap-2">
            <CodjentMark className="size-6" />
            <CodjentWordmark />
          </div>
          <div className="flex items-center gap-1">
            <IconButton
              className="size-11 lg:size-8"
              icon={Plus}
              label="New thread"
              onClick={create}
              size="sm"
            />
            <IconButton
              className="size-11 lg:hidden lg:size-8"
              icon={X}
              label="Close sidebar"
              onClick={onClose}
              ref={closeRef}
              size="sm"
            />
          </div>
        </div>

        <div className="px-3 pb-2">
          <label className="flex h-9 items-center gap-2 rounded-field border border-transparent bg-surface-muted px-2.5 transition-colors duration-fast focus-within:border-line-strong focus-within:bg-surface">
            <Search className="size-3.5 shrink-0 text-faint" />
            <input
              aria-label="Search threads"
              autoComplete="off"
              className="min-w-0 flex-1 bg-transparent text-body-sm text-ink outline-none placeholder:text-faint"
              name="q"
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search threads…"
              type="search"
              value={query}
            />
          </label>
        </div>

        <div className="flex min-h-0 flex-1 flex-col overflow-y-auto overscroll-contain px-2.5 pb-4">
          <div className="mb-3 flex items-center gap-2 px-2 text-meta text-muted">
            <Folder className="size-3.5 shrink-0" />
            <span className="min-w-0 flex-1 truncate font-medium text-ink/80">
              {workspaceName || "Workspace"}
            </span>
            <span className="rounded-chip border border-line px-1.5 py-0.5 text-meta text-faint">
              local
            </span>
          </div>

          {loading ? (
            <div className="space-y-1.5 px-1">
              {[0, 1, 2].map((item) => (
                <div
                  className="h-[58px] animate-pulse rounded-field bg-surface-muted"
                  key={item}
                />
              ))}
            </div>
          ) : groups.length === 0 ? (
            <div className="px-3 py-8 text-center text-body-sm leading-5 text-faint">
              {deferredQuery ? (
                "No threads match that search."
              ) : (
                <>
                  No threads yet.
                  <br />
                  Start one with the plus above.
                </>
              )}
            </div>
          ) : (
            <div className="space-y-4">
              {groups.map((group) => (
                <section key={group.label}>
                  <h2 className="mb-1.5 px-2 text-meta text-faint">
                    {group.label}
                  </h2>
                  <div className="space-y-1">
                    {group.threads.map((thread) => {
                      const live = liveRuns[thread.id];
                      const selected = selectedID === thread.id;
                      return (
                        <button
                          aria-current={selected ? "page" : undefined}
                          className={cn(
                            "group w-full rounded-field border px-2.5 py-2 text-left transition-colors duration-fast",
                            selected
                              ? "border-line-strong bg-surface shadow-[inset_2px_0_0_var(--accent)]"
                              : "border-transparent hover:bg-surface-hover",
                          )}
                          key={thread.id}
                          onClick={() => choose(thread.id)}
                          type="button"
                        >
                          <span className="flex items-start gap-2">
                            <span className="min-w-0 flex-1">
                              <span
                                className={cn(
                                  "block truncate text-body-sm leading-5",
                                  selected
                                    ? "font-semibold text-ink"
                                    : "font-medium text-ink/90",
                                )}
                              >
                                {thread.title}
                              </span>
                              <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-meta text-faint">
                                {live ? (
                                  <ThreadLive live={live} now={now} />
                                ) : (
                                  <>
                                    <span className="truncate">
                                      {modelName(
                                        providers,
                                        thread.provider,
                                        thread.model,
                                      )}
                                    </span>
                                    <span className="shrink-0">·</span>
                                    <span className="shrink-0">
                                      {accessLabel(thread.access_mode)}
                                    </span>
                                  </>
                                )}
                              </span>
                            </span>
                            <span className="shrink-0 pt-0.5 text-meta text-faint">
                              {relativeTime(thread.updated_at)}
                            </span>
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

        <div className="shrink-0 border-t border-line px-2.5 py-2">
          <Link
            className="flex items-center gap-2 rounded-field px-2 py-1.5 text-meta font-medium text-muted transition-colors duration-fast hover:bg-surface-hover hover:text-ink"
            href="/settings/"
          >
            <Settings2 className="size-3.5" />
            Providers &amp; models
          </Link>
        </div>
      </aside>
    </>
  );
}
