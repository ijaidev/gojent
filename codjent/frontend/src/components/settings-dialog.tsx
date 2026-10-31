"use client";

import { Dialog } from "@base-ui/react/dialog";
import { LockKeyhole, Settings2, Trash2, Unlock, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { fieldClass, labelClass } from "@/components/ui/form";
import type {
  AccessMode,
  ProviderInfo,
  Thread,
  UpdateThreadInput,
} from "@/lib/types";
import { basename, cn } from "@/lib/utils";

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
        <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/30 transition-opacity duration-base data-ending-style:opacity-0 data-starting-style:opacity-0" />
        <Dialog.Popup className="fixed left-1/2 top-1/2 z-50 w-[min(560px,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2 overflow-hidden overscroll-contain rounded-dialog border border-line bg-surface text-ink shadow-overlay outline-none transition duration-base data-ending-style:scale-[0.98] data-ending-style:opacity-0 data-starting-style:scale-[0.98] data-starting-style:opacity-0">
          <div className="flex items-start justify-between border-b border-line px-5 py-4">
            <div>
              <Dialog.Title className="flex items-center gap-2 text-title font-semibold">
                <Settings2 className="size-4 text-muted" />
                Thread settings
              </Dialog.Title>
              <Dialog.Description className="mt-1 text-body-sm leading-5 text-muted">
                Choose the model and how much Codjent can do without asking.
              </Dialog.Description>
            </div>
            <Dialog.Close
              aria-label="Close settings"
              className="grid size-11 shrink-0 place-items-center rounded-field text-muted sm:size-8 transition-colors duration-fast hover:bg-surface-muted hover:text-ink"
            >
              <X className="size-4" />
            </Dialog.Close>
          </div>

          {!thread ? null : (
            <div className="space-y-4 px-5 py-5">
              {busy ? (
                <div className="rounded-field border border-warning/25 bg-warning-soft px-3 py-2 text-body-sm leading-5 text-warning">
                  This thread is working. Finish or stop the run before changing
                  its settings.
                </div>
              ) : null}

              <label className="block">
                <span className={labelClass}>Title</span>
                <input
                  className={fieldClass}
                  disabled={disabled}
                  onChange={(event) => setTitle(event.target.value)}
                  value={title}
                />
              </label>

              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <label className="block">
                  <span className={labelClass}>Provider</span>
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
                  <span className={labelClass}>Model</span>
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

              <fieldset className="m-0 min-w-0 border-0 p-0">
                <legend className={labelClass}>Command access</legend>
                <div className="grid gap-2">
                  <label
                    className={cn(
                      "flex cursor-pointer items-start gap-3 rounded-panel border px-3 py-2.5 transition-colors duration-fast",
                      accessMode === "ask"
                        ? "border-line-strong bg-surface-muted"
                        : "border-line hover:bg-surface-muted/60",
                      disabled && "cursor-not-allowed opacity-60",
                    )}
                  >
                    <input
                      checked={accessMode === "ask"}
                      className="sr-only"
                      disabled={disabled}
                      name="access-mode"
                      onChange={() => setAccessMode("ask")}
                      type="radio"
                      value="ask"
                    />
                    <span className="mt-0.5 grid size-7 shrink-0 place-items-center rounded-field bg-surface text-muted">
                      <LockKeyhole className="size-3.5" />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block text-body-sm font-medium">
                        Ask before commands
                      </span>
                      <span className="mt-0.5 block text-meta leading-4 text-faint">
                        File edits run freely. Shell commands wait for your
                        approval.
                      </span>
                    </span>
                    <span
                      aria-hidden="true"
                      className={cn(
                        "mt-1 size-2 rounded-full",
                        accessMode === "ask" ? "bg-accent" : "bg-transparent",
                      )}
                    />
                  </label>

                  <label
                    className={cn(
                      "flex cursor-pointer items-start gap-3 rounded-panel border px-3 py-2.5 transition-colors duration-fast",
                      accessMode === "full"
                        ? "border-danger/30 bg-danger-soft"
                        : "border-line hover:bg-surface-muted/60",
                      disabled && "cursor-not-allowed opacity-60",
                    )}
                  >
                    <input
                      checked={accessMode === "full"}
                      className="sr-only"
                      disabled={disabled}
                      name="access-mode"
                      onChange={() => setAccessMode("full")}
                      type="radio"
                      value="full"
                    />
                    <span className="mt-0.5 grid size-7 shrink-0 place-items-center rounded-field bg-danger-soft text-danger">
                      <Unlock className="size-3.5" />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block text-body-sm font-medium text-danger">
                        Full access
                      </span>
                      <span className="mt-0.5 block text-meta leading-4 text-faint">
                        Commands run without approval in this thread.
                      </span>
                    </span>
                    <span
                      aria-hidden="true"
                      className={cn(
                        "mt-1 size-2 rounded-full",
                        accessMode === "full" ? "bg-danger" : "bg-transparent",
                      )}
                    />
                  </label>
                </div>
              </fieldset>

              <div className="flex items-center justify-between gap-3 rounded-field border border-line bg-surface-muted/60 px-3 py-2 text-meta text-faint">
                <span className="shrink-0">Workspace</span>
                <span
                  className="max-w-[62%] truncate font-mono text-meta text-muted"
                  title={workspaceName}
                >
                  {basename(workspaceName)}
                </span>
              </div>
            </div>
          )}

          {error ? (
            <div
              className="border-t border-danger/20 bg-danger-soft px-5 py-2.5 text-body-sm leading-5 text-danger"
              role="alert"
            >
              {error}
            </div>
          ) : null}

          {confirmDelete ? (
            <div className="border-t border-danger/20 bg-danger-soft px-5 py-3">
              <div className="text-body-sm font-semibold text-danger">
                Delete this thread?
              </div>
              <p className="mt-0.5 text-meta leading-4 text-danger">
                Its transcript will be removed. This cannot be undone.
              </p>
              <div className="mt-2.5 flex items-center gap-2">
                <Button
                  className="h-11 sm:h-8"
                  loading={deleting}
                  onClick={() => void remove()}
                  size="sm"
                  variant="danger"
                >
                  Delete thread
                </Button>
                <Button
                  className="h-11 sm:h-8"
                  disabled={deleting}
                  onClick={() => setConfirmDelete(false)}
                  size="sm"
                  variant="secondary"
                >
                  Keep thread
                </Button>
              </div>
            </div>
          ) : null}

          <div className="flex items-center justify-between gap-2 border-t border-line bg-surface-muted/40 px-5 py-3">
            <Button
              className="h-11 text-danger hover:bg-danger-soft hover:text-danger sm:h-8"
              disabled={disabled || confirmDelete}
              icon={Trash2}
              onClick={() => {
                setConfirmDelete(true);
                setError(null);
              }}
              size="sm"
              variant="ghost"
            >
              Delete thread
            </Button>
            <div className="flex items-center gap-2">
              <Button
                className="h-11 sm:h-8"
                disabled={saving || deleting}
                onClick={() => onOpenChange(false)}
                size="sm"
                variant="secondary"
              >
                Cancel
              </Button>
              <Button
                className="h-11 sm:h-8"
                disabled={
                  disabled || confirmDelete || !title.trim() || !providerID
                }
                loading={saving}
                onClick={() => void save()}
                size="sm"
                variant="primary"
              >
                Save changes
              </Button>
            </div>
          </div>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
