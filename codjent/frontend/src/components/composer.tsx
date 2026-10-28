"use client";

import { Menu } from "@base-ui/react/menu";
import {
  ArrowUp,
  Check,
  ChevronDown,
  CircleAlert,
  Folder,
  ImagePlus,
  LockKeyhole,
  Sparkles,
  Square,
  Unlock,
  X,
} from "lucide-react";
import {
  type ClipboardEvent,
  type DragEvent,
  type KeyboardEvent,
  useEffect,
  useRef,
  useState,
} from "react";
import { ImageLightbox } from "@/components/image-lightbox";
import { Spinner } from "@/components/spinner";
import type { SendMessage, SendOptions } from "@/hooks/use-codjent";
import {
  formatBytes,
  MAX_ATTACHMENTS,
  MAX_IMAGE_BYTES,
  MAX_MESSAGE_BYTES,
  type PendingAttachment,
  prepareAttachment,
  toAttachmentInput,
  totalAttachmentBytes,
} from "@/lib/attachments";
import type {
  AccessMode,
  ProviderInfo,
  RunStatus,
  Thread,
  UpdateThreadInput,
} from "@/lib/types";
import { cn, errorMessage, statusCopy } from "@/lib/utils";

interface ComposerProps {
  thread: Thread | null;
  providers: ProviderInfo[];
  workspaceName: string;
  busy: boolean;
  sending: boolean;
  liveStatus?: RunStatus;
  large?: boolean;
  autoFocus?: boolean;
  onSend: SendMessage;
  onStop: () => void;
  onUpdateThread: (input: UpdateThreadInput) => Promise<Thread | null>;
}

const menuItemClass =
  "flex cursor-default select-none items-start gap-2.5 rounded-lg px-2.5 py-2 text-left outline-none data-highlighted:bg-surface-muted";

function modelName(
  providers: ProviderInfo[],
  providerID: string,
  modelID: string,
) {
  const provider = providers.find((item) => item.id === providerID);
  return (
    provider?.models.find((model) => model.id === modelID)?.name ?? modelID
  );
}

