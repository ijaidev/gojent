"use client";

import {
  CircleAlert,
  Folder,
  Menu,
  Server,
  Settings2,
  ShieldAlert,
  ShieldCheck,
} from "lucide-react";
import Link from "next/link";
import { useEffect, useState } from "react";
import { CodjentMark } from "@/components/brand";
import { Composer } from "@/components/composer";
import { SettingsDialog } from "@/components/settings-dialog";
import { Sidebar } from "@/components/sidebar";
import { Spinner } from "@/components/spinner";
import { ThreadView } from "@/components/thread-view";
import type { LiveRun } from "@/hooks/use-codjent";
import { useCodjent } from "@/hooks/use-codjent";
import type { Thread } from "@/lib/types";
import { isActiveStatus } from "@/lib/types";
import { basename, formatElapsed, statusCopy } from "@/lib/utils";

function LiveStatusPill({ live }: { live?: LiveRun }) {
  const [now, setNow] = useState(() => Date.now());
  const active = Boolean(live && isActiveStatus(live.status));

  useEffect(() => {
    if (!active) {
      return;
    }
    const interval = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(interval);
  }, [active]);

  if (!live) {
    return null;
  }
  if (live.reconnecting) {
    return (
      <span className="hidden items-center gap-1.5 rounded-full border border-warning/25 bg-warning-soft px-2.5 py-1 text-[10.5px] font-medium text-warning sm:inline-flex">
        <CircleAlert className="size-3" />
        Reconnecting
      </span>
    );
  }
  if (live.status === "waiting_approval") {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-full border border-warning/25 bg-warning-soft px-2.5 py-1 text-[10.5px] font-medium text-warning">
        <ShieldAlert className="size-3" />
        Needs approval
      </span>
    );
  }
  if (active) {
    return (
      <span className="hidden items-center gap-1.5 rounded-full border border-accent/15 bg-accent-soft px-2.5 py-1 text-[10.5px] font-medium text-accent sm:inline-flex">
        <Spinner className="size-3" label="Working" />
        Working {formatElapsed(now - live.startedAt)}
      </span>
    );
  }
  if (live.status === "failed") {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-full border border-danger/20 bg-danger-soft px-2.5 py-1 text-[10.5px] font-medium text-danger">
        <CircleAlert className="size-3" />
        Failed
      </span>
    );
  }
  if (live.status === "canceled") {
    return (
      <span className="rounded-full border border-line bg-surface-muted px-2.5 py-1 text-[10.5px] font-medium text-muted">
        {statusCopy(live.status)}
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1.5 rounded-full border border-success/20 bg-success-soft px-2.5 py-1 text-[10.5px] font-medium text-success">
      <ShieldCheck className="size-3" />
      Done
    </span>
  );
}

function Header({
  thread,
  live,
  workspaceName,
  onOpenSidebar,
  onOpenSettings,
}: {
  thread: Thread | null;
  live?: LiveRun;
  workspaceName: string;
  onOpenSidebar: () => void;
  onOpenSettings: () => void;
}) {
  return (
    <header className="flex h-[60px] shrink-0 items-center gap-3 border-b border-line px-3 sm:px-5">
      <button
        type="button"
        aria-label="Open sidebar"
        className="grid size-8 place-items-center rounded-lg text-muted transition hover:bg-surface-muted hover:text-ink lg:hidden"
        onClick={onOpenSidebar}
      >
        <Menu className="size-4" />
      </button>

      <div className="flex min-w-0 items-center gap-2 text-[12px]">
        <span className="hidden min-w-0 items-center gap-1.5 text-muted sm:flex">
          <Folder className="size-3.5 shrink-0" />
          <span className="max-w-[180px] truncate">{workspaceName}</span>
        </span>
        <span className="hidden text-line-strong sm:inline">/</span>
        <span className="min-w-0 truncate font-medium text-ink">
          {thread?.title ?? "New thread"}
        </span>
      </div>

      <div className="ml-auto flex items-center gap-1.5">
        <LiveStatusPill live={live} />
        <Link
          aria-label="Provider settings"
          className="grid size-8 place-items-center rounded-lg text-muted transition hover:bg-surface-muted hover:text-ink"
          href="/settings"
        >
          <Server className="size-4" />
        </Link>
        <button
          type="button"
          aria-label="Thread settings"
          className="grid size-8 place-items-center rounded-lg text-muted transition hover:bg-surface-muted hover:text-ink disabled:cursor-not-allowed disabled:opacity-40"
          disabled={!thread}
          onClick={onOpenSettings}
        >
          <Settings2 className="size-4" />
        </button>
      </div>
    </header>
  );
}

