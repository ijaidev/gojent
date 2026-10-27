"use client";

import { Dialog } from "@base-ui/react/dialog";
import {
  ArrowLeft,
  CircleAlert,
  KeyRound,
  Loader2,
  Pencil,
  Plus,
  Server,
  Trash2,
  X,
} from "lucide-react";
import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { CodjentMark, CodjentWordmark } from "@/components/brand";
import { Spinner } from "@/components/spinner";
import { api } from "@/lib/api";
import { providerFormatInfo, providerFormats } from "@/lib/providers";
import type {
  ProviderConfig,
  ProviderConfigInput,
  ProviderFormat,
  ProviderHeader,
} from "@/lib/types";
import { cn, errorMessage } from "@/lib/utils";

const fieldClass =
  "h-9 w-full rounded-lg border border-line bg-surface px-2.5 text-[12px] text-ink outline-none transition placeholder:text-faint focus:border-line-strong focus:ring-2 focus:ring-accent/10 disabled:cursor-not-allowed disabled:opacity-60";

const labelClass =
  "mb-1.5 block text-[10px] font-medium uppercase tracking-[0.12em] text-faint";

interface ModelRow {
  key: string;
  id: string;
  name: string;
}

interface HeaderRow {
  key: string;
  name: string;
  value: string;
  dynamic: boolean;
}

let modelRowKey = 0;
let headerRowKey = 0;

function newModelRow(id = "", name = ""): ModelRow {
  modelRowKey += 1;
  return { key: `model-${modelRowKey}`, id, name };
}

function newHeaderRow(header?: ProviderHeader): HeaderRow {
  headerRowKey += 1;
  return {
    key: `header-${headerRowKey}`,
    name: header?.name ?? "",
    value: header?.value ?? "",
    dynamic: header?.dynamic ?? false,
  };
}

function formatModels(models: ModelRow[]): ModelRow[] {
  return models.length === 0 ? [newModelRow()] : models;
}

const dynamicPlaceholderPattern = /\{\{\s*([A-Za-z0-9_.]+)\s*\}\}/g;
const supportedPlaceholders = new Set(["session.id", "session.resource_id"]);

// dynamicHeaderError mirrors the backend template validation so a typo is
// caught before the save round-trip.
function dynamicHeaderError(value: string): string | null {
  const withoutPlaceholders = value.replace(dynamicPlaceholderPattern, "");
  if (
    withoutPlaceholders.includes("{{") ||
    withoutPlaceholders.includes("}}")
  ) {
    return "Use {{session.id}} or {{session.resource_id}}.";
  }
  for (const match of value.matchAll(dynamicPlaceholderPattern)) {
    if (!supportedPlaceholders.has(match[1])) {
      return `Unknown placeholder {{${match[1]}}}.`;
    }
  }
  return null;
}

