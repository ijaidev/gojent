"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";
import {
  createLiveRun,
  createPendingRun,
  finishLiveRun,
  type LiveRun,
  reduceRunEvent,
} from "@/lib/run-state";
import {
  type AccessMode,
  type AttachmentInput,
  isActiveStatus,
  type Message,
  type Part,
  type ProviderInfo,
  type RunEvent,
  type RunStatus,
  type Thread,
  type ThreadDetail,
  type UpdateThreadInput,
} from "@/lib/types";
import { errorMessage, truncate, upsertThread } from "@/lib/utils";

const SELECTED_THREAD_KEY = "codjent:selected-thread:v2";

export type { LiveRun, LiveTool } from "@/lib/run-state";

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
  const aliveRef = useRef(true);

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
        return {
          ...prev,
          [threadID]: finishLiveRun(current, status, runError),
        };
      });

      try {
        const refreshed = await api.getThread(threadID);
        setThreads((prev) => upsertThread(prev, refreshed.thread));
        if (selectedRef.current === threadID) {
          detailRef.current = refreshed;
          setDetail(refreshed);
        }
        // The canceled/failed transcript is now persisted, so drop the live
        // copy of the text and tools to avoid rendering the same work twice.
        // Keep the terminal status (and failure message) for the turn header.
        setLiveRuns((prev) => {
          const current = prev[threadID];
          if (!current || current.runID !== runID) {
            return prev;
          }
          if (status === "completed") {
            const next = { ...prev };
            delete next[threadID];
            return next;
          }
          return {
            ...prev,
            [threadID]: {
              ...finishLiveRun(current, status, runError),
              textChunks: [],
              thinkingChunks: [],
              tools: [],
            },
          };
        });
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
        const current = existing ?? createPendingRun(event.run_id, threadID);
        return { ...prev, [threadID]: reduceRunEvent(current, event) };
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
        const live = createLiveRun(run);
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
        const resolved: RunEvent = {
          id: 0,
          run_id: runID,
          type: "approval_resolved",
          data: { approval_id: approvalID, allow, note },
        };
        setLiveRuns((prev) => {
          const entry = Object.entries(prev).find(
            ([, live]) => live.runID === runID,
          );
          if (!entry) {
            return prev;
          }
          const [threadID, live] = entry;
          return { ...prev, [threadID]: reduceRunEvent(live, resolved) };
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

  const boot = useCallback(
    async (preserveSelection = false) => {
      setBooting(true);
      setError(null);
      try {
        const [providerList, threadList, health] = await Promise.all([
          api.providers(),
          api.listThreads(),
          api.health(),
        ]);
        if (!aliveRef.current) {
          return;
        }
        setProviders(providerList);
        setThreads(threadList);
        setWorkspace(health.workspace);

        if (preserveSelection && selectedRef.current) {
          return;
        }

        const saved = savedSelection.read();
        const initial =
          threadList.find((thread) => thread.id === saved) ?? null;
        if (initial) {
          await loadThread(initial.id);
        } else {
          commitSelection(null);
          detailRef.current = null;
          setDetail(null);
        }
      } catch (bootError) {
        if (aliveRef.current) {
          setError(errorMessage(bootError));
        }
      } finally {
        if (aliveRef.current) {
          setBooting(false);
        }
      }
    },
    [commitSelection, loadThread],
  );

  useEffect(() => {
    aliveRef.current = true;
    void boot();

    return () => {
      aliveRef.current = false;
      for (const source of sourcesRef.current.values()) {
        source.close();
      }
      sourcesRef.current.clear();
    };
  }, [boot]);

  const retry = useCallback(() => {
    void boot(true);
  }, [boot]);

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
      return { ...prev, [active.thread_id]: createLiveRun(active) };
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
    retry,
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
