"use client";

import { Dialog } from "@base-ui/react/dialog";
import {
  Loader2,
  LockKeyhole,
  Settings2,
  Trash2,
  Unlock,
  X,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type {
  AccessMode,
  ProviderInfo,
  Thread,
  UpdateThreadInput,
} from "@/lib/types";
import { cn } from "@/lib/utils";

interface SettingsDialogProps {
  open: boolean;
  thread: Thread | null;
  providers: ProviderInfo[];
  workspaceName: string;
  busy: boolean;
  onOpenChange: (open: boolean) => void;
  onSave: (input: UpdateThreadInput) => Promise<Thread | null>;
  onDelete: () => Promise<boolean>;
}

const fieldClass =
  "h-9 w-full rounded-lg border border-line bg-surface px-2.5 text-[12px] text-ink outline-none transition focus:border-line-strong focus:ring-2 focus:ring-accent/10";

export function SettingsDialog({
  open,
  thread,
  providers,
  workspaceName,
  busy,
  onOpenChange,
  onSave,
  onDelete,
}: SettingsDialogProps) {
  const [title, setTitle] = useState("");
  const [providerID, setProviderID] = useState("");
  const [modelID, setModelID] = useState("");
  const [accessMode, setAccessMode] = useState<AccessMode>("ask");
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const threadRef = useRef(thread);

  useEffect(() => {
    threadRef.current = thread;
  }, [thread]);

  useEffect(() => {
    if (!open) {
      return;
    }
    const current = threadRef.current;
    if (!current) {
      return;
    }
    setTitle(current.title);
    setProviderID(current.provider);
    setModelID(current.model);
    setAccessMode(current.access_mode);
    setSaving(false);
    setDeleting(false);
    setConfirmDelete(false);
    setError(null);
  }, [open]);

  const provider =
    providers.find((item) => item.id === providerID) ?? providers[0];

  const changeProvider = (nextProviderID: string) => {
    setProviderID(nextProviderID);
    const next = providers.find((item) => item.id === nextProviderID);
    if (next) {
      setModelID(next.default_model);
    }
  };

  const save = async () => {
    if (!thread || !title.trim()) {
      return;
    }
    setSaving(true);
    setError(null);
    const updated = await onSave({
      title: title.trim(),
      provider: providerID,
      model: modelID,
      access_mode: accessMode,
    });
    setSaving(false);
    if (updated) {
      onOpenChange(false);
    } else {
      setError(
        "Could not save these settings. Check the connection and retry.",
      );
    }
  };

  const remove = async () => {
    if (!thread) {
      return;
    }
    setDeleting(true);
    setError(null);
    const deleted = await onDelete();
    setDeleting(false);
    if (deleted) {
      onOpenChange(false);
    } else {
      setError("Could not delete this thread. Check the connection and retry.");
    }
  };

  const disabled = busy || saving || deleting;

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/25 backdrop-blur-[2px] transition-opacity duration-150 data-ending-style:opacity-0 data-starting-style:opacity-0" />
        <Dialog.Popup className="fixed left-1/2 top-1/2 z-50 w-[min(520px,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2 overflow-hidden rounded-2xl border border-line bg-surface text-ink shadow-[0_24px_80px_-24px_rgba(28,25,23,0.45)] outline-none transition duration-150 data-ending-style:scale-[0.98] data-ending-style:opacity-0 data-starting-style:scale-[0.98] data-starting-style:opacity-0">
          <div className="flex items-start justify-between border-b border-line px-5 py-4">
            <div>
              <Dialog.Title className="flex items-center gap-2 text-[14px] font-semibold">
                <Settings2 className="size-4 text-muted" />
                Thread settings
              </Dialog.Title>
              <Dialog.Description className="mt-1 text-[11px] leading-5 text-faint">
                Model and command permissions are stored with this thread.
              </Dialog.Description>
            </div>
            <Dialog.Close
              aria-label="Close settings"
              className="grid size-7 place-items-center rounded-lg text-muted transition hover:bg-surface-muted hover:text-ink"
            >
              <X className="size-4" />
            </Dialog.Close>
          </div>

          {!thread ? null : (
            <div className="space-y-5 px-5 py-5">
              {busy ? (
                <div className="rounded-lg border border-warning/25 bg-warning-soft px-3 py-2 text-[11px] leading-5 text-warning">
                  This thread is working. Finish or stop the run before changing
                  its settings.
                </div>
              ) : null}

              <label className="block">
                <span className="mb-1.5 block text-[10px] font-medium uppercase tracking-[0.12em] text-faint">
                  Title
                </span>
                <input
                  className={fieldClass}
                  disabled={disabled}
                  onChange={(event) => setTitle(event.target.value)}
                  value={title}
                />
              </label>

              <div className="grid grid-cols-2 gap-3">
                <label className="block">
                  <span className="mb-1.5 block text-[10px] font-medium uppercase tracking-[0.12em] text-faint">
                    Provider
                  </span>
                  <select
                    className={fieldClass}
                    disabled={disabled}
                    onChange={(event) => changeProvider(event.target.value)}
                    value={providerID}
                  >
                    {providers.map((item) => (
                      <option key={item.id} value={item.id}>
                        {item.name}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="block">
                  <span className="mb-1.5 block text-[10px] font-medium uppercase tracking-[0.12em] text-faint">
                    Model
                  </span>
                  <select
                    className={fieldClass}
                    disabled={disabled}
                    onChange={(event) => setModelID(event.target.value)}
                    value={modelID}
                  >
                    {provider?.models.map((model) => (
                      <option key={model.id} value={model.id}>
                        {model.name}
                      </option>
                    ))}
                  </select>
                </label>
              </div>

              <div>
                <span className="mb-1.5 block text-[10px] font-medium uppercase tracking-[0.12em] text-faint">
                  Command access
                </span>
                <div className="grid gap-2">
                  <button
                    type="button"
                    aria-pressed={accessMode === "ask"}
                    className={cn(
                      "flex items-start gap-3 rounded-xl border px-3 py-2.5 text-left transition",
                      accessMode === "ask"
                        ? "border-line-strong bg-surface-muted"
                        : "border-line hover:bg-surface-muted/60",
                    )}
                    disabled={disabled}
                    onClick={() => setAccessMode("ask")}
                  >
                    <span className="mt-0.5 grid size-7 shrink-0 place-items-center rounded-lg bg-surface text-muted">
                      <LockKeyhole className="size-3.5" />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block text-[12px] font-medium">
                        Ask before commands
                      </span>
                      <span className="mt-0.5 block text-[10.5px] leading-4 text-faint">
                        File edits run freely. Shell commands wait for your
                        approval.
                      </span>
                    </span>
                    {accessMode === "ask" ? (
                      <span className="mt-1 size-2 rounded-full bg-accent" />
                    ) : null}
                  </button>
                  <button
                    type="button"
                    aria-pressed={accessMode === "full"}
                    className={cn(
                      "flex items-start gap-3 rounded-xl border px-3 py-2.5 text-left transition",
                      accessMode === "full"
                        ? "border-danger/30 bg-danger-soft"
                        : "border-line hover:bg-surface-muted/60",
                    )}
                    disabled={disabled}
                    onClick={() => setAccessMode("full")}
                  >
                    <span className="mt-0.5 grid size-7 shrink-0 place-items-center rounded-lg bg-danger-soft text-danger">
                      <Unlock className="size-3.5" />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block text-[12px] font-medium text-danger">
                        Full access
                      </span>
                      <span className="mt-0.5 block text-[10.5px] leading-4 text-faint">
                        Commands run without approval in this workspace.
                      </span>
                    </span>
                    {accessMode === "full" ? (
                      <span className="mt-1 size-2 rounded-full bg-danger" />
                    ) : null}
                  </button>
                </div>
              </div>

              <div className="flex items-center justify-between rounded-lg border border-line bg-surface-muted/60 px-3 py-2 text-[10.5px] text-faint">
                <span className="truncate">Workspace</span>
                <span className="max-w-[62%] truncate font-mono text-[10px] text-muted">
                  {workspaceName}
                </span>
              </div>
            </div>
          )}

          {error ? (
            <div className="border-t border-danger/20 bg-danger-soft px-5 py-2.5 text-[11px] leading-5 text-danger">
              {error}
            </div>
          ) : null}

          {confirmDelete ? (
            <div className="border-t border-danger/20 bg-danger-soft px-5 py-3">
              <div className="text-[11.5px] font-semibold text-danger">
                Delete this thread?
              </div>
              <p className="mt-0.5 text-[10.5px] leading-4 text-danger/80">
                Its transcript will be removed. This cannot be undone.
              </p>
              <div className="mt-2.5 flex items-center gap-2">
                <button
                  type="button"
                  className="inline-flex items-center gap-1.5 rounded-lg bg-danger px-3 py-1.5 text-[11px] font-medium text-white transition hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
                  disabled={deleting}
                  onClick={() => void remove()}
                >
                  {deleting ? (
                    <Loader2 className="size-3.5 animate-spin" />
                  ) : (
                    <Trash2 className="size-3.5" />
                  )}
                  Delete thread
                </button>
                <button
                  type="button"
                  className="rounded-lg border border-line-strong bg-surface px-3 py-1.5 text-[11px] font-medium text-ink transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
                  disabled={deleting}
                  onClick={() => setConfirmDelete(false)}
                >
                  Keep thread
                </button>
              </div>
            </div>
          ) : null}

          <div className="flex items-center justify-between gap-2 border-t border-line bg-surface-muted/40 px-5 py-3">
            <button
              type="button"
              className="inline-flex items-center gap-1.5 rounded-lg px-2 py-1.5 text-[11px] font-medium text-danger transition hover:bg-danger-soft disabled:cursor-not-allowed disabled:opacity-50"
              disabled={disabled || confirmDelete}
              onClick={() => {
                setConfirmDelete(true);
                setError(null);
              }}
            >
              <Trash2 className="size-3.5" />
              Delete thread
            </button>
            <div className="flex items-center gap-2">
              <button
                type="button"
                className="rounded-lg border border-line-strong bg-surface px-3 py-1.5 text-[11px] font-medium text-ink transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
                disabled={saving || deleting}
                onClick={() => onOpenChange(false)}
              >
                Cancel
              </button>
              <button
                type="button"
                className="inline-flex items-center gap-1.5 rounded-lg bg-ink px-3 py-1.5 text-[11px] font-medium text-app transition hover:opacity-85 disabled:cursor-not-allowed disabled:opacity-50"
                disabled={
                  disabled || confirmDelete || !title.trim() || !providerID
                }
                onClick={() => void save()}
              >
                {saving ? <Loader2 className="size-3.5 animate-spin" /> : null}
                Save changes
              </button>
            </div>
          </div>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