export function ProviderSettings() {
  const [configs, setConfigs] = useState<ProviderConfig[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [editor, setEditor] = useState<ProviderConfig | "new" | null>(null);
  const [pendingDelete, setPendingDelete] = useState<ProviderConfig | null>(
    null,
  );

  const refresh = useCallback(async () => {
    setError(null);
    try {
      setConfigs(await api.listProviderConfigs());
    } catch (loadError) {
      setError(errorMessage(loadError));
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return (
    <div className="min-h-dvh overflow-y-auto bg-app text-ink">
      <header className="sticky top-0 z-20 border-b border-line bg-app/90 backdrop-blur">
        <div className="mx-auto flex h-[60px] w-full max-w-4xl items-center gap-3 px-4 sm:px-6">
          <Link
            aria-label="Back to codjent"
            className="grid size-8 place-items-center rounded-lg text-muted transition hover:bg-surface-muted hover:text-ink"
            href="/"
          >
            <ArrowLeft className="size-4" />
          </Link>
          <CodjentMark className="size-7 text-ink" />
          <CodjentWordmark />
          <span className="text-line-strong">/</span>
          <span className="min-w-0 truncate text-[12px] font-medium">
            Provider settings
          </span>
        </div>
      </header>

      <main className="mx-auto w-full max-w-4xl px-4 pb-20 pt-8 sm:px-6">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div>
            <h1 className="text-[24px] font-semibold tracking-[-0.035em]">
              Providers
            </h1>
            <p className="mt-1.5 max-w-xl text-[12px] leading-5 text-muted">
              Connect the model endpoints codjent can use. Credentials, headers,
              and model lists are stored locally in SQLite and never leave this
              machine except in requests to the endpoint you configure.
            </p>
          </div>
          <button
            type="button"
            className="inline-flex items-center gap-1.5 rounded-lg bg-ink px-3 py-2 text-[11px] font-medium text-app transition hover:opacity-85"
            onClick={() => setEditor("new")}
          >
            <Plus className="size-3.5" />
            Add provider
          </button>
        </div>

        {error ? (
          <div className="mt-5 flex items-start gap-2 rounded-xl border border-danger/20 bg-danger-soft px-3 py-2.5 text-danger">
            <CircleAlert className="mt-0.5 size-3.5 shrink-0" />
            <p className="min-w-0 flex-1 whitespace-pre-wrap break-words text-[11px] leading-5">
              {error}
            </p>
            <button
              type="button"
              className="rounded px-1 text-[10.5px] font-medium opacity-70 transition hover:opacity-100"
              onClick={() => void refresh()}
            >
              Retry
            </button>
          </div>
        ) : null}

        {configs === null ? (
          error ? null : (
            <div className="mt-16 flex items-center justify-center gap-2 text-[11px] text-muted">
              <Spinner className="size-3.5" />
              Loading providers
            </div>
          )
        ) : configs.length === 0 ? (
          <EmptyProviders onAdd={() => setEditor("new")} />
        ) : (
          <div className="mt-6 space-y-3">
            {configs.map((config) => (
              <ProviderCard
                config={config}
                key={config.id}
                onDelete={() => setPendingDelete(config)}
                onEdit={() => setEditor(config)}
              />
            ))}
          </div>
        )}

        <div className="mt-8 rounded-xl border border-line bg-surface-muted/50 px-4 py-3 text-[10.5px] leading-5 text-faint">
          <span className="font-medium text-muted">Tip:</span> local endpoints
          such as Ollama, LM Studio, and llama.cpp usually work with the OpenAI
          Chat Completions format and may not need an API key. Hosted providers
          work with their default endpoint when the base URL is left blank.
        </div>
      </main>

      <ProviderEditorDialog
        config={editor === "new" ? null : editor}
        onOpenChange={(open) => {
          if (!open) {
            setEditor(null);
          }
        }}
        onSaved={() => {
          setEditor(null);
          void refresh();
        }}
        open={editor !== null}
      />

      <DeleteProviderDialog
        config={pendingDelete}
        onDeleted={() => {
          setPendingDelete(null);
          void refresh();
        }}
        onOpenChange={(open) => {
          if (!open) {
            setPendingDelete(null);
          }
        }}
      />
    </div>
  );
}

function ProviderCard({
  config,
  onEdit,
  onDelete,
}: {
  config: ProviderConfig;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const format = providerFormatInfo(config.format);
  const defaultModel =
    config.models.find((model) => model.id === config.default_model)?.name ??
    config.default_model;

  return (
    <article className="rounded-2xl border border-line bg-surface p-4 sm:p-5">
      <div className="flex flex-wrap items-start gap-3">
        <div className="grid size-9 shrink-0 place-items-center rounded-xl bg-surface-muted text-muted">
          <Server className="size-4" />
        </div>

        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <h2 className="min-w-0 truncate text-[14px] font-semibold">
              {config.name}
            </h2>
            <span className="rounded-full border border-line bg-surface-muted px-2 py-0.5 text-[9px] font-medium uppercase tracking-[0.1em] text-muted">
              {format.shortLabel}
            </span>
            <span
              className={cn(
                "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[9px] font-medium",
                config.api_key_set
                  ? "border-success/20 bg-success-soft text-success"
                  : "border-line bg-surface-muted text-faint",
              )}
            >
              <KeyRound className="size-2.5" />
              {config.api_key_set ? "Key stored" : "No key"}
            </span>
          </div>

          <dl className="mt-2 grid gap-1 text-[10.5px] text-faint sm:grid-cols-2">
            <div className="flex min-w-0 gap-1">
              <dt>Endpoint</dt>
              <dd className="min-w-0 truncate font-mono text-muted">
                {config.base_url || "provider default"}
              </dd>
            </div>
            <div className="flex min-w-0 gap-1">
              <dt>Default</dt>
              <dd className="min-w-0 truncate font-mono text-muted">
                {defaultModel || "none"}
              </dd>
            </div>
            <div className="flex min-w-0 gap-1">
              <dt>Headers</dt>
              <dd className="min-w-0 truncate font-mono text-muted">
                {config.headers?.length
                  ? config.headers.map((header) => header.name).join(", ")
                  : "none"}
              </dd>
            </div>
          </dl>
        </div>

        <div className="flex items-center gap-1.5">
          <button
            type="button"
            className="inline-flex items-center gap-1 rounded-lg border border-line-strong bg-surface px-2.5 py-1.5 text-[10.5px] font-medium text-ink transition hover:bg-surface-muted"
            onClick={onEdit}
          >
            <Pencil className="size-3" />
            Edit
          </button>
          <button
            type="button"
            aria-label={`Delete ${config.name}`}
            className="grid size-7 place-items-center rounded-lg text-muted transition hover:bg-danger-soft hover:text-danger"
            onClick={onDelete}
          >
            <Trash2 className="size-3.5" />
          </button>
        </div>
      </div>

      <div className="mt-3 flex flex-wrap gap-1.5 border-t border-line pt-3">
        {config.models.length === 0 ? (
          <span className="text-[10.5px] leading-5 text-faint">
            No models yet. Add one so codjent can start threads with this
            provider.
          </span>
        ) : (
          config.models.map((model) => (
            <span
              className={cn(
                "inline-flex min-w-0 items-center gap-1.5 rounded-lg border px-2 py-1 text-[10px]",
                model.id === config.default_model
                  ? "border-accent/20 bg-accent-soft text-accent"
                  : "border-line bg-surface-muted text-muted",
              )}
              key={model.id}
            >
              <span className="max-w-[160px] truncate font-medium">
                {model.name}
              </span>
              <span className="max-w-[200px] truncate font-mono text-[9px] opacity-70">
                {model.id}
              </span>
            </span>
          ))
        )}
      </div>
    </article>
  );
}

function EmptyProviders({ onAdd }: { onAdd: () => void }) {
  return (
    <div className="mt-6 rounded-2xl border border-dashed border-line-strong bg-surface px-6 py-12 text-center">
      <div className="mx-auto grid size-11 place-items-center rounded-2xl bg-accent-soft text-accent">
        <Server className="size-5" />
      </div>
      <h2 className="mt-3 text-[15px] font-semibold">
        Add your first provider
      </h2>
      <p className="mx-auto mt-1.5 max-w-md text-[11.5px] leading-5 text-muted">
        Give codjent an endpoint, an optional API key, and the models it may
        use. Everything is stored in the local codjent database.
      </p>
      <button
        type="button"
        className="mt-4 inline-flex items-center gap-1.5 rounded-lg bg-ink px-3 py-2 text-[11px] font-medium text-app transition hover:opacity-85"
        onClick={onAdd}
      >
        <Plus className="size-3.5" />
        Add provider
      </button>
      <div className="mt-6 flex flex-wrap justify-center gap-1.5">
        {providerFormats.map((format) => (
          <span
            className="rounded-full border border-line bg-surface-muted px-2.5 py-1 text-[10px] text-muted"
            key={format.value}
          >
            {format.label}
          </span>
        ))}
      </div>
    </div>
  );
}

function ProviderEditorDialog({
  open,
  config,
  onOpenChange,
  onSaved,
}: {
  open: boolean;
  config: ProviderConfig | null;
  onOpenChange: (open: boolean) => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState("");
  const [format, setFormat] = useState<ProviderFormat>("openai-completion");
  const [baseURL, setBaseURL] = useState("");
  const [apiKey, setAPIKey] = useState("");
  const [clearAPIKey, setClearAPIKey] = useState(false);
  const [headers, setHeaders] = useState<HeaderRow[]>([]);
  const [models, setModels] = useState<ModelRow[]>([newModelRow()]);
  const [defaultModel, setDefaultModel] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    if (config) {
      setName(config.name);
      setFormat(config.format);
      setBaseURL(config.base_url);
      setHeaders((config.headers ?? []).map((header) => newHeaderRow(header)));
      setModels(
        formatModels(
          config.models.map((model) => newModelRow(model.id, model.name)),
        ),
      );
      setDefaultModel(config.default_model);
    } else {
      setName("");
      setFormat("openai-completion");
      setBaseURL("");
      setHeaders([]);
      setModels([newModelRow()]);
      setDefaultModel("");
    }
    setAPIKey("");
    setClearAPIKey(false);
    setSaving(false);
    setError(null);
  }, [open, config]);

  const formatInfo = providerFormatInfo(format);
  const modelIDs = models
    .map((model) => model.id.trim())
    .filter((id) => id !== "");
  const duplicateIDs = modelIDs.filter(
    (id, index) => modelIDs.indexOf(id) !== index,
  );
  const incompleteModel = models.some(
    (model) => !model.id.trim() && model.name.trim() !== "",
  );
  const effectiveDefault = modelIDs.includes(defaultModel)
    ? defaultModel
    : (modelIDs[0] ?? "");

  const activeHeaders = headers.filter(
    (header) => header.name.trim() !== "" || header.value.trim() !== "",
  );
  const headerNames = activeHeaders
    .map((header) => header.name.trim().toLowerCase())
    .filter((headerName) => headerName !== "");
  const duplicateHeaderNames = headerNames.filter(
    (headerName, index) => headerNames.indexOf(headerName) !== index,
  );
  const incompleteHeader = activeHeaders.some((header) => !header.name.trim());
  const invalidDynamicHeader = activeHeaders.find(
    (header) => header.dynamic && dynamicHeaderError(header.value) !== null,
  );

  const updateModel = (index: number, patch: Partial<ModelRow>) => {
    setModels((prev) =>
      prev.map((model, modelIndex) => {
        if (modelIndex !== index) {
          return model;
        }
        const next = { ...model, ...patch };
        if (
          patch.id !== undefined &&
          (model.name.trim() === "" || model.name === model.id)
        ) {
          next.name = patch.id;
        }
        return next;
      }),
    );
    if (patch.id !== undefined && defaultModel === models[index]?.id) {
      setDefaultModel(patch.id);
    }
  };

  const addModel = () => {
    setModels((prev) => [...prev, newModelRow()]);
  };

  const removeModel = (index: number) => {
    setModels((prev) => prev.filter((_, modelIndex) => modelIndex !== index));
    if (defaultModel === models[index]?.id) {
      setDefaultModel("");
    }
  };

  const updateHeader = (index: number, patch: Partial<HeaderRow>) => {
    setHeaders((prev) =>
      prev.map((header, headerIndex) =>
        headerIndex === index ? { ...header, ...patch } : header,
      ),
    );
  };

  const addHeader = () => {
    setHeaders((prev) => [...prev, newHeaderRow()]);
  };

  const removeHeader = (index: number) => {
    setHeaders((prev) =>
      prev.filter((_, headerIndex) => headerIndex !== index),
    );
  };

  const save = async () => {
    if (
      !name.trim() ||
      duplicateIDs.length > 0 ||
      incompleteModel ||
      duplicateHeaderNames.length > 0 ||
      incompleteHeader ||
      invalidDynamicHeader
    ) {
      return;
    }
    setSaving(true);
    setError(null);

    const input: ProviderConfigInput = {
      name: name.trim(),
      format,
      base_url: baseURL.trim(),
      default_model: effectiveDefault,
      models: models
        .map((model) => ({
          id: model.id.trim(),
          name: model.name.trim() || model.id.trim(),
        }))
        .filter((model) => model.id !== ""),
      headers: activeHeaders.map((header) => ({
        name: header.name.trim(),
        value: header.value.trim(),
        dynamic: header.dynamic,
      })),
    };
    if (config) {
      if (clearAPIKey) {
        input.clear_api_key = true;
      } else if (apiKey.trim()) {
        input.api_key = apiKey.trim();
      }
    } else if (apiKey.trim()) {
      input.api_key = apiKey.trim();
    }

    try {
      if (config) {
        await api.updateProviderConfig(config.id, input);
      } else {
        await api.createProviderConfig(input);
      }
      onSaved();
    } catch (saveError) {
      setError(errorMessage(saveError));
    } finally {
      setSaving(false);
    }
  };

  const disabled = saving;

  return (
    <Dialog.Root
      open={open}
      onOpenChange={(next) => {
        if (!next && !saving) {
          onOpenChange(false);
        }
      }}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/25 backdrop-blur-[2px] transition-opacity duration-150 data-ending-style:opacity-0 data-starting-style:opacity-0" />
        <Dialog.Popup className="fixed left-1/2 top-1/2 z-50 flex max-h-[min(760px,calc(100dvh-2rem))] w-[min(640px,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-2xl border border-line bg-surface text-ink shadow-[0_24px_80px_-24px_rgba(28,25,23,0.45)] outline-none transition duration-150 data-ending-style:scale-[0.98] data-ending-style:opacity-0 data-starting-style:scale-[0.98] data-starting-style:opacity-0">
          <div className="flex items-start justify-between border-b border-line px-5 py-4">
            <div>
              <Dialog.Title className="text-[14px] font-semibold">
                {config ? "Edit provider" : "Add provider"}
              </Dialog.Title>
              <Dialog.Description className="mt-1 text-[11px] leading-5 text-faint">
                {config
                  ? "Update the endpoint, credentials, headers, or model list."
                  : "Choose the wire format this endpoint speaks, then add its models."}
              </Dialog.Description>
            </div>
            <Dialog.Close
              aria-label="Close provider editor"
              className="grid size-7 place-items-center rounded-lg text-muted transition hover:bg-surface-muted hover:text-ink"
              disabled={disabled}
            >
              <X className="size-4" />
            </Dialog.Close>
          </div>

          <div className="min-h-0 flex-1 overflow-y-auto px-5 py-5">
            <div className="space-y-4">
              <div className="grid gap-3 sm:grid-cols-[1fr_220px]">
                <label className="block">
                  <span className={labelClass}>Name</span>
                  <input
                    className={fieldClass}
                    disabled={disabled}
                    onChange={(event) => setName(event.target.value)}
                    placeholder="Local models"
                    value={name}
                  />
                </label>
                <label className="block">
                  <span className={labelClass}>Format</span>
                  <select
                    className={fieldClass}
                    disabled={disabled}
                    onChange={(event) =>
                      setFormat(event.target.value as ProviderFormat)
                    }
                    value={format}
                  >
                    {providerFormats.map((option) => (
                      <option key={option.value} value={option.value}>
                        {option.label}
                      </option>
                    ))}
                  </select>
                </label>
              </div>

              <p className="rounded-lg border border-line bg-surface-muted/60 px-3 py-2 text-[10.5px] leading-4 text-muted">
                {formatInfo.description}
              </p>

              <label className="block">
                <span className={labelClass}>Base URL</span>
                <input
                  className={cn(fieldClass, "font-mono")}
                  disabled={disabled}
                  onChange={(event) => setBaseURL(event.target.value)}
                  placeholder={formatInfo.baseURLPlaceholder}
                  spellCheck={false}
                  value={baseURL}
                />
                <span className="mt-1 block text-[10px] leading-4 text-faint">
                  Leave blank to use the provider default. HTTP and HTTPS are
                  supported.
                </span>
              </label>

              <div>
                <label className="block">
                  <span className={labelClass}>API key</span>
                  <input
                    autoComplete="off"
                    className={cn(fieldClass, "font-mono")}
                    disabled={disabled || clearAPIKey}
                    onChange={(event) => setAPIKey(event.target.value)}
                    placeholder={
                      config?.api_key_set
                        ? "Leave blank to keep the stored key"
                        : "Optional for local or unauthenticated endpoints"
                    }
                    spellCheck={false}
                    type="password"
                    value={apiKey}
                  />
                </label>
                {config?.api_key_set ? (
                  <label className="mt-2 flex items-center gap-2 text-[10.5px] text-muted">
                    <input
                      checked={clearAPIKey}
                      className="size-3.5 accent-[var(--danger)]"
                      disabled={disabled}
                      onChange={(event) => {
                        setClearAPIKey(event.target.checked);
                        if (event.target.checked) {
                          setAPIKey("");
                        }
                      }}
                      type="checkbox"
                    />
                    Remove the stored key when saving
                  </label>
                ) : (
                  <span className="mt-1 block text-[10px] leading-4 text-faint">
                    Keys are stored in the local database and only sent to this
                    endpoint.
                  </span>
                )}
              </div>

              <div>
                <div className="flex items-center justify-between gap-2">
                  <span className={labelClass}>Headers</span>
                  <button
                    type="button"
                    className="inline-flex items-center gap-1 rounded-lg border border-line-strong bg-surface px-2 py-1 text-[10px] font-medium text-ink transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
                    disabled={disabled}
                    onClick={addHeader}
                  >
                    <Plus className="size-3" />
                    Add header
                  </button>
                </div>

                {headers.length === 0 ? (
                  <p className="rounded-lg border border-dashed border-line-strong px-3 py-3 text-center text-[10.5px] leading-4 text-faint">
                    No custom headers. Add one when the endpoint needs a gateway
                    or session header.
                  </p>
                ) : (
                  <div className="space-y-2">
                    {headers.map((header, index) => {
                      const templateError = header.dynamic
                        ? dynamicHeaderError(header.value)
                        : null;
                      return (
                        <div className="space-y-1" key={header.key}>
                          <div className="flex flex-col gap-2 sm:flex-row sm:items-start">
                            <input
                              aria-label={`Header ${index + 1} name`}
                              className={cn(fieldClass, "font-mono sm:w-48")}
                              disabled={disabled}
                              onChange={(event) =>
                                updateHeader(index, {
                                  name: event.target.value,
                                })
                              }
                              placeholder="Header name"
                              spellCheck={false}
                              value={header.name}
                            />
                            <select
                              aria-label={`Header ${index + 1} mode`}
                              className={cn(fieldClass, "sm:w-28")}
                              disabled={disabled}
                              onChange={(event) =>
                                updateHeader(index, {
                                  dynamic: event.target.value === "dynamic",
                                })
                              }
                              value={header.dynamic ? "dynamic" : "static"}
                            >
                              <option value="static">Static</option>
                              <option value="dynamic">Dynamic</option>
                            </select>
                            <input
                              aria-label={`Header ${index + 1} value`}
                              className={cn(fieldClass, "font-mono sm:flex-1")}
                              disabled={disabled}
                              onChange={(event) =>
                                updateHeader(index, {
                                  value: event.target.value,
                                })
                              }
                              placeholder={
                                header.dynamic
                                  ? "{{session.id}}"
                                  : "Header value"
                              }
                              spellCheck={false}
                              value={header.value}
                            />
                            <button
                              type="button"
                              aria-label={`Remove header ${index + 1}`}
                              className="grid size-9 shrink-0 place-items-center rounded-lg border border-line text-muted transition hover:border-danger/30 hover:bg-danger-soft hover:text-danger disabled:cursor-not-allowed disabled:opacity-50"
                              disabled={disabled}
                              onClick={() => removeHeader(index)}
                            >
                              <Trash2 className="size-3.5" />
                            </button>
                          </div>
                          {templateError ? (
                            <p className="text-[10px] leading-4 text-danger">
                              {templateError}
                            </p>
                          ) : null}
                        </div>
                      );
                    })}
                  </div>
                )}

                {duplicateHeaderNames.length > 0 ? (
                  <p className="mt-1.5 text-[10px] leading-4 text-danger">
                    Duplicate header names:{" "}
                    {Array.from(new Set(duplicateHeaderNames)).join(", ")}
                  </p>
                ) : incompleteHeader ? (
                  <p className="mt-1.5 text-[10px] leading-4 text-danger">
                    Every header needs a name.
                  </p>
                ) : (
                  <p className="mt-1.5 text-[10px] leading-4 text-faint">
                    Static values are sent as-is. Dynamic values are resolved
                    for each run; use <code>{"{{session.id}}"}</code> for the
                    current conversation ID.
                  </p>
                )}

                {format === "google" ? (
                  <p className="mt-1.5 text-[10px] leading-4 text-faint">
                    Google sets <code>x-goog-api-key</code> itself, so it cannot
                    be overridden here.
                  </p>
                ) : null}
              </div>

              <div>
                <div className="flex items-center justify-between gap-2">
                  <span className={labelClass}>Models</span>
                  <button
                    type="button"
                    className="inline-flex items-center gap-1 rounded-lg border border-line-strong bg-surface px-2 py-1 text-[10px] font-medium text-ink transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
                    disabled={disabled}
                    onClick={addModel}
                  >
                    <Plus className="size-3" />
                    Add model
                  </button>
                </div>

                {models.length === 0 ? (
                  <p className="rounded-lg border border-dashed border-line-strong px-3 py-3 text-center text-[10.5px] leading-4 text-faint">
                    No models configured. Add at least one to run threads.
                  </p>
                ) : (
                  <div className="space-y-2">
                    {models.map((model, index) => (
                      <div
                        className="flex flex-col gap-2 sm:flex-row sm:items-start"
                        key={model.key}
                      >
                        <input
                          aria-label={`Model ${index + 1} ID`}
                          className={cn(fieldClass, "font-mono sm:flex-1")}
                          disabled={disabled}
                          onChange={(event) =>
                            updateModel(index, { id: event.target.value })
                          }
                          placeholder="model-id"
                          spellCheck={false}
                          value={model.id}
                        />
                        <input
                          aria-label={`Model ${index + 1} display name`}
                          className={cn(fieldClass, "sm:flex-1")}
                          disabled={disabled}
                          onChange={(event) =>
                            updateModel(index, { name: event.target.value })
                          }
                          placeholder="Display name (optional)"
                          value={model.name}
                        />
                        <button
                          type="button"
                          aria-label={`Remove model ${index + 1}`}
                          className="grid size-9 shrink-0 place-items-center rounded-lg border border-line text-muted transition hover:border-danger/30 hover:bg-danger-soft hover:text-danger disabled:cursor-not-allowed disabled:opacity-50"
                          disabled={disabled}
                          onClick={() => removeModel(index)}
                        >
                          <Trash2 className="size-3.5" />
                        </button>
                      </div>
                    ))}
                  </div>
                )}

                {duplicateIDs.length > 0 ? (
                  <p className="mt-1.5 text-[10px] leading-4 text-danger">
                    Duplicate model IDs:{" "}
                    {Array.from(new Set(duplicateIDs)).join(", ")}
                  </p>
                ) : incompleteModel ? (
                  <p className="mt-1.5 text-[10px] leading-4 text-danger">
                    Every model needs an ID.
                  </p>
                ) : (
                  <p className="mt-1.5 text-[10px] leading-4 text-faint">
                    The ID is the exact name the endpoint expects. The display
                    name is only shown in codjent.
                  </p>
                )}
              </div>

              <label className="block">
                <span className={labelClass}>Default model</span>
                <select
                  className={fieldClass}
                  disabled={disabled || modelIDs.length === 0}
                  onChange={(event) => setDefaultModel(event.target.value)}
                  value={effectiveDefault}
                >
                  {modelIDs.length === 0 ? (
                    <option value="">Add a model first</option>
                  ) : (
                    modelIDs.map((id) => (
                      <option key={id} value={id}>
                        {models.find((model) => model.id.trim() === id)?.name ||
                          id}
                      </option>
                    ))
                  )}
                </select>
                <span className="mt-1 block text-[10px] leading-4 text-faint">
                  New threads start with this model. You can switch models per
                  thread later.
                </span>
              </label>
            </div>
          </div>

          {error ? (
            <div className="border-t border-danger/20 bg-danger-soft px-5 py-2.5 text-[11px] leading-5 text-danger">
              {error}
            </div>
          ) : null}

          <div className="flex items-center justify-between gap-2 border-t border-line bg-surface-muted/40 px-5 py-3">
            <span className="hidden text-[10px] text-faint sm:block">
              {config
                ? "Changes apply to new runs."
                : "You can add models later."}
            </span>
            <div className="ml-auto flex items-center gap-2">
              <button
                type="button"
                className="rounded-lg border border-line-strong bg-surface px-3 py-1.5 text-[11px] font-medium text-ink transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
                disabled={saving}
                onClick={() => onOpenChange(false)}
              >
                Cancel
              </button>
              <button
                type="button"
                className="inline-flex items-center gap-1.5 rounded-lg bg-ink px-3 py-1.5 text-[11px] font-medium text-app transition hover:opacity-85 disabled:cursor-not-allowed disabled:opacity-50"
                disabled={
                  saving ||
                  !name.trim() ||
                  duplicateIDs.length > 0 ||
                  incompleteModel ||
                  duplicateHeaderNames.length > 0 ||
                  incompleteHeader ||
                  Boolean(invalidDynamicHeader)
                }
                onClick={() => void save()}
              >
                {saving ? <Loader2 className="size-3.5 animate-spin" /> : null}
                {config ? "Save changes" : "Add provider"}
              </button>
            </div>
          </div>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function DeleteProviderDialog({
  config,
  onOpenChange,
  onDeleted,
}: {
  config: ProviderConfig | null;
  onOpenChange: (open: boolean) => void;
  onDeleted: () => void;
}) {
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (config) {
      setDeleting(false);
      setError(null);
    }
  }, [config]);

  const remove = async () => {
    if (!config) {
      return;
    }
    setDeleting(true);
    setError(null);
    try {
      await api.deleteProviderConfig(config.id);
      onDeleted();
    } catch (deleteError) {
      setError(errorMessage(deleteError));
      setDeleting(false);
    }
  };

  return (
    <Dialog.Root
      open={Boolean(config)}
      onOpenChange={(next) => {
        if (!next && !deleting) {
          onOpenChange(false);
        }
      }}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/25 backdrop-blur-[2px] transition-opacity duration-150 data-ending-style:opacity-0 data-starting-style:opacity-0" />
        <Dialog.Popup className="fixed left-1/2 top-1/2 z-50 w-[min(440px,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2 overflow-hidden rounded-2xl border border-line bg-surface text-ink shadow-[0_24px_80px_-24px_rgba(28,25,23,0.45)] outline-none transition duration-150 data-ending-style:scale-[0.98] data-ending-style:opacity-0 data-starting-style:scale-[0.98] data-starting-style:opacity-0">
          <div className="flex items-start justify-between border-b border-line px-5 py-4">
            <div>
              <Dialog.Title className="text-[14px] font-semibold">
                Delete {config?.name ?? "provider"}?
              </Dialog.Title>
              <Dialog.Description className="mt-1 text-[11px] leading-5 text-faint">
                The provider and its stored key will be removed. This cannot be
                undone.
              </Dialog.Description>
            </div>
            <Dialog.Close
              aria-label="Close delete dialog"
              className="grid size-7 place-items-center rounded-lg text-muted transition hover:bg-surface-muted hover:text-ink"
              disabled={deleting}
            >
              <X className="size-4" />
            </Dialog.Close>
          </div>

          <div className="px-5 py-4 text-[11px] leading-5 text-muted">
            A provider cannot be deleted while any thread still references it.
            Delete or move those threads first.
          </div>

          {error ? (
            <div className="border-t border-danger/20 bg-danger-soft px-5 py-2.5 text-[11px] leading-5 text-danger">
              {error}
            </div>
          ) : null}

          <div className="flex items-center justify-end gap-2 border-t border-line bg-surface-muted/40 px-5 py-3">
            <button
              type="button"
              className="rounded-lg border border-line-strong bg-surface px-3 py-1.5 text-[11px] font-medium text-ink transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
              disabled={deleting}
              onClick={() => onOpenChange(false)}
            >
              Keep provider
            </button>
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
              Delete provider
            </button>
          </div>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