function ErrorBanner({
  message,
  onDismiss,
}: {
  message: string;
  onDismiss: () => void;
}) {
  return (
    <div className="flex shrink-0 items-start gap-2 border-b border-danger/15 bg-danger-soft px-4 py-2 text-danger">
      <CircleAlert className="mt-0.5 size-3.5 shrink-0" />
      <p className="min-w-0 flex-1 whitespace-pre-wrap break-words text-[11px] leading-5">
        {message}
      </p>
      <button
        type="button"
        aria-label="Dismiss error"
        className="rounded px-1 text-[11px] font-medium opacity-70 transition hover:opacity-100"
        onClick={onDismiss}
      >
        Dismiss
      </button>
    </div>
  );
}

function LoadingScreen() {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 text-muted">
      <CodjentMark className="size-9 text-ink" />
      <div className="flex items-center gap-2 text-[11px]">
        <Spinner className="size-3.5" />
        Connecting to codjent
      </div>
    </div>
  );
}

function ConnectionError({ message }: { message: string }) {
  return (
    <div className="flex flex-1 items-center justify-center px-6">
      <div className="w-full max-w-md rounded-2xl border border-line bg-surface p-6 text-center">
        <div className="mx-auto grid size-10 place-items-center rounded-xl bg-danger-soft text-danger">
          <CircleAlert className="size-5" />
        </div>
        <h2 className="mt-3 text-[15px] font-semibold">
          Could not reach codjent
        </h2>
        <p className="mt-1.5 whitespace-pre-wrap break-words text-[11px] leading-5 text-muted">
          {message}
        </p>
        <button
          type="button"
          className="mt-4 rounded-lg bg-ink px-3.5 py-2 text-[11px] font-medium text-app transition hover:opacity-85"
          onClick={() => window.location.reload()}
        >
          Retry connection
        </button>
        <p className="mt-3 text-[10px] text-faint">
          Start the backend with{" "}
          <code className="rounded border border-line bg-code px-1.5 py-0.5 font-mono text-[10px]">
            go run ./codjent/cmd/codjent
          </code>
        </p>
      </div>
    </div>
  );
}

function ProviderSetup() {
  return (
    <div className="flex flex-1 items-center justify-center px-6">
      <div className="w-full max-w-md rounded-2xl border border-line bg-surface p-6 text-center">
        <div className="mx-auto grid size-10 place-items-center rounded-xl bg-accent-soft text-accent">
          <Server className="size-5" />
        </div>
        <h2 className="mt-3 text-[15px] font-semibold">
          Connect a model provider
        </h2>
        <p className="mt-1.5 text-[11px] leading-5 text-muted">
          Add an endpoint, an optional API key, and at least one model. codjent
          stores provider settings locally in SQLite.
        </p>
        <Link
          className="mt-4 inline-flex items-center gap-1.5 rounded-lg bg-ink px-3.5 py-2 text-[11px] font-medium text-app transition hover:opacity-85"
          href="/settings"
        >
          <Server className="size-3.5" />
          Open provider settings
        </Link>
        <p className="mt-3 text-[10px] leading-4 text-faint">
          Supports OpenAI Responses, OpenAI Chat Completions, Anthropic, and
          Google Gen AI.
        </p>
      </div>
    </div>
  );
}

function EmptyState({
  providers,
  workspaceName,
  sending,
  onSend,
}: {
  providers: ReturnType<typeof useCodjent>["providers"];
  workspaceName: string;
  sending: boolean;
  onSend: ReturnType<typeof useCodjent>["sendMessage"];
}) {
  return (
    <div className="flex min-h-0 flex-1 items-center justify-center overflow-y-auto px-5 py-10 sm:px-8">
      <div className="w-full max-w-[780px]">
        <div className="mb-6 flex justify-center">
          <span className="inline-flex items-center gap-1.5 rounded-full border border-line bg-surface px-2.5 py-1 text-[10.5px] text-muted">
            <Folder className="size-3" />
            <span className="max-w-[240px] truncate">
              {workspaceName || "Local workspace"}
            </span>
          </span>
        </div>
        <h1 className="text-center text-[25px] font-semibold leading-[1.2] tracking-[-0.045em] text-ink sm:text-[33px]">
          What should we build in{" "}
          <span className="underline decoration-line-strong decoration-[2px] underline-offset-[7px]">
            {workspaceName || "this workspace"}
          </span>
          ?
        </h1>
        <p className="mx-auto mt-3 max-w-[470px] text-center text-[12px] leading-5 text-muted">
          Describe a task, paste a screenshot, or drop in a design. Codjent can
          read and edit this checkout, run commands, and show every step while
          it works.
        </p>

        <div className="mt-7">
          <Composer
            autoFocus
            busy={false}
            large
            onSend={onSend}
            onStop={() => {}}
            onUpdateThread={async () => null}
            providers={providers}
            sending={sending}
            thread={null}
            workspaceName={workspaceName}
          />
        </div>
      </div>
    </div>
  );
}

