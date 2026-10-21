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
- Google Gemini as the only provider (the provider seam is ready for more).
- File read/write/edit/list tools sandboxed to one workspace.
- `execute_command` requires approval in `ask` mode and runs without prompts in
  `full` mode.
- Per-thread provider, model, and access-mode settings.
- Replayable run events and resumable SSE streams.

Threads currently share one configured workspace. Per-thread worktrees are a
future milestone.

## Run

Go 1.25 or newer is required. The Gemini key is resolved by the existing
gojent Google provider from `GEMINI_API_KEY` or `GOOGLE_API_KEY`.

```sh
export GEMINI_API_KEY=...
go run ./codjent/cmd/codjent
```

Or from the repository root:

```sh
just codjent
```

The server listens on `127.0.0.1:8787` by default. Useful options:

```sh
go run ./codjent/cmd/codjent \
  --addr 127.0.0.1:8787 \
  --workspace /path/to/repo \
  --data-dir .local/codjent \
  --model gemini-3.8-flash \
  --access-mode ask \
  --frontend-dir codjent/frontend/out
```

## Frontend

The frontend is a static Next.js export. For development, run the backend and
Next's dev server together; the UI calls `http://127.0.0.1:8787` when it is
served from port 3000.

```sh
# terminal 1
just codjent

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

Open <http://127.0.0.1:8787>. The backend serves the static files and the API
from the same origin. Set `NEXT_PUBLIC_CODJENT_API_URL` before `pnpm build` to
point the UI at a different API origin.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `CODJENT_ADDR` | `127.0.0.1:8787` | HTTP listen address |
| `CODJENT_DATA_DIR` | OS data dir | SQLite directory |
| `CODJENT_WORKSPACE` | current directory | Agent workspace |
| `CODJENT_PROVIDER` | `google` | Default provider ID |
| `CODJENT_MODEL` | provider default | Default model ID |
| `CODJENT_GOOGLE_MODELS` | `gemini-3.8-flash` | Comma-separated model picker values |
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
| `GET` | `/providers` | Provider and model catalog |
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

Create a thread:

```sh
curl -sS http://127.0.0.1:8787/api/threads \
  -H 'content-type: application/json' \
  -d '{"title":"Fix the failing test","provider":"google","model":"gemini-3.8-flash","access_mode":"ask"}'
```

Start a run:

```sh
curl -sS http://127.0.0.1:8787/api/threads/<thread-id>/messages \
  -H 'content-type: application/json' \
  -d '{"content":"Run the tests and fix the failure."}'
```

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
- `internal/store/sqlite` is the SQLite implementation of `app.Store`.
- `internal/provider` is the provider registry seam; `internal/provider/google`
  adapts gojent's Gemini provider.
- `internal/httpapi` is a thin JSON/SSE adapter over `app.Service`; it can also
  serve the exported frontend from `--frontend-dir`.
- `frontend` is a static Next.js export. `lib/api.ts` is the browser boundary,
  `hooks/use-codjent.ts` owns thread state and SSE resumption, and
  `components/` renders the T3-inspired interface.
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
pnpm build
```
