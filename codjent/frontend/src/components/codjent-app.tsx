"use client";

import {
  CircleAlert,
  FlaskConical,
  Folder,
  GitPullRequest,
  Menu,
  Plus,
  Search,
  Server,
  Settings2,
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
import { Button } from "@/components/ui/button";
import { IconButton } from "@/components/ui/icon-button";
import { StatusBadge } from "@/components/ui/status-badge";
import type { LiveRun } from "@/hooks/use-codjent";
import { useCodjent } from "@/hooks/use-codjent";
import { connectionStatus, runStatus } from "@/lib/status";
import type { Thread } from "@/lib/types";
import { isActiveStatus } from "@/lib/types";
import { basename, modelName } from "@/lib/utils";

function LiveStatusPill({
  live,
  onJumpToApproval,
}: {
  live?: LiveRun;
  onJumpToApproval: () => void;
}) {
  if (!live) {
    return null;
  }
  if (live.reconnecting) {
    return (
      <StatusBadge
        className="hidden sm:inline-flex"
        compact
        plain
        status={connectionStatus.reconnecting}
      />
    );
  }
  if (live.status === "waiting_approval") {
    return (
      <button
        className="inline-flex min-h-11 items-center rounded-field px-1.5 py-1 transition-colors duration-fast hover:bg-warning-soft focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 sm:min-h-0"
        onClick={onJumpToApproval}
        title="Jump to approval"
        type="button"
      >
        <StatusBadge compact plain status={runStatus.waiting_approval} />
      </button>
    );
  }
  return (
    <StatusBadge
      className="hidden sm:inline-flex"
      compact
      plain
      status={runStatus[live.status]}
    />
  );
}

function Header({
  thread,
  live,
  workspaceName,
  onOpenSidebar,
  onOpenSettings,
  onJumpToApproval,
}: {
  thread: Thread | null;
  live?: LiveRun;
  workspaceName: string;
  onOpenSidebar: () => void;
  onOpenSettings: () => void;
  onJumpToApproval: () => void;
}) {
  return (
    <header className="flex h-[52px] shrink-0 items-center gap-3 border-b border-line px-4 sm:px-5">
      <IconButton
        className="size-11 sm:size-8 lg:hidden"
        icon={Menu}
        label="Open sidebar"
        onClick={onOpenSidebar}
        size="sm"
      />

      <div className="flex min-w-0 items-center gap-2 text-meta">
        <span className="hidden min-w-0 items-center gap-1.5 text-muted sm:flex">
          <Folder className="size-3.5 shrink-0" />
          <span className="max-w-[180px] truncate">{workspaceName}</span>
        </span>
        <span className="hidden text-line-strong sm:inline">/</span>
        <span className="min-w-0 truncate text-body-sm font-medium text-ink">
          {thread?.title ?? "New thread"}
        </span>
      </div>

      <div className="ml-auto flex items-center gap-1.5">
        <LiveStatusPill live={live} onJumpToApproval={onJumpToApproval} />
        <IconButton
          className="size-11 sm:size-8"
          disabled={!thread}
          icon={Settings2}
          label="Thread settings"
          onClick={onOpenSettings}
          size="sm"
        />
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
    <div
      className="flex shrink-0 items-start gap-2 border-b border-danger/15 bg-danger-soft px-4 py-2 text-danger"
      role="alert"
    >
      <CircleAlert className="mt-0.5 size-3.5 shrink-0" />
      <p className="min-w-0 flex-1 whitespace-pre-wrap break-words text-body-sm leading-5">
        {message}
      </p>
      <button
        className="rounded px-1 text-meta font-medium opacity-70 transition hover:opacity-100"
        onClick={onDismiss}
        type="button"
      >
        Dismiss
      </button>
    </div>
  );
}

function LoadingScreen() {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 text-muted">
      <CodjentMark className="size-9" />
      <div className="flex items-center gap-2 text-body-sm">
        <Spinner className="size-3.5" />
        Connecting to Codjent
      </div>
    </div>
  );
}

