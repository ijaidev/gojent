# Codjent

Codjent is a local coding agent built on the [gojent](../README.md) agent
loop. It pairs a Go backend with a Next.js frontend: persistent threads,
streaming runs, tool calls, approval prompts, and a provider registry. The
frontend lives in [`frontend/`](frontend) and is designed to feel like
[T3 Code](https://t3.codes).

## Current scope

- Multiple persistent threads backed by SQLite.
- A responsive web UI with a thread sidebar, live transcript, tool cards,
  approval cards, cancellation, and per-thread settings.
- Multiple provider configurations stored in SQLite and managed at `/settings`:
  OpenAI Responses, OpenAI Chat Completions, Anthropic Messages, and Google
  Gen AI, each with an optional custom base URL, its own model list, and static
  or per-run dynamic HTTP headers.
- File read/write/edit/list tools sandboxed to one workspace.
- Image attachments in prompts: pick, drag/drop, or paste PNG, JPEG, and WebP
  files, preview them in a keyboard-navigable lightbox, and replay them with
  the thread.
- `execute_command` requires approval in `ask` mode and runs without prompts in
  `full` mode.
- Per-thread provider, model, and access-mode settings.
- Replayable run events and resumable SSE streams.
- Canceled and failed runs persist the work produced before they stopped:
  partial assistant text and tool results stay in the transcript, and
  unfinished tool calls are closed with an interrupted result.

Threads currently share one configured workspace. Per-thread worktrees are a
future milestone.

## Run

Go 1.25 or newer is required.

```sh
go run ./codjent/cmd/codjent
```

Or from the repository root:

```sh
just --justfile codjent/justfile run
```

A fresh install starts with no providers. Open
<http://127.0.0.1:8787/settings> to add an endpoint, an optional API key, and
one or more models. Provider settings are persisted in the local SQLite
database.

For backwards compatibility, codjent seeds a Google provider on first run when
`GEMINI_API_KEY` or `GOOGLE_API_KEY` is set, when `CODJENT_GOOGLE_MODELS` /
`CODJENT_MODEL` is configured, or when existing threads reference the former
`google` provider. The Google adapter also resolves those environment keys at
request time when no key is stored in the database.

The server listens on `127.0.0.1:8787` by default. Useful options:

```sh
go run ./codjent/cmd/codjent \
  --addr 127.0.0.1:8787 \
  --workspace /path/to/repo \
  --data-dir .local/codjent \
  --provider <provider-id> \
  --model <model-id> \
  --access-mode ask \
  --frontend-dir codjent/frontend/out
```

## Frontend

The frontend is a static Next.js export. For development, run the backend and
Next's dev server together; the UI calls `http://127.0.0.1:8787` when it is
served from port 3000.

```sh
# terminal 1
just --justfile codjent/justfile run

# terminal 2
cd codjent/frontend
pnpm install
pnpm dev
```

Then open <http://localhost:3000>.

For a single-process build, export the frontend and point the backend at it:

```sh
cd codjent/frontend
pnpm build
cd ../..
go run ./codjent/cmd/codjent \
  --workspace . \
  --frontend-dir codjent/frontend/out
```

Or from the repository root:

```sh
just --justfile codjent/justfile web
```

Open <http://127.0.0.1:8787>. The backend serves the static files and the API
from the same origin. Set `NEXT_PUBLIC_CODJENT_API_URL` before `pnpm build` to
point the UI at a different API origin.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `CODJENT_ADDR` | `127.0.0.1:8787` | HTTP listen address |
| `CODJENT_DATA_DIR` | OS data dir | SQLite directory |
| `CODJENT_WORKSPACE` | current directory | Agent workspace |
| `CODJENT_PROVIDER` | first runnable provider | Default provider ID for new threads |
| `CODJENT_MODEL` | provider default | Default model ID for new threads |
| `CODJENT_GOOGLE_MODELS` | unset | Google model IDs used when seeding a Google provider on first run |
| `CODJENT_ACCESS_MODE` | `ask` | `ask` or `full` |
| `CODJENT_MAX_STEPS` | `20` | ReAct step limit per run |
| `CODJENT_ALLOWED_ORIGINS` | localhost:3000 origins | Browser CORS allowlist |
| `CODJENT_FRONTEND_DIR` | unset | Built frontend directory to serve |

The default SQLite location follows OS conventions:

- Linux: `$XDG_DATA_HOME/codjent` or `~/.local/share/codjent`
- macOS: `~/Library/Application Support/codjent`
- Windows: `%LOCALAPPDATA%\codjent`

The database file is created with `0600` permissions and WAL mode. Pass
`--data-dir .local/codjent` for a repository-local database.

## HTTP API

All endpoints are under `/api`.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/health` | Health and workspace path |
| `GET` | `/providers` | Runnable providers and their models |
| `GET` | `/settings/providers` | Provider configurations (API keys masked) |
| `POST` | `/settings/providers` | Create a provider configuration |
| `PUT` | `/settings/providers/{id}` | Update a provider configuration |
| `DELETE` | `/settings/providers/{id}` | Delete an unused provider configuration |
| `GET` | `/threads` | List threads, newest first |
| `POST` | `/threads` | Create a thread |
| `GET` | `/threads/{id}` | Thread plus transcript |
| `PATCH` | `/threads/{id}` | Update title/provider/model/access mode |
| `DELETE` | `/threads/{id}` | Delete a thread and its messages |
| `POST` | `/threads/{id}/messages` | Persist a user message and start a run |
| `GET` | `/runs/{id}` | Run status and pending approval |
| `GET` | `/runs/{id}/events` | SSE event stream (`?after=<event id>`) |
| `POST` | `/runs/{id}/approvals/{approvalID}` | Allow or deny a tool call |
| `POST` | `/runs/{id}/cancel` | Cancel an active run |

Provider configurations are only exposed by the default binary, which wires the
dynamic catalog. API keys are never returned. On update, omit `api_key` (or
send an empty string) to keep the stored secret, and set `clear_api_key` to
remove it. A provider cannot be deleted while any thread references it; the API
returns `409 Conflict`.

Create a provider:

```sh
curl -sS http://127.0.0.1:8787/api/settings/providers \
  -H 'content-type: application/json' \
  -d '{"name":"Local","format":"openai-completion","base_url":"http://localhost:11434/v1","default_model":"llama3","models":[{"id":"llama3","name":"Llama 3"}],"headers":[{"name":"x-opencode-session","value":"{{session.id}}","dynamic":true}],"api_key":""}'
```

The four `format` values are `openai-responses`, `openai-completion`,
`anthropic`, and `google`. `base_url` is optional and must use HTTP or HTTPS.

`headers` is an optional array of custom request headers. A static header
(`"dynamic": false`, the default) sends its value verbatim. A dynamic header
(`"dynamic": true`) resolves its value from the current run before every model
call. Supported placeholders are `{{session.id}}` (the conversation/thread ID)
and `{{session.resource_id}}`. For example, the header above sends
`x-opencode-session: <thread-id>` with every request. Header names are
case-insensitive and must be unique within a provider. The Google adapter
cannot override `x-goog-api-key` because its SDK sets that header after request
headers; configure the Google API key through the provider's key field instead.

Create a thread:

```sh
curl -sS http://127.0.0.1:8787/api/threads \
  -H 'content-type: application/json' \
  -d '{"title":"Fix the failing test","provider":"<provider-id>","model":"<model-id>","access_mode":"ask"}'
```

Start a run:

```sh
curl -sS http://127.0.0.1:8787/api/threads/<thread-id>/messages \
  -H 'content-type: application/json' \
  -d '{"content":"Run the tests and fix the failure."}'
```

Attach images by adding an `attachments` array. `data` is standard base64 of
the raw file bytes, without a `data:` prefix:

```sh
curl -sS http://127.0.0.1:8787/api/threads/<thread-id>/messages \
  -H 'content-type: application/json' \
  -d '{
    "content": "What is wrong with this layout?",
    "attachments": [{
      "filename": "screen.png",
      "mime_type": "image/png",
      "data": "<base64>"
    }]
  }'