export function Composer({
  thread,
  providers,
  workspaceName,
  busy,
  sending,
  liveStatus,
  large = false,
  autoFocus = false,
  onSend,
  onStop,
  onUpdateThread,
}: ComposerProps) {
  const [value, setValue] = useState("");
  const [updating, setUpdating] = useState(false);
  const [draftProvider, setDraftProvider] = useState(providers[0]?.id ?? "");
  const [draftModel, setDraftModel] = useState(
    providers[0]?.default_model ?? "",
  );
  const [draftAccess, setDraftAccess] = useState<AccessMode>("ask");
  const [attachments, setAttachments] = useState<PendingAttachment[]>([]);
  const [attachmentError, setAttachmentError] = useState<string | null>(null);
  const [preparing, setPreparing] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [previewIndex, setPreviewIndex] = useState<number | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const preparingRef = useRef(false);

  useEffect(() => {
    if (thread || draftProvider) {
      return;
    }
    const provider = providers[0];
    if (provider) {
      setDraftProvider(provider.id);
      setDraftModel(provider.default_model);
    }
  }, [draftProvider, providers, thread]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: the controlled value drives the textarea height
  useEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea) {
      return;
    }
    textarea.style.height = "0px";
    textarea.style.height = `${Math.min(textarea.scrollHeight, 220)}px`;
  }, [value]);

  useEffect(() => {
    if (!autoFocus || busy) {
      return;
    }
    textareaRef.current?.focus();
  }, [autoFocus, busy]);

  const providerID = thread?.provider ?? draftProvider;
  const modelID = thread?.model ?? draftModel;
  const access = thread?.access_mode ?? draftAccess;
  const activeProvider =
    providers.find((item) => item.id === providerID) ?? providers[0];
  const controlsDisabled = busy || sending || updating;

  const addFiles = async (files: FileList | File[]) => {
    const selected = Array.from(files);
    if (selected.length === 0 || preparingRef.current || controlsDisabled) {
      return;
    }

    preparingRef.current = true;
    setPreparing(true);
    setAttachmentError(null);

    const next = [...attachments];
    let total = totalAttachmentBytes(next);
    try {
      for (const file of selected) {
        if (next.length >= MAX_ATTACHMENTS) {
          setAttachmentError(
            `You can attach up to ${MAX_ATTACHMENTS} images per message.`,
          );
          break;
        }
        try {
          const prepared = await prepareAttachment(file);
          if (total + prepared.size > MAX_MESSAGE_BYTES) {
            setAttachmentError(
              `Images must total ${formatBytes(MAX_MESSAGE_BYTES)} or less per message.`,
            );
            break;
          }
          next.push(prepared);
          total += prepared.size;
        } catch (error) {
          setAttachmentError(errorMessage(error));
        }
      }
      setAttachments(next);
    } finally {
      preparingRef.current = false;
      setPreparing(false);
    }
  };

  const removeAttachment = (id: string) => {
    setAttachments((current) =>
      current.filter((attachment) => attachment.id !== id),
    );
    setAttachmentError(null);
  };

  const handlePaste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const files = Array.from(event.clipboardData.items)
      .filter((item) => item.kind === "file")
      .map((item) => item.getAsFile())
      .filter((file): file is File => file !== null);
    if (files.length === 0) {
      return;
    }
    // Keep the browser's normal text paste when the clipboard also has text.
    void addFiles(files);
  };

  const handleDragEnter = (event: DragEvent<HTMLElement>) => {
    if (!Array.from(event.dataTransfer.types).includes("Files")) {
      return;
    }
    event.preventDefault();
    setDragging(true);
  };

  const handleDragOver = (event: DragEvent<HTMLElement>) => {
    if (!Array.from(event.dataTransfer.types).includes("Files")) {
      return;
    }
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
  };

  const handleDragLeave = (event: DragEvent<HTMLElement>) => {
    if (event.currentTarget.contains(event.relatedTarget as Node | null)) {
      return;
    }
    setDragging(false);
  };

  const handleDrop = (event: DragEvent<HTMLElement>) => {
    event.preventDefault();
    setDragging(false);
    if (controlsDisabled || preparingRef.current) {
      return;
    }
    void addFiles(event.dataTransfer.files);
  };

  const chooseModel = async (nextProvider: string, nextModel: string) => {
    if (thread) {
      setUpdating(true);
      await onUpdateThread({
        provider: nextProvider,
        model: nextModel,
      });
      setUpdating(false);
      return;
    }
    setDraftProvider(nextProvider);
    setDraftModel(nextModel);
  };

  const chooseAccess = async (nextAccess: AccessMode) => {
    if (thread) {
      if (nextAccess === thread.access_mode) {
        return;
      }
      setUpdating(true);
      await onUpdateThread({ access_mode: nextAccess });
      setUpdating(false);
      return;
    }
    setDraftAccess(nextAccess);
  };

  const submit = async () => {
    const content = value.trim();
    if (
      (!content && attachments.length === 0) ||
      busy ||
      sending ||
      preparing
    ) {
      return;
    }
    const options: SendOptions | undefined = thread
      ? undefined
      : {
          provider: providerID,
          model: modelID,
          access_mode: access,
        };
    const sent = await onSend(
      content,
      options,
      attachments.map(toAttachmentInput),
    );
    if (sent) {
      setValue("");
      setAttachments([]);
      setAttachmentError(null);
      textareaRef.current?.focus();
    }
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (
      event.key === "Enter" &&
      !event.shiftKey &&
      !event.nativeEvent.isComposing
    ) {
      event.preventDefault();
      void submit();
    }
  };

  const attachmentImages = attachments.map((attachment) => ({
    url: attachment.previewURL,
    filename: attachment.filename,
  }));
  const attachmentBytes = totalAttachmentBytes(attachments);

  const placeholder = busy
    ? liveStatus === "waiting_approval"
      ? "Resolve the approval above to continue…"
      : `Codjent is ${statusCopy(liveStatus ?? "running").toLowerCase()}…`
    : large
      ? "Ask codjent to build, debug, or explain something…"
      : "Ask a follow-up, or paste a screenshot…";

  const stop = () => {
    if (busy) {
      onStop();
    }
  };

  return (
    <div
      className={cn(
        "mx-auto w-full",
        large ? "max-w-[780px]" : "max-w-[800px]",
      )}
    >
      <fieldset
        aria-busy={preparing}
        aria-label="Message composer"
        className={cn(
          "composer-card relative m-0 min-w-0 rounded-[20px] border border-line bg-surface p-0 transition focus-within:border-line-strong",
          large ? "min-h-[148px]" : "min-h-[104px]",
          dragging && "border-accent",
        )}
        onDragEnter={handleDragEnter}
        onDragLeave={handleDragLeave}
        onDragOver={handleDragOver}
        onDrop={handleDrop}
      >
        <input
          ref={fileInputRef}
          accept="image/png,image/jpeg,image/webp"
          className="hidden"
          multiple
          onChange={(event) => {
            const files = Array.from(event.target.files ?? []);
            event.target.value = "";
            if (files.length > 0) {
              void addFiles(files);
            }
          }}
          type="file"
        />

        {dragging ? (
          <div className="pointer-events-none absolute inset-2 z-20 grid place-items-center rounded-2xl border-2 border-dashed border-accent bg-surface/95">
            <span className="flex flex-col items-center gap-1.5">
              <span className="grid size-10 place-items-center rounded-xl bg-accent-soft text-accent">
                <ImagePlus className="size-5" />
              </span>
              <span className="text-[12.5px] font-semibold text-ink">
                Drop images to attach
              </span>
              <span className="text-[10px] text-muted">
                PNG, JPEG, or WebP · up to {MAX_ATTACHMENTS}
              </span>
            </span>
          </div>
        ) : null}

        {attachments.length > 0 || preparing ? (
          <div className="px-3 pt-3">
            <div className="flex flex-wrap gap-2.5">
              {attachments.map((attachment, index) => (
                <div
                  className="group/preview relative w-[84px] shrink-0"
                  key={attachment.id}
                >
                  <button
                    aria-label={`Preview image ${index + 1}: ${attachment.filename}`}
                    className="relative block size-[84px] cursor-zoom-in overflow-hidden rounded-xl border border-line bg-surface-muted transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:ring-offset-2 focus-visible:ring-offset-surface"
                    onClick={() => setPreviewIndex(index)}
                    type="button"
                  >
                    {/* biome-ignore lint/performance/noImgElement: data URLs are local and cannot be optimized by next/image */}
                    <img
                      alt=""
                      className="h-full w-full object-cover transition duration-150 group-hover/preview:brightness-95 motion-reduce:transition-none"
                      height={84}
                      src={attachment.previewURL}
                      width={84}
                    />
                    <span className="pointer-events-none absolute inset-x-0 bottom-0 truncate bg-gradient-to-t from-black/70 via-black/40 to-transparent px-1.5 pb-1 pt-4 text-[9px] font-medium text-white/95">
                      {attachment.filename}
                    </span>
                    {attachments.length > 1 ? (
                      <span className="absolute left-1.5 top-1.5 grid size-4 place-items-center rounded-full bg-black/65 text-[8px] font-semibold text-white">
                        {index + 1}
                      </span>
                    ) : null}
                  </button>
                  <button
                    aria-label={`Remove ${attachment.filename}`}
                    className="absolute -right-1.5 -top-1.5 z-10 grid size-6 place-items-center rounded-full border border-line bg-surface text-muted shadow-sm transition hover:bg-danger-soft hover:text-danger disabled:opacity-40"
                    disabled={preparing}
                    onClick={() => removeAttachment(attachment.id)}
                    type="button"
                  >
                    <X className="size-3" />
                  </button>
                </div>
              ))}

              {preparing ? (
                <div className="grid size-[84px] shrink-0 animate-pulse place-items-center rounded-xl border border-line bg-surface-muted motion-reduce:animate-none">
                  <Spinner className="size-4 text-faint" />
                </div>
              ) : null}

              {attachments.length < MAX_ATTACHMENTS && !preparing ? (
                <button
                  aria-label="Add images"
                  className="group/add grid size-[84px] shrink-0 place-items-center rounded-xl border border-dashed border-line-strong bg-surface-muted/40 text-muted transition hover:border-accent/40 hover:bg-accent-soft/60 hover:text-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:ring-offset-2 focus-visible:ring-offset-surface"
                  onClick={() => fileInputRef.current?.click()}
                  type="button"
                >
                  <span className="flex flex-col items-center gap-1">
                    <ImagePlus className="size-4" />
                    <span className="text-[9px] font-medium">Add</span>
                  </span>
                </button>
              ) : null}
            </div>

            <output
              aria-live="polite"
              className="mt-1.5 flex items-center justify-between gap-3 text-[9.5px] text-faint"
            >
              <span>
                {preparing
                  ? "Preparing image…"
                  : `${attachments.length} of ${MAX_ATTACHMENTS} images · ${formatBytes(attachmentBytes)}`}
              </span>
              <span className="hidden sm:inline">
                Click an image to preview
              </span>
            </output>
          </div>
        ) : null}

        {attachmentError ? (
          <div
            className="mx-3 mt-2 flex items-start gap-2 rounded-lg border border-danger/20 bg-danger-soft px-2.5 py-2 text-danger"
            id="composer-attachment-error"
            role="alert"
          >
            <CircleAlert className="mt-0.5 size-3.5 shrink-0" />
            <p className="min-w-0 flex-1 text-[10.5px] leading-4">
              {attachmentError}
            </p>
          </div>
        ) : null}

        <textarea
          ref={textareaRef}
          aria-describedby={
            attachmentError ? "composer-attachment-error" : undefined
          }
          aria-label="Message codjent"
          className={cn(
            "block w-full resize-none bg-transparent px-4 pt-4 text-[14.5px] leading-6 text-ink outline-none placeholder:text-faint focus-visible:outline-none disabled:cursor-not-allowed",
            large ? "min-h-[82px]" : "min-h-[54px]",
          )}
          disabled={busy || sending}
          onChange={(event) => setValue(event.target.value)}
          onKeyDown={handleKeyDown}
          onPaste={handlePaste}
          placeholder={placeholder}
          rows={1}
          value={value}
        />

        <div className="flex items-center gap-1.5 px-2.5 pb-2.5">
          <button
            type="button"
            aria-label="Attach images"
            className="grid size-8 shrink-0 place-items-center rounded-lg text-muted transition hover:bg-surface-muted hover:text-ink disabled:cursor-not-allowed disabled:opacity-40"
            disabled={
              controlsDisabled ||
              preparing ||
              attachments.length >= MAX_ATTACHMENTS
            }
            onClick={() => fileInputRef.current?.click()}
            title={
              attachments.length >= MAX_ATTACHMENTS
                ? `Up to ${MAX_ATTACHMENTS} images per message`
                : `Attach images (PNG, JPEG, WebP; up to ${MAX_ATTACHMENTS}, ${formatBytes(MAX_IMAGE_BYTES)} each)`
            }
          >
            {preparing ? (
              <Spinner className="size-3.5" />
            ) : (
              <ImagePlus className="size-4" />
            )}
          </button>

          <Menu.Root>
            <Menu.Trigger
              className="inline-flex min-w-0 items-center gap-1.5 rounded-lg px-2 py-1.5 text-[11.5px] font-medium text-muted outline-none transition hover:bg-surface-muted hover:text-ink disabled:cursor-not-allowed disabled:opacity-50"
              disabled={controlsDisabled || !activeProvider}
            >
              <Sparkles className="size-3.5 shrink-0 text-accent" />
              <span className="max-w-[150px] truncate">
                {modelName(providers, providerID, modelID)}
              </span>
              {updating ? (
                <Spinner className="size-3" />
              ) : (
                <ChevronDown className="size-3" />
              )}
            </Menu.Trigger>
            <Menu.Portal>
              <Menu.Positioner align="start" className="z-50" sideOffset={6}>
                <Menu.Popup className="menu-popup min-w-[260px]">
                  {providers.map((provider, providerIndex) => (
                    <div key={provider.id}>
                      {providerIndex > 0 ? <Menu.Separator /> : null}
                      <div className="px-2.5 pb-1 pt-2 text-[9.5px] font-medium uppercase tracking-[0.14em] text-faint">
                        {provider.name}
                      </div>
                      {provider.models.map((model) => {
                        const selected =
                          provider.id === providerID && model.id === modelID;
                        return (
                          <Menu.Item
                            className={menuItemClass}
                            key={`${provider.id}/${model.id}`}
                            onClick={() =>
                              void chooseModel(provider.id, model.id)
                            }
                          >
                            <span className="mt-1 grid size-3.5 shrink-0 place-items-center">
                              {selected ? (
                                <Check className="size-3.5 text-accent" />
                              ) : null}
                            </span>
                            <span className="min-w-0">
                              <span className="block truncate text-[12px] font-medium text-ink">
                                {model.name}
                              </span>
                              <span className="block truncate font-mono text-[10px] text-faint">
                                {model.id}
                              </span>
                            </span>
                          </Menu.Item>
                        );
                      })}
                    </div>
                  ))}
                </Menu.Popup>
              </Menu.Positioner>
            </Menu.Portal>
          </Menu.Root>

          <span className="text-line-strong">/</span>

          <Menu.Root>
            <Menu.Trigger
              className={cn(
                "inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-lg px-2 py-1.5 text-[11.5px] font-medium outline-none transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50",
                access === "full" ? "text-danger" : "text-muted hover:text-ink",
              )}
              disabled={controlsDisabled}
            >
              {access === "full" ? (
                <Unlock className="size-3.5" />
              ) : (
                <LockKeyhole className="size-3.5" />
              )}
              {access === "full" ? "Full access" : "Ask"}
              <ChevronDown className="size-3" />
            </Menu.Trigger>
            <Menu.Portal>
              <Menu.Positioner align="start" className="z-50" sideOffset={6}>
                <Menu.Popup className="menu-popup w-[292px]">
                  <Menu.Item
                    className={menuItemClass}
                    onClick={() => void chooseAccess("ask")}
                  >
                    <span className="mt-0.5 grid size-7 shrink-0 place-items-center rounded-lg bg-surface-muted text-muted">
                      <LockKeyhole className="size-3.5" />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="flex items-center gap-1.5 text-[12px] font-medium text-ink">
                        Ask before commands
                        {access === "ask" ? (
                          <Check className="size-3 text-accent" />
                        ) : null}
                      </span>
                      <span className="mt-0.5 block text-[10.5px] leading-4 text-faint">
                        File edits run freely. Shell commands wait for you.
                      </span>
                    </span>
                  </Menu.Item>
                  <Menu.Item
                    className={menuItemClass}
                    onClick={() => void chooseAccess("full")}
                  >
                    <span className="mt-0.5 grid size-7 shrink-0 place-items-center rounded-lg bg-danger-soft text-danger">
                      <Unlock className="size-3.5" />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="flex items-center gap-1.5 text-[12px] font-medium text-danger">
                        Full access
                        {access === "full" ? (
                          <Check className="size-3 text-danger" />
                        ) : null}
                      </span>
                      <span className="mt-0.5 block text-[10.5px] leading-4 text-faint">
                        Commands run without approval in this workspace.
                      </span>
                    </span>
                  </Menu.Item>
                </Menu.Popup>
              </Menu.Positioner>
            </Menu.Portal>
          </Menu.Root>

          <div className="ml-auto flex items-center gap-1.5">
            {busy ? (
              <button
                type="button"
                aria-label="Stop run"
                className="grid size-8 place-items-center rounded-full bg-danger text-white transition-opacity hover:opacity-90"
                title="Stop run"
                onClick={stop}
              >
                <Square className="size-3 fill-current" />
              </button>
            ) : (
              <button
                type="button"
                aria-label="Send message"
                className="grid size-8 place-items-center rounded-full bg-ink text-app transition-opacity hover:opacity-85 disabled:cursor-not-allowed disabled:bg-line-strong disabled:text-faint"
                disabled={
                  (!value.trim() && attachments.length === 0) ||
                  sending ||
                  preparing
                }
                onClick={() => void submit()}
              >
                {sending ? (
                  <Spinner className="size-3.5" label="Sending" />
                ) : (
                  <ArrowUp className="size-4" strokeWidth={2.25} />
                )}
              </button>
            )}
          </div>
        </div>
      </fieldset>

      <div className="mt-2 flex items-center justify-between gap-3 px-2 text-[10px] text-faint">
        <span className="inline-flex min-w-0 items-center gap-1.5">
          <Folder className="size-3 shrink-0" />
          <span className="truncate">{workspaceName || "Workspace"}</span>
          <span className="text-line-strong">·</span>
          <span className="shrink-0">Local checkout</span>
        </span>
        <span className="shrink-0">
          {access === "full" ? (
            <span className="text-danger">Full access enabled</span>
          ) : (
            "Commands ask first"
          )}
        </span>
      </div>

      <ImageLightbox
        images={attachmentImages}
        index={previewIndex}
        onIndexChange={setPreviewIndex}
      />
    </div>
  );
}
