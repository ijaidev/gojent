"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";
import {
  type AccessMode,
  type AttachmentInput,
  isActiveStatus,
  type Message,
  type Part,
  type PendingApproval,
  type ProviderInfo,
  type RunEvent,
  type RunInfo,
  type RunStatus,
  type Thread,
  type ThreadDetail,
  type ToolCall,
  type UpdateThreadInput,
} from "@/lib/types";
import { errorMessage, truncate, upsertThread } from "@/lib/utils";

const SELECTED_THREAD_KEY = "codjent:selected-thread";

export interface LiveTool {
  call: ToolCall;
  status: "pending" | "approval" | "running" | "done" | "error";
  output?: string;
  error?: string;
  skipped?: boolean;
  note?: string;
}

export interface LiveRun {
  runID: string;
  threadID: string;
  status: RunStatus;
  text: string;
  thinking: string;
  tools: LiveTool[];
  approval: PendingApproval | null;
  error?: string;
  reconnecting: boolean;
  startedAt: number;
}

export interface SendOptions {
  provider?: string;
  model?: string;
  access_mode?: AccessMode;
}

export type SendMessage = (
  content: string,
  options?: SendOptions,
  attachments?: AttachmentInput[],
) => Promise<boolean>;

interface SavedSelection {
  read(): string | null;
  write(id: string): void;
  clear(): void;
}

const savedSelection: SavedSelection = {
  read() {
    try {
      return window.localStorage.getItem(SELECTED_THREAD_KEY);
    } catch {
      return null;
    }
  },
  write(id) {
    try {
      window.localStorage.setItem(SELECTED_THREAD_KEY, id);
    } catch {
      // Ignore storage failures; selection still works for this session.
    }
  },
  clear() {
    try {
      window.localStorage.removeItem(SELECTED_THREAD_KEY);
    } catch {
      // Ignore storage failures.
    }
  },
};

function liveFromRun(run: RunInfo): LiveRun {
  const startedAt = Date.parse(run.created_at);
  return {
    runID: run.id,
    threadID: run.thread_id,
    status: run.status,
    text: "",
    thinking: "",
    tools: [],
    approval: run.pending_approval ?? null,
    error: run.error,
    reconnecting: false,
    startedAt: Number.isFinite(startedAt) ? startedAt : Date.now(),
  };
}

function messageTitle(text: string, attachments: AttachmentInput[]): string {
  if (text) {
    return text;
  }
  if (attachments.length === 1) {
    return attachments[0].filename || "1 image";
  }
  return `${attachments.length} images`;
}

function settingsPatch(
  thread: Thread,
  options: SendOptions,
): UpdateThreadInput | null {
  const patch: UpdateThreadInput = {};
  if (options.provider && options.provider !== thread.provider) {
    patch.provider = options.provider;
  }
  if (options.model && options.model !== thread.model) {
    patch.model = options.model;
  }
  if (options.access_mode && options.access_mode !== thread.access_mode) {
    patch.access_mode = options.access_mode;
  }
  return Object.keys(patch).length > 0 ? patch : null;
}