```

Supported formats are PNG, JPEG, and WebP. A message can contain up to four
images, at most 5 MiB each and 8 MiB total; a thread is capped at 12 MiB of
images. Images are stored in the SQLite transcript and sent to the configured
model provider whenever the conversation is replayed, so only attach files you
are comfortable sharing with that provider. A text-only model will reject the
request at the provider even though the codjent UI accepts the attachment.

Stream events:

```sh
curl -N 'http://127.0.0.1:8787/api/runs/<run-id>/events?after=0'
```

Each SSE `data:` line is one complete event:

```json
{"id":4,"run_id":"run_...","type":"approval_required","data":{"approval":{"id":"apr_...","tool_call_id":"call_...","tool_name":"execute_command","arguments":"{\"command\":\"go test ./...\"}"}}}
```

Resolve an approval:

```sh
curl -sS -X POST \
  http://127.0.0.1:8787/api/runs/<run-id>/approvals/<approval-id> \
  -H 'content-type: application/json' \
  -d '{"allow":true}'
```

Event types are `run_started`, `text_delta`, `thinking_delta`, `tool_call`,
`tool_result`, `approval_required`, `approval_resolved`, `run_completed`,
`run_failed`, and `run_canceled`. Events are retained per run for replay until
the process exits, bounded to 20,000 events.

## Architecture

The backend follows a deep-module layout:

- `internal/app` owns the thread/run lifecycle, approval gate, event log, and
  transcript persistence contract. It is the only package that knows how a
  codjent run is assembled.
- `internal/store/sqlite` is the SQLite implementation of `app.Store` and the
  provider configuration store.
- `internal/provider` is the provider registry seam; `internal/provider/catalog`
  is the SQLite-backed dynamic catalog that validates CRUD input, builds
  adapters for each format, and exposes only runnable providers to `app`.
  `internal/provider/google` adapts gojent's Gemini provider.
- `internal/httpapi` is a thin JSON/SSE adapter over `app.Service`; it also
  serves provider settings CRUD and the exported frontend from
  `--frontend-dir`.
- `frontend` is a static Next.js export. `lib/api.ts` is the browser boundary,
  `hooks/use-codjent.ts` owns thread state and SSE resumption, and
  `components/` renders the T3-inspired interface. The `/settings` route
  manages providers and models.
- `cmd/codjent` wires configuration, storage, providers, HTTP, and static
  frontend serving.

The browser boundary uses an origin allowlist, and the server binds to loopback
by default because full-access mode can execute arbitrary commands. There is no
authentication layer yet; do not expose the listener to an untrusted network.

## Checks

```sh
cd codjent
go test -race ./...
golangci-lint run ./...

cd frontend
pnpm lint
pnpm exec tsc --noEmit
pnpm build
```
