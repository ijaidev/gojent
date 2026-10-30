# Codjent frontend

The Next.js frontend for [Codjent](../README.md). It is a static export: the Go
backend serves the built files and the API from the same origin in production.

## Development

Run the backend and the Next.js dev server side by side:

```sh
# repository root
just --justfile codjent/justfile run

# codjent/frontend
pnpm install
pnpm dev
```

Open <http://localhost:3000>. When the UI is served from port 3000 it calls the
backend at `http://127.0.0.1:8787`. For any other origin, set
`NEXT_PUBLIC_CODJENT_API_URL` before starting the dev server or building.

## Build

```sh
pnpm build
```

The export is written to `out/`. Run it with the backend from the repository
root:

```sh
go run ./codjent/cmd/codjent --frontend-dir codjent/frontend/out
```

See the [main README](../README.md) for configuration, the HTTP API, and the
full run instructions.

## Layout

- `src/app` — app shell, theme tokens, and static entry points (`/` and
  `/settings`).
- `src/components` — sidebar, transcript, composer, thread settings, provider
  settings, and tool/approval cards.
- `src/hooks/use-codjent.ts` — thread state, run lifecycle, SSE replay, and
  approvals.
- `src/lib/api.ts` — the browser/API boundary.
- `src/lib/types.ts` — API wire types shared by the UI.
- `src/lib/providers.ts` — provider format labels, descriptions, and endpoint
  hints.

## Checks

```sh
pnpm lint
pnpm exec tsc --noEmit
pnpm build
```
