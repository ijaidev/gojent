"use client";

import { Menu } from "@base-ui/react/menu";
import {
  ArrowUp,
  Check,
  ChevronDown,
  Folder,
  LockKeyhole,
  Sparkles,
  Square,
  Unlock,
} from "lucide-react";
import { type KeyboardEvent, useEffect, useRef, useState } from "react";
import { Spinner } from "@/components/spinner";
import type { SendOptions } from "@/hooks/use-codjent";
import type {
  AccessMode,
  ProviderInfo,
  RunStatus,
  Thread,
  UpdateThreadInput,
} from "@/lib/types";
import { cn, statusCopy } from "@/lib/utils";

interface ComposerProps {
  thread: Thread | null;
  providers: ProviderInfo[];
  workspaceName: string;
  busy: boolean;
  sending: boolean;
  liveStatus?: RunStatus;
  large?: boolean;
  autoFocus?: boolean;
  onSend: (content: string, options?: SendOptions) => Promise<boolean>;
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
  const textareaRef = useRef<HTMLTextAreaElement>(null);

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
    if (!content || busy || sending) {
      return;
    }
    const options: SendOptions | undefined = thread
      ? undefined
      : {
          provider: providerID,
          model: modelID,
          access_mode: access,
        };
    const sent = await onSend(content, options);
    if (sent) {
      setValue("");
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

  const placeholder = busy
    ? liveStatus === "waiting_approval"
      ? "Resolve the approval above to continue…"
      : `Codjent is ${statusCopy(liveStatus ?? "running").toLowerCase()}…`
    : large
      ? "Ask codjent to build, debug, or explain something…"
      : "Ask for a follow-up, or describe the next change…";

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
      <div
        className={cn(
          "composer-card rounded-[20px] border border-line bg-surface transition focus-within:border-line-strong",
          large ? "min-h-[148px]" : "min-h-[104px]",
        )}
      >
        <textarea
          ref={textareaRef}
          aria-label="Message codjent"
          className={cn(
            "block w-full resize-none bg-transparent px-4 pt-4 text-[14.5px] leading-6 text-ink outline-none placeholder:text-faint focus-visible:outline-none disabled:cursor-not-allowed",
            large ? "min-h-[82px]" : "min-h-[54px]",
          )}
          disabled={busy || sending}
          onChange={(event) => setValue(event.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={placeholder}
          rows={1}
          value={value}
        />

        <div className="flex items-center gap-1.5 px-2.5 pb-2.5">
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
                disabled={!value.trim() || sending}
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
      </div>

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
    </div>
  );
}