export function useCodjent() {
  const [providers, setProviders] = useState<ProviderInfo[]>([]);
  const [threads, setThreads] = useState<Thread[]>([]);
  const [workspace, setWorkspace] = useState("");
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [detail, setDetail] = useState<ThreadDetail | null>(null);
  const [liveRuns, setLiveRuns] = useState<Record<string, LiveRun>>({});
  const [booting, setBooting] = useState(true);
  const [loadingThread, setLoadingThread] = useState(false);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const selectedRef = useRef<string | null>(null);
  const detailRef = useRef<ThreadDetail | null>(null);
  const liveRunsRef = useRef<Record<string, LiveRun>>({});
  const sourcesRef = useRef(new Map<string, EventSource>());
  const terminalRef = useRef(new Set<string>());

  useEffect(() => {
    selectedRef.current = selectedID;
  }, [selectedID]);

  useEffect(() => {
    detailRef.current = detail;
  }, [detail]);

  useEffect(() => {
    liveRunsRef.current = liveRuns;
  }, [liveRuns]);

  const commitSelection = useCallback((id: string | null) => {
    selectedRef.current = id;
    setSelectedID(id);
    if (id) {
      savedSelection.write(id);
    } else {
      savedSelection.clear();
    }
  }, []);

  const closeSource = useCallback((runID: string) => {
    const source = sourcesRef.current.get(runID);
    source?.close();
    sourcesRef.current.delete(runID);
  }, []);

  const finalizeRun = useCallback(
    async (
      threadID: string,
      runID: string,
      status: RunStatus,
      runError?: string,
    ) => {
      if (terminalRef.current.has(runID)) {
        return;
      }
      terminalRef.current.add(runID);
      closeSource(runID);

      setLiveRuns((prev) => {
        const current = prev[threadID];
        if (!current || current.runID !== runID) {
          return prev;
        }
        const interruption =
          status === "canceled"
            ? "The run was canceled before this tool finished."
            : status === "failed"
              ? "The run failed before this tool finished."
              : "The run ended before this tool finished.";
        return {
          ...prev,
          [threadID]: {
            ...current,
            status,
            error: runError ?? current.error,
            approval: null,
            reconnecting: false,
            tools: current.tools.map((tool) =>
              tool.status === "done" || tool.status === "error"
                ? tool
                : {
                    ...tool,
                    status: "error" as const,
                    skipped: true,
                    note: interruption,
                  },
            ),
          },
        };
      });

      try {
        const refreshed = await api.getThread(threadID);
        setThreads((prev) => upsertThread(prev, refreshed.thread));
        if (selectedRef.current === threadID) {
          detailRef.current = refreshed;
          setDetail(refreshed);
        }
        if (status === "completed") {
          setLiveRuns((prev) => {
            if (prev[threadID]?.runID !== runID) {
              return prev;
            }
            const next = { ...prev };
            delete next[threadID];
            return next;
          });
        }
      } catch (refreshError) {
        setError(
          `The run ended, but the transcript could not be refreshed: ${errorMessage(refreshError)}`,
        );
      }
    },
    [closeSource],
  );

  const applyEvent = useCallback(
    (event: RunEvent, threadID: string) => {
      if (
        event.type === "run_completed" ||
        event.type === "run_failed" ||
        event.type === "run_canceled"
      ) {
        const status: RunStatus =
          event.type === "run_completed"
            ? "completed"
            : event.type === "run_failed"
              ? "failed"
              : "canceled";
        const runError =
          event.type === "run_failed"
            ? event.data.error
            : event.type === "run_canceled"
              ? event.data.reason
              : undefined;
        void finalizeRun(threadID, event.run_id, status, runError);
        return;
      }

      setLiveRuns((prev) => {
        const existing = prev[threadID];
        if (existing && existing.runID !== event.run_id) {
          return prev;
        }
        const current: LiveRun = existing ?? {
          runID: event.run_id,
          threadID,
          status: "running",
          text: "",
          thinking: "",
          tools: [],
          approval: null,
          reconnecting: false,
          startedAt: Date.now(),
        };

        let next = current;
        switch (event.type) {
          case "run_started":
            next = {
              ...current,
              status: "running",
              reconnecting: false,
            };
            break;
          case "text_delta":
            next = { ...current, text: current.text + event.data.text };
            break;
          case "thinking_delta":
            next = {
              ...current,
              thinking: current.thinking + event.data.text,
            };
            break;
          case "tool_call": {
            const call = event.data;
            const exists = current.tools.some(
              (tool) => tool.call.id === call.id,
            );
            next = {
              ...current,
              tools: exists
                ? current.tools.map((tool) =>
                    tool.call.id === call.id ? { ...tool, call } : tool,
                  )
                : [...current.tools, { call, status: "pending" as const }],
            };
            break;
          }
          case "approval_required": {
            const approval = event.data.approval;
            next = {
              ...current,
              status: "waiting_approval",
              approval,
              tools: current.tools.map((tool) =>
                tool.call.id === approval.tool_call_id
                  ? { ...tool, status: "approval" }
                  : tool,
              ),
            };
            break;
          }
          case "approval_resolved": {
            const resolved = event.data;
            const toolID =
              current.approval?.id === resolved.approval_id
                ? current.approval.tool_call_id
                : null;
            next = {
              ...current,
              status: "running",
              approval:
                current.approval?.id === resolved.approval_id
                  ? null
                  : current.approval,
              tools: current.tools.map((tool) =>
                tool.call.id === toolID
                  ? {
                      ...tool,
                      status: resolved.allow ? "running" : "error",
                      note: resolved.note,
                    }
                  : tool,
              ),
            };
            break;
          }
          case "tool_result": {
            const result = event.data;
            const status = result.error
              ? "error"
              : result.skipped
                ? "done"
                : "done";
            const existingTool = current.tools.find(
              (tool) => tool.call.id === result.id,
            );
            const updated: LiveTool = {
              call: existingTool?.call ?? {
                id: result.id,
                name: result.name,
                arguments: "",
              },
              status,
              output: result.output,
              error: result.error,
              skipped: result.skipped,
            };
            next = {
              ...current,
              tools: existingTool
                ? current.tools.map((tool) =>
                    tool.call.id === result.id ? updated : tool,
                  )
                : [...current.tools, updated],
            };
            break;
          }
          default:
            break;
        }
        return { ...prev, [threadID]: next };
      });
    },
    [finalizeRun],
  );

  const openStream = useCallback(
    (runID: string, threadID: string) => {
      if (sourcesRef.current.has(runID)) {
        return;
      }
      const source = new EventSource(api.eventsURL(runID));
      sourcesRef.current.set(runID, source);

      source.onopen = () => {
        setLiveRuns((prev) => {
          const current = prev[threadID];
          if (!current || current.runID !== runID || !current.reconnecting) {
            return prev;
          }
          return {
            ...prev,
            [threadID]: { ...current, reconnecting: false },
          };
        });
      };

      source.onmessage = (message) => {
        let event: RunEvent;
        try {
          event = JSON.parse(message.data) as RunEvent;
        } catch {
          return;
        }
        if (event.run_id !== runID) {
          return;
        }
        applyEvent(event, threadID);
      };

      source.onerror = () => {
        if (terminalRef.current.has(runID)) {
          closeSource(runID);
          return;
        }
        setLiveRuns((prev) => {
          const current = prev[threadID];
          if (!current || current.runID !== runID || current.reconnecting) {
            return prev;
          }
          return {
            ...prev,
            [threadID]: { ...current, reconnecting: true },
          };
        });
      };
    },
    [applyEvent, closeSource],
  );

  const loadThread = useCallback(
    async (id: string): Promise<ThreadDetail | null> => {
      commitSelection(id);
      setLoadingThread(true);
      try {
        const loaded = await api.getThread(id);
        if (selectedRef.current !== id) {
          return loaded;
        }
        detailRef.current = loaded;
        setDetail(loaded);
        setThreads((prev) => upsertThread(prev, loaded.thread));
        return loaded;
      } catch (loadError) {
        if (selectedRef.current === id) {
          setError(errorMessage(loadError));
        }
        return null;
      } finally {
        if (selectedRef.current === id) {
          setLoadingThread(false);
        }
      }
    },
    [commitSelection],
  );

  const selectThread = useCallback(
    async (id: string) => {
      if (id === selectedRef.current && detailRef.current) {
        return;
      }
      setError(null);
      await loadThread(id);
    },
    [loadThread],
  );

  const startNewThread = useCallback(() => {
    commitSelection(null);
    detailRef.current = null;
    setDetail(null);
    setLoadingThread(false);
    setError(null);
  }, [commitSelection]);

  const sendMessage = useCallback(
    async (
      content: string,
      options?: SendOptions,
      attachments: AttachmentInput[] = [],
    ): Promise<boolean> => {
      const text = content.trim();
      if (!text && attachments.length === 0) {
        return false;
      }

      setSending(true);
      setError(null);
      let threadID = selectedRef.current;

      try {
        let thread =
          threadID && detailRef.current?.thread.id === threadID
            ? detailRef.current.thread
            : null;

        if (!threadID) {
          const provider =
            providers.find((item) => item.id === options?.provider) ??
            providers[0];
          if (!provider) {
            throw new Error("No model providers are configured.");
          }
          thread = await api.createThread({
            title: "",
            provider: options?.provider ?? provider.id,
            model: options?.model ?? provider.default_model,
            access_mode: options?.access_mode ?? "ask",
          });
          threadID = thread.id;
          commitSelection(thread.id);
          setThreads((prev) => upsertThread(prev, thread as Thread));
          const emptyDetail: ThreadDetail = { thread, messages: [] };
          detailRef.current = emptyDetail;
          setDetail(emptyDetail);
        } else if (!thread) {
          const loaded = await api.getThread(threadID);
          thread = loaded.thread;
          detailRef.current = loaded;
          setDetail(loaded);
        } else if (options) {
          const patch = settingsPatch(thread, options);
          if (patch) {
            thread = await api.updateThread(thread.id, patch);
            setThreads((prev) => upsertThread(prev, thread as Thread));
            const current = detailRef.current;
            if (current?.thread.id === thread.id) {
              const updatedDetail = { ...current, thread };
              detailRef.current = updatedDetail;
              setDetail(updatedDetail);
            }
          }
        }

        const threadIDValue = threadID;
        const now = new Date().toISOString();
        const contentParts: Part[] = [
          ...attachments.map((attachment) => ({
            type: "file",
            mime_type: attachment.mime_type,
            data: attachment.data,
            filename: attachment.filename,
          })),
          ...(text ? [{ type: "text" as const, text }] : []),
        ];
        const optimistic: Message = {
          id: `local_${Date.now()}`,
          thread_id: threadIDValue,
          role: "user",
          content: contentParts,
          created_at: now,
        };
        setDetail((prev) => {
          if (!prev || prev.thread.id !== threadIDValue) {
            return prev;
          }
          const next = {
            ...prev,
            messages: [...prev.messages, optimistic],
          };
          detailRef.current = next;
          return next;
        });

        const run = await api.startMessage(threadIDValue, text, attachments);
        const live = liveFromRun(run);
        terminalRef.current.delete(run.id);
        setLiveRuns((prev) => ({ ...prev, [threadIDValue]: live }));
        openStream(run.id, threadIDValue);

        const title =
          thread?.title === "New thread"
            ? truncate(messageTitle(text, attachments))
            : (thread?.title ?? "New thread");
        setThreads((prev) =>
          prev.map((item) =>
            item.id === threadIDValue
              ? { ...item, title, updated_at: now }
              : item,
          ),
        );
        setDetail((prev) => {
          if (!prev || prev.thread.id !== threadIDValue) {
            return prev;
          }
          const next = {
            ...prev,
            thread: { ...prev.thread, title, updated_at: now },
          };
          detailRef.current = next;
          return next;
        });
        return true;
      } catch (sendError) {
        setError(errorMessage(sendError));
        if (threadID) {
          try {
            const refreshed = await api.getThread(threadID);
            setThreads((prev) => upsertThread(prev, refreshed.thread));
            if (selectedRef.current === threadID) {
              detailRef.current = refreshed;
              setDetail(refreshed);
            }
          } catch {
            // Keep the optimistic transcript if the refresh also fails.
          }
        }
        return false;
      } finally {
        setSending(false);
      }
    },
    [commitSelection, openStream, providers],
  );

  const updateThread = useCallback(
    async (id: string, input: UpdateThreadInput): Promise<Thread | null> => {
      setError(null);
      try {
        const thread = await api.updateThread(id, input);
        setThreads((prev) => upsertThread(prev, thread));
        const current = detailRef.current;
        if (current?.thread.id === id) {
          const next = { ...current, thread };
          detailRef.current = next;
          setDetail(next);
        }
        return thread;
      } catch (updateError) {
        setError(errorMessage(updateError));
        return null;
      }
    },
    [],
  );

  const deleteThread = useCallback(
    async (id: string): Promise<boolean> => {
      setError(null);
      try {
        await api.deleteThread(id);
        setThreads((prev) => prev.filter((item) => item.id !== id));

        for (const live of Object.values(liveRunsRef.current)) {
          if (live.threadID === id) {
            terminalRef.current.add(live.runID);
            closeSource(live.runID);
          }
        }
        setLiveRuns((prev) => {
          const next = { ...prev };
          delete next[id];
          return next;
        });

        if (selectedRef.current === id) {
          const remaining = threads.filter((item) => item.id !== id);
          commitSelection(null);
          detailRef.current = null;
          setDetail(null);
          if (remaining[0]) {
            await loadThread(remaining[0].id);
          }
        }
        return true;
      } catch (deleteError) {
        setError(errorMessage(deleteError));
        return false;
      }
    },
    [closeSource, commitSelection, loadThread, threads],
  );

  const resolveApproval = useCallback(
    async (
      runID: string,
      approvalID: string,
      allow: boolean,
      note?: string,
    ): Promise<boolean> => {
      setError(null);
      try {
        await api.resolveApproval(runID, approvalID, allow, note);
        setLiveRuns((prev) => {
          const entry = Object.entries(prev).find(
            ([, live]) => live.runID === runID,
          );
          if (!entry) {
            return prev;
          }
          const [threadID, live] = entry;
          const toolID =
            live.approval?.id === approvalID
              ? live.approval.tool_call_id
              : null;
          return {
            ...prev,
            [threadID]: {
              ...live,
              status:
                live.status === "waiting_approval" ? "running" : live.status,
              approval: live.approval?.id === approvalID ? null : live.approval,
              tools: live.tools.map((tool) =>
                tool.call.id === toolID
                  ? { ...tool, status: allow ? "running" : "error" }
                  : tool,
              ),
            },
          };
        });
        return true;
      } catch (approvalError) {
        setError(errorMessage(approvalError));
        return false;
      }
    },
    [],
  );

  const cancelRun = useCallback(async (runID: string): Promise<boolean> => {
    setError(null);
    try {
      await api.cancelRun(runID);
      return true;
    } catch (cancelError) {
      setError(errorMessage(cancelError));
      return false;
    }
  }, []);

  useEffect(() => {
    let alive = true;

    const boot = async () => {
      try {
        const [providerList, threadList, health] = await Promise.all([
          api.providers(),
          api.listThreads(),
          api.health(),
        ]);
        if (!alive) {
          return;
        }
        setProviders(providerList);
        setThreads(threadList);
        setWorkspace(health.workspace);

        const saved = savedSelection.read();
        const initial =
          threadList.find((thread) => thread.id === saved) ??
          threadList[0] ??
          null;
        if (initial) {
          await loadThread(initial.id);
        }
      } catch (bootError) {
        if (alive) {
          setError(errorMessage(bootError));
        }
      } finally {
        if (alive) {
          setBooting(false);
        }
      }
    };

    void boot();

    return () => {
      alive = false;
      for (const source of sourcesRef.current.values()) {
        source.close();
      }
      sourcesRef.current.clear();
    };
  }, [loadThread]);

  useEffect(() => {
    const active = detail?.active_run;
    if (!active || !isActiveStatus(active.status)) {
      return;
    }
    if (sourcesRef.current.has(active.id)) {
      return;
    }

    setLiveRuns((prev) => {
      const current = prev[active.thread_id];
      if (current?.runID === active.id) {
        return prev;
      }
      if (current && isActiveStatus(current.status)) {
        return prev;
      }
      return { ...prev, [active.thread_id]: liveFromRun(active) };
    });
    openStream(active.id, active.thread_id);
  }, [detail, openStream]);

  return {
    providers,
    threads,
    workspace,
    selectedID,
    detail,
    liveRuns,
    booting,
    loadingThread,
    sending,
    error,
    clearError: () => setError(null),
    selectThread,
    startNewThread,
    sendMessage,
    updateThread,
    deleteThread,
    resolveApproval,
    cancelRun,
  };
}

export type CodjentState = ReturnType<typeof useCodjent>;