export function CodjentApp() {
  const codjent = useCodjent();
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);

  const workspaceName = basename(codjent.workspace) || "workspace";
  const selectedThread =
    codjent.detail?.thread ??
    codjent.threads.find((thread) => thread.id === codjent.selectedID) ??
    null;
  const selectedLive = codjent.selectedID
    ? codjent.liveRuns[codjent.selectedID]
    : undefined;
  const busy = Boolean(selectedLive && isActiveStatus(selectedLive.status));

  useEffect(() => {
    if (!selectedThread) {
      setSettingsOpen(false);
    }
  }, [selectedThread]);

  const stop = () => {
    if (selectedLive) {
      void codjent.cancelRun(selectedLive.runID);
    }
  };

  const resolveApproval = async (approvalID: string, allow: boolean) => {
    if (!selectedLive) {
      return false;
    }
    return codjent.resolveApproval(selectedLive.runID, approvalID, allow);
  };

  const updateSelectedThread = async (
    input: Parameters<typeof codjent.updateThread>[1],
  ) => {
    if (!selectedThread) {
      return null;
    }
    return codjent.updateThread(selectedThread.id, input);
  };

  const renderContent = () => {
    if (codjent.booting) {
      return <LoadingScreen />;
    }
    if (codjent.providers.length === 0) {
      if (codjent.error) {
        return <ConnectionError message={codjent.error} />;
      }
      return <ProviderSetup />;
    }
    if (codjent.selectedID) {
      const detail = codjent.detail;
      if (!detail || detail.thread.id !== codjent.selectedID) {
        if (codjent.loadingThread) {
          return (
            <div className="flex flex-1 items-center justify-center gap-2 text-[11px] text-muted">
              <Spinner className="size-3.5" />
              Loading thread
            </div>
          );
        }
        return (
          <div className="flex flex-1 items-center justify-center px-6">
            <div className="text-center">
              <p className="text-[13px] font-medium text-ink">
                This thread could not be loaded.
              </p>
              <p className="mt-1 max-w-sm text-[11px] leading-5 text-faint">
                It may have been deleted. Start a new thread to continue.
              </p>
              <button
                type="button"
                className="mt-4 rounded-lg bg-ink px-3 py-1.5 text-[11px] font-medium text-app"
                onClick={codjent.startNewThread}
              >
                New thread
              </button>
            </div>
          </div>
        );
      }
      return (
        <ThreadView
          key={detail.thread.id}
          live={selectedLive}
          messages={detail.messages}
          onResolveApproval={resolveApproval}
          onSend={codjent.sendMessage}
          onStop={stop}
          onUpdateThread={updateSelectedThread}
          providers={codjent.providers}
          sending={codjent.sending}
          thread={detail.thread}
          workspaceName={workspaceName}
        />
      );
    }
    return (
      <EmptyState
        onSend={codjent.sendMessage}
        providers={codjent.providers}
        sending={codjent.sending}
        workspaceName={workspaceName}
      />
    );
  };

  return (
    <div className="flex h-dvh w-full overflow-hidden bg-app text-ink">
      <Sidebar
        liveRuns={codjent.liveRuns}
        loading={codjent.booting}
        onClose={() => setSidebarOpen(false)}
        onNew={() => {
          codjent.startNewThread();
          setSettingsOpen(false);
        }}
        onSelect={(id) => {
          void codjent.selectThread(id);
        }}
        open={sidebarOpen}
        selectedID={codjent.selectedID}
        threads={codjent.threads}
        workspaceName={workspaceName}
      />

      <main className="flex min-w-0 flex-1 flex-col">
        <Header
          live={selectedLive}
          onOpenSettings={() => setSettingsOpen(true)}
          onOpenSidebar={() => setSidebarOpen(true)}
          thread={selectedThread}
          workspaceName={workspaceName}
        />

        {codjent.error && codjent.providers.length > 0 ? (
          <ErrorBanner message={codjent.error} onDismiss={codjent.clearError} />
        ) : null}

        <div className="flex min-h-0 flex-1 flex-col">{renderContent()}</div>
      </main>

      <SettingsDialog
        busy={busy}
        onDelete={async () => {
          if (!selectedThread) {
            return false;
          }
          return codjent.deleteThread(selectedThread.id);
        }}
        onOpenChange={setSettingsOpen}
        onSave={async (input) => {
          if (!selectedThread) {
            return null;
          }
          return codjent.updateThread(selectedThread.id, input);
        }}
        open={settingsOpen}
        providers={codjent.providers}
        thread={selectedThread}
        workspaceName={codjent.workspace}
      />
    </div>
  );
}