function ConnectionError({
  message,
  onRetry,
}: {
  message: string;
  onRetry: () => void;
}) {
  return (
    <div className="flex flex-1 items-center justify-center px-6">
      <div className="w-full max-w-md rounded-panel border border-line bg-surface p-6 text-center">
        <CircleAlert className="mx-auto size-5 text-danger" />
        <h2 className="mt-3 text-title font-semibold">
          Could not reach Codjent
        </h2>
        <p className="mt-1.5 whitespace-pre-wrap break-words text-body-sm leading-5 text-muted">
          {message}
        </p>
        <Button
          className="mt-4 h-11 sm:h-9"
          onClick={onRetry}
          variant="primary"
        >
          Retry connection
        </Button>
        <p className="mt-3 text-meta leading-5 text-faint">
          Start the backend with{" "}
          <code className="rounded-chip border border-line bg-code px-1.5 py-0.5 font-mono text-meta">
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
      <div className="w-full max-w-md rounded-panel border border-line bg-surface p-6 text-center">
        <Server className="mx-auto size-5 text-accent" />
        <h2 className="mt-3 text-title font-semibold">
          Connect a model provider
        </h2>
        <p className="mt-1.5 text-body-sm leading-5 text-muted">
          Add an endpoint and at least one model. Settings stay on this machine
          and are only sent to the endpoint you configure.
        </p>
        <Link
          className="mt-4 inline-flex h-11 items-center gap-1.5 rounded-field border border-accent bg-accent px-3 text-body-sm font-medium text-app transition-colors duration-fast hover:bg-accent/90 sm:h-9"
          href="/settings/"
        >
          <Server className="size-3.5" />
          Open provider settings
        </Link>
        <p className="mt-3 text-meta leading-5 text-faint">
          Supports OpenAI Responses, OpenAI Chat Completions, Anthropic, and
          Google Gen AI.
        </p>
      </div>
    </div>
  );
}

function EmptyState({
  providers,
  threads,
  workspaceName,
  sending,
  onSend,
}: {
  providers: ReturnType<typeof useCodjent>["providers"];
  threads: Thread[];
  workspaceName: string;
  sending: boolean;
  onSend: ReturnType<typeof useCodjent>["sendMessage"];
}) {
  const [draft, setDraft] = useState<{ id: number; text: string } | null>(null);
  const defaultProvider = providers[0];
  const defaultModel = defaultProvider
    ? modelName(providers, defaultProvider.id, defaultProvider.default_model)
    : "No model";
  const threadCount = threads.length === 0 ? "None" : `${threads.length}`;

  const suggestions = [
    {
      icon: Search,
      title: "Explain this codebase",
      description: "Map the entry points and main packages.",
      prompt:
        "Explain the architecture of this codebase, starting from the entry points and the main packages.",
    },
    {
      icon: FlaskConical,
      title: "Fix a failing test",
      description: "Run the suite and fix the first failure.",
      prompt: "Run the test suite, find the first failing test, and fix it.",
    },
    {
      icon: Plus,
      title: "Add a feature",
      description: "Plan and implement a small change.",
      prompt:
        "Add a small feature to this project. Start by proposing the plan and the files you'll touch.",
    },
    {
      icon: GitPullRequest,
      title: "Review my changes",
      description: "Check the diff for bugs and gaps.",
      prompt:
        "Review the uncommitted changes in this checkout and point out bugs, risks, and missing tests.",
    },
  ];

  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto grid w-full max-w-[760px] grid-cols-1 gap-8 px-5 py-8 sm:px-8 xl:max-w-[1080px] xl:grid-cols-[minmax(0,760px)_280px] xl:justify-center xl:gap-10">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-meta text-muted">
            <Folder className="size-3.5" />
            <span className="max-w-[240px] truncate">
              {workspaceName || "Local workspace"}
            </span>
            <span className="text-line-strong">·</span>
            <span>local</span>
          </div>

          <h1 className="mt-4 text-hero font-semibold tracking-[-0.02em] text-ink">
            What are we building?
          </h1>
          <p className="mt-2 max-w-[540px] text-body-sm leading-5 text-muted">
            Describe a task or paste a screenshot. Codjent can read and edit
            this checkout, run commands, and show its work.
          </p>

          <div className="mt-6">
            <Composer
              autoFocus
              busy={false}
              initialValue={draft?.text ?? ""}
              key={draft?.id ?? "new"}
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

          <div className="mt-7">
            <div className="text-meta font-medium text-muted">Start with</div>
            <div className="mt-2 grid gap-2 sm:grid-cols-2">
              {suggestions.map((suggestion) => {
                const Icon = suggestion.icon;
                return (
                  <button
                    className="group flex items-start gap-3 rounded-panel border border-line bg-surface p-3 text-left transition-colors duration-fast hover:border-line-strong hover:bg-surface-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
                    key={suggestion.title}
                    onClick={() =>
                      setDraft({ id: Date.now(), text: suggestion.prompt })
                    }
                    type="button"
                  >
                    <Icon className="mt-0.5 size-4 shrink-0 text-muted transition-colors group-hover:text-accent" />
                    <span className="min-w-0">
                      <span className="block text-body-sm font-medium text-ink">
                        {suggestion.title}
                      </span>
                      <span className="mt-0.5 block text-meta leading-4 text-faint">
                        {suggestion.description}
                      </span>
                    </span>
                  </button>
                );
              })}
            </div>
          </div>
        </div>

        <aside className="hidden self-start xl:block">
          <div className="rounded-panel border border-line bg-surface p-4">
            <div className="flex items-center justify-between gap-2">
              <span className="text-meta font-medium text-muted">
                Workspace
              </span>
              <span className="rounded-chip border border-line px-1.5 py-0.5 text-meta text-faint">
                local
              </span>
            </div>
            <div className="mt-2 flex items-center gap-1.5 text-body font-medium text-ink">
              <Folder className="size-4 text-muted" />
              <span className="min-w-0 truncate">{workspaceName}</span>
            </div>
            <dl className="mt-4 space-y-3 border-t border-line pt-3 text-meta">
              <div className="flex items-start justify-between gap-3">
                <dt className="text-muted">Model</dt>
                <dd
                  className="min-w-0 truncate text-right font-medium text-ink"
                  title={defaultModel}
                >
                  {defaultModel}
                </dd>
              </div>
              <div className="flex items-start justify-between gap-3">
                <dt className="text-muted">Access</dt>
                <dd className="font-medium text-ink">Ask before commands</dd>
              </div>
              <div className="flex items-start justify-between gap-3">
                <dt className="text-muted">Threads</dt>
                <dd className="font-medium text-ink">{threadCount}</dd>
              </div>
            </dl>
          </div>

          <div className="mt-3 rounded-panel border border-line bg-surface-muted/50 p-3 text-meta leading-5 text-muted">
            <div className="flex items-center gap-1.5 font-medium text-ink">
              <ShieldCheck className="size-3.5 text-success" />
              Commands ask first
            </div>
            <p className="mt-1">
              Codjent pauses before running a shell command in this workspace.
            </p>
          </div>
        </aside>
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
  const announcement = selectedLive
    ? selectedLive.reconnecting
      ? "Reconnecting to the run"
      : selectedLive.status === "waiting_approval"
        ? "Approval needed"
        : selectedLive.status === "failed"
          ? "The run failed"
          : selectedLive.status === "canceled"
            ? "The run was canceled"
            : selectedLive.status === "completed"
              ? "The run finished"
              : "Codjent is working"
    : "";

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

  const jumpToApproval = () => {
    const approval = document.querySelector<HTMLElement>("[data-approval]");
    if (!approval) {
      return;
    }
    const reduced = window.matchMedia(
      "(prefers-reduced-motion: reduce)",
    ).matches;
    approval.scrollIntoView({
      behavior: reduced ? "auto" : "smooth",
      block: "center",
    });
    approval.focus({ preventScroll: true });
  };

  const renderContent = () => {
    if (codjent.booting) {
      return <LoadingScreen />;
    }
    if (codjent.providers.length === 0) {
      if (codjent.error) {
        return (
          <ConnectionError message={codjent.error} onRetry={codjent.retry} />
        );
      }
      return <ProviderSetup />;
    }
    if (codjent.selectedID) {
      const detail = codjent.detail;
      if (!detail || detail.thread.id !== codjent.selectedID) {
        if (codjent.loadingThread) {
          return (
            <div className="flex flex-1 items-center justify-center gap-2 text-body-sm text-muted">
              <Spinner className="size-3.5" />
              Loading thread
            </div>
          );
        }
        return (
          <div className="flex flex-1 items-center justify-center px-6">
            <div className="text-center">
              <p className="text-body font-medium text-ink">
                This thread could not be loaded.
              </p>
              <p className="mt-1 max-w-sm text-body-sm leading-5 text-muted">
                It may have been deleted. Start a new thread to continue.
              </p>
              <Button
                className="mt-4"
                onClick={codjent.startNewThread}
                variant="primary"
              >
                New thread
              </Button>
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
        threads={codjent.threads}
        workspaceName={workspaceName}
      />
    );
  };

  return (
    <div className="flex h-dvh w-full overflow-hidden bg-app text-ink">
      <a
        className="sr-only focus:not-sr-only focus:absolute focus:left-3 focus:top-3 focus:z-50 focus:rounded-field focus:border focus:border-line focus:bg-surface focus:px-3 focus:py-2 focus:text-body-sm focus:shadow-overlay"
        href="#composer"
      >
        Skip to composer
      </a>
      <output aria-live="polite" className="sr-only">
        {announcement}
      </output>
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
        providers={codjent.providers}
        selectedID={codjent.selectedID}
        threads={codjent.threads}
        workspaceName={workspaceName}
      />

      <main className="flex min-w-0 flex-1 flex-col">
        <Header
          live={selectedLive}
          onJumpToApproval={jumpToApproval}
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
