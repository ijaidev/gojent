# Codjent image input support — implementation plan

Status: implemented (v1)  
Date: 2026-10-27  
Scope: images attached to user prompts, as input only.

## 1. Decision

Support image attachments in codjent without changing `core/` or any module
under `providers/`.

The core already models files as first-class message parts:

- `llm.FilePart` carries `MIMEType`, inline bytes, URL, file ID, filename, and
  detail (`core/llm/part_file.go`).
- `llm.UserMessageWithParts` builds ordered user content
  (`core/llm/message_constructors.go`).
- `Message.Validate` allows file parts in user messages
  (`core/llm/message.go`).
- The persisted message format version already serializes file parts
  (`core/llm/json.go`, `FormatVersion = 2`).
- The agent runner rejects files when the model does not advertise `Vision`
  (`core/agent/features.go`).
- All four provider adapters already encode file parts:
  OpenAI Responses (`providers/openai/responses_convert.go`), OpenAI Chat
  Completions (`providers/openai/chatcompletion_convert.go`), Anthropic
  (`providers/anthropic/convert.go`), and Google
  (`providers/google/part.go`).
- Codjent already projects file parts into API views
  (`codjent/internal/app/types.go`), and SQLite already round-trips them
  (`codjent/internal/store/sqlite/sqlite_test.go`).

The missing pieces are all codjent concerns:

1. the message API only accepts a text string;
2. the composer cannot select, preview, or send files;
3. the transcript does not render image parts;
4. the request body limit is 1 MiB.

This plan implements an inline, JSON-based v1 with no database migration and no
core/provider changes.

## 2. Scope

### In scope for v1

- Attach 1–4 images to a new user message.
- Send text plus images, or images alone.
- Pick files, drag and drop, and paste screenshots.
- Preview and remove attachments before sending.
- Persist images in the existing canonical transcript.
- Replay images in later turns and after reload.
- Render user attachments as thumbnails in the transcript.
- Validate type, size, count, and file signature server-side.
- Surface provider rejection (for example a non-vision model) as a normal run
  failure.

### Explicitly out of scope for v1

- Image generation or image editing.
- Model-produced image rendering beyond the generic file-part view that
  already exists.
- PDFs, text documents, audio, and video.
- OCR-specific preprocessing.
- Provider Files API uploads and file-ID reuse.
- Per-model `vision` capability metadata.
- Pruning or summarizing old images from replay history.

The same architecture extends to documents later; only the MIME allowlist and
provider capability rules would change.

## 3. API contract

Extend the existing endpoint:

`POST /api/threads/{threadID}/messages`

```json
{
  "content": "What is wrong with this layout?",
  "attachments": [
    {
      "filename": "screen.png",
      "mime_type": "image/png",
      "data": "iVBORw0KGgoAAAANSUhEUg..."
    }
  ]
}
```

Rules:

- `content` remains a string and is backwards compatible.
- `attachments` is optional and omitted by old clients.
- `data` is standard base64 of the raw file bytes, without a `data:` prefix.
  Go's `[]byte` JSON decoding accepts this directly.
- `mime_type` is required and must be one of the v1 formats.
- `filename` is optional and display-only. It is never used as a path.
- Sending neither content nor attachments returns `400`.
- Invalid base64, unsupported type, signature mismatch, too many files, or
  oversized files return `400` with a specific message.
- An oversized HTTP body returns `413` (or `400` if we keep the current generic
  wrapper; `413` is clearer and worth wiring).

The response shape (`RunInfo`) and the SSE stream are unchanged. Images never
travel through run events; the transcript refresh at run completion provides
them.

## 4. Backend design

### 4.1 App input types and validation

Add to `codjent/internal/app/types.go` (or a small
`codjent/internal/app/attachments.go`):

```go
// AttachmentInput is one uploaded image from the message request.
type AttachmentInput struct {
    Filename string `json:"filename,omitempty"`
    MIMEType string `json:"mime_type"`
    Data     []byte `json:"data"`
}

// MessageInput is the attachment-capable start-message payload.
type MessageInput struct {
    Content     string
    Attachments []AttachmentInput
}
```

Add `StartMessageWith` and keep `StartMessage` as a text-only wrapper so
existing tests and callers keep working:

```go
func (s *Service) StartMessage(ctx context.Context, threadID, content string) (*RunInfo, error) {
    return s.StartMessageWith(ctx, threadID, MessageInput{Content: content})
}

func (s *Service) StartMessageWith(ctx context.Context, threadID string, input MessageInput) (*RunInfo, error)
```

`StartMessageWith` performs the same busy/closed/history checks as today, then:

1. trim text;
2. validate and normalize every attachment;
3. build file parts;
4. build the user message with file parts first, then the text part;
5. reject empty messages;
6. persist the message;
7. choose a title.

Image-first ordering follows Anthropic's guidance and works for the other
providers too. The UI renders text and attachments in their own blocks, so the
canonical order does not affect the visual layout.

### 4.2 Attachment validation

Keep validation in `app`, not `httpapi`, so the service contract is safe even
if another transport is added later.

Recommended defaults (all configurable constants):

| Limit | Default |
| --- | --- |
| Images per message | 4 |
| Bytes per image | 5 MiB |
| Bytes per message | 8 MiB |
| Bytes per thread | 12 MiB (optional safety valve) |
| HTTP body for this route | 16 MiB |
| Max filename length | 120 runes |

Validation steps:

1. Reject empty `Data`.
2. Normalize the declared MIME type: lowercase, parse parameters, map
   `image/jpg` to `image/jpeg`.
3. Detect the real type from bytes with `http.DetectContentType` (verified to
   return `image/png`, `image/jpeg`, and `image/webp` for valid headers).
4. Reject a declared/detected mismatch instead of trusting the browser.
5. Allow only `image/png`, `image/jpeg`, `image/webp` in v1.
6. Reject `image/gif`, `image/heic`, `image/heif`, `image/svg+xml`, and
   everything else with a clear message.
7. Sanitize the filename: `filepath.Base`, trim, strip control characters,
   truncate to 120 runes.
8. Build `llm.FileFromBytes(data, mime)`, set `Filename`, and leave `Detail`
   empty so OpenAI uses `auto` and the other adapters ignore it.

The optional per-thread image budget is computed from the history already loaded
by `StartMessage`. It prevents unbounded replay requests, especially Gemini's
20 MB inline request limit. If the new message would exceed it, return a clear
`ErrInvalidInput`: "This thread already has X MB of images; start a new thread
for more." This can be dropped if the UX cost is not worth it, but it is cheap
because history is already in memory.

### 4.3 Message construction and title

Use:

```go
parts := make([]llm.Part, 0, len(files)+1)
parts = append(parts, files...)
if text != "" {
    parts = append(parts, llm.NewTextPart(text))
}
message := userMessageWithParts(thread.ID, parts, nowUTC())
```

`llm.UserMessageWithParts` already drops empty text parts and validates content.
An image-only message is valid.

For the default thread title:

- use the trimmed text when present;
- otherwise use the single attachment's filename;
- otherwise use `"N images"`.

### 4.4 HTTP adapter

In `codjent/internal/httpapi/api.go`:

- Add a private request struct with `Content` and `Attachments`.
- Refactor `readJSON` into `readJSONLimit(w, r, target, limit)`; keep the
  existing 1 MiB default and use 16 MiB only for the start-message route.
- Keep `DisallowUnknownFields`.
- Map `*http.MaxBytesError` to `413`.
- Never log attachment bytes or full filenames at info level.

The body cap protects memory before base64 decoding. The per-image and total
byte limits are checked after decoding.

### 4.5 Views

`PartView` already has `Type`, `MIMEType`, `Data`, and `URL`. Add:

```go
Filename string `json:"filename,omitempty"`
```

and populate it from `FilePart.Filename` in `partViews`. The frontend can then
show a filename and use it as alt text.

No store schema, `llm.FormatVersion`, or SQLite migration changes are needed.
The canonical message JSON already contains the file part. Deleting a thread
cascades to its messages, so images are deleted with the thread.

### 4.6 Instructions

Add one line to `defaultInstructions` in `codjent/internal/app/service.go`:

> User messages may include image attachments such as screenshots, diagrams, or
> photos. Inspect them when they are relevant to the request.

This is optional but improves behavior when the user writes "fix this" without
naming the image.

## 5. Frontend design

### 5.1 Types and API

In `codjent/frontend/src/lib/types.ts`:

```ts
export interface Part {
  type: string;
  text?: string;
  mime_type?: string;
  data?: string;
  url?: string;
  filename?: string;
}

export interface AttachmentInput {
  filename?: string;
  mime_type: string;
  data: string; // base64, no data: prefix
}
```

In `codjent/frontend/src/lib/api.ts`, change:

```ts
startMessage(threadID, content, attachments?: AttachmentInput[])
```

and send `{ content, attachments }`. Keep the field omitted when empty so the
wire format stays identical for text-only messages.

### 5.2 Sending flow

Extend the send callback through the existing chain:

- `useCodjent.sendMessage(content, options?, attachments?)`
- `ThreadView` and `EmptyState` prop types
- `Composer.onSend`

`sendMessage` should:

1. allow empty text when at least one attachment exists;
2. build the optimistic message with file parts first, then text, so the
   transcript matches what the server will persist;
3. call `api.startMessage` with the attachments;
4. keep the existing refresh-on-completion path, which replaces the optimistic
   message with the persisted one.

If the request fails, keep the composer attachments so the user can retry.

### 5.3 Composer

Add to `codjent/frontend/src/components/composer.tsx`:

- hidden `<input type="file" accept="image/png,image/jpeg,image/webp" multiple>`;
- an attach button (`Paperclip` or `ImagePlus`) next to the model/access
  controls;
- a pending-attachment list above the textarea with thumbnail, filename,
  size, and remove button;
- drag/drop over the composer card;
- paste handling for clipboard image files;
- inline validation error text;
- send enabled when text or attachments exist.

Store each pending attachment as:

```ts
{
  id: string;
  file: File;
  filename: string;
  mimeType: "image/png" | "image/jpeg" | "image/webp";
  data: string;       // base64
  previewURL: string; // data URL
}
```

Read the file once with `FileReader.readAsDataURL`, then split at the first
comma for the base64 payload. This keeps preview and upload bytes identical and
avoids a second read at send time.

Client-side validation should mirror the server limits and produce an inline
message rather than a failed run. Default pasted filenames to
`pasted-image.png` or the matching extension.

### 5.4 Client-side normalization

Recommended, but can ship one increment after the basic path:

- If an image is larger than 5 MiB or has a long edge over 2048 px, decode it
  with `createImageBitmap`, draw it to a canvas at max 2048 px on the long edge,
  and re-encode it.
- Preserve PNG for PNG sources (screenshots and text stay sharp).
- Use JPEG/WebP quality around 0.9 for photos.
- Re-encoding also strips EXIF metadata.
- If the browser cannot decode the image (for example HEIC), reject it with a
  clear message.

This reduces provider cost and makes Gemini's inline limit much less likely to
be hit.

### 5.5 Transcript

In `codjent/frontend/src/components/transcript.tsx`:

- add a `messageFiles(message)` helper or filter inline;
- change `UserMessage` to render when there is text or at least one file;
- render images in a responsive grid inside the user bubble;
- use `file.data` to build `data:${mime_type};base64,${data}`;
- prefer `file.url` when present;
- use `file.filename` as alt text, falling back to "Attached image";
- use plain `<img>` or Next Image with `unoptimized`; data URLs are not
  optimizable;
- add an optional click-to-enlarge modal. A simple overlay is enough; do not
  navigate to the data URL.

Assistant and tool messages already expose file parts in `PartView`. A small
shared file-part renderer can be used there later; v1 only needs user
attachments to be visible.

## 6. Limits, formats, and provider behavior

### 6.1 Universal v1 formats

Use the intersection of all three vendor image allowlists:

| Format | MIME | v1 |
| --- | --- | --- |
| PNG | `image/png` | yes |
| JPEG | `image/jpeg` | yes |
| WebP | `image/webp` | yes |
| GIF | `image/gif` | no (Google adapter rejects it; animations are problematic) |
| HEIC/HEIF | `image/heic`, `image/heif` | no (only Google supports it in-repo) |
| SVG | `image/svg+xml` | no (script-capable, not a raster input) |

The intersection keeps persisted history portable when the user changes the
thread's provider or model.

### 6.2 What each adapter already does

| Codjent format | Encoding path | Notes |
| --- | --- | --- |
| `openai-responses` | `input_image` with a base64 data URL | Supports inline data, URL, and file ID. `Detail` is `low`, `high`, or `auto`; empty means `auto`. |
| `openai-completion` | `image_url` content part with a base64 data URL | Supports inline data and URL; no image file IDs. |
| `anthropic` | `image` block with a base64 source | Supports inline data, URL, and file ID. JPEG, PNG, GIF, WebP. |
| `google` | `inlineData` part | Supports PNG, JPEG, WebP, HEIC, HEIF. Inline request total is the binding limit. |

Provider docs at the time of writing:

- OpenAI: PNG/JPEG/WebP/non-animated GIF, large per-request payload allowance,
  and detail-dependent image tokenization.
- Anthropic: JPEG/PNG/GIF/WebP, 10 MB base64 per image on the standard API,
  32 MB standard request limit, 8000×8000 px max, stricter 2000 px limit when a
  request contains more than 20 images.
- Gemini: inline image data contributes to a 20 MB total inline request limit;
  use the Files API for larger inputs.

Codjent's v1 caps are far below all of those except Gemini's inline limit,
which is why the per-thread image budget is worth considering.

### 6.3 Model capability caveat

The four adapters currently advertise `Vision: true` for every model ID. The
core runner therefore allows image parts, and a text-only model will fail at
the provider with a normal run error. That is acceptable for v1.

If provider errors become common, add a codjent-only `vision` flag to
`provider.ModelInfo` and surface it in settings and the composer. Do not infer
capabilities from model names.

## 7. Security and privacy

- Codjent is loopback-only and has no authentication. Attachments are only as
  safe as the existing workspace and transcript are; keep the listener
  unexposed.
- Images are stored in the SQLite message JSON and sent to the configured model
  provider on every replay of the conversation. Document this in the README.
- Validate real bytes, not extensions or browser-declared MIME types.
- Reject SVG and other non-raster formats.
- Keep the `0600` database permissions and cascade deletion that already exist.
- Do not log attachment data, base64 payloads, or full request bodies.
- Cap body size before decoding and per-image/total size after decoding to
  avoid memory exhaustion.
- Treat text rendered inside an image as untrusted model input, just like text
  from the user or a tool.
- Render images with `<img>` and data URLs, never with `dangerouslySetInnerHTML`.

## 8. Testing

### App tests (`codjent/internal/app`)

- `StartMessageWith` with text + image persists a user message containing one
  file part followed by one text part.
- Image-only message is accepted and gets an attachment-derived title.
- Empty content with no attachments is rejected.
- Too many attachments, too many bytes per image, too many bytes per message,
  empty data, bad base64, unsupported MIME, and declared/detected mismatch are
  rejected.
- `StartMessage` (text-only wrapper) still behaves exactly as before.
- `GetThread` projects the file part with MIME, data, and filename.
- The scripted model used in app tests must advertise `Vision: true` for image
  cases; otherwise the core correctly rejects the request.
- Optional: a model with `Vision: false` fails with the core's unsupported
  vision error, proving the capability check remains intact.

### HTTP tests (`codjent/internal/httpapi`)

- POST with a base64 image returns `202`; GET thread returns the file part.
- Text-only POST without `attachments` still works.
- Malformed base64 and unknown fields return `400`.
- A body over the route limit returns `413`.
- Attachment count/size/type errors return `400` with stable messages.
- No image bytes appear in run SSE events.

### Store tests

Existing SQLite tests already round-trip `llm.FilePart`; add an HTTP-to-store
integration assertion if the app test does not cover persistence end to end.

### Frontend checks

```sh
cd codjent/frontend
pnpm lint
pnpm exec tsc --noEmit
pnpm build
```

Manual matrix:

- pick 1–4 PNG/JPEG/WebP files;
- drag and drop;
- paste a clipboard screenshot;
- remove an attachment before send;
- send text + images and images alone;
- reload the thread and confirm thumbnails;
- send a follow-up and confirm prior images still reach the model;
- reject too many, too large, GIF, HEIC, and SVG;
- switch to a text-only model and confirm the provider error is surfaced;
- run against OpenAI Responses, OpenAI Chat Completions, Anthropic, and Google.

### Race and lint

```sh
cd codjent
go test -race ./...
golangci-lint run ./...
```

## 9. Implementation checklist

Backend:

- [x] Add `AttachmentInput`, `MessageInput`, limits, and `StartMessageWith` in
      `codjent/internal/app`.
- [x] Add MIME/signature/filename/size validation in a helper file.
- [x] Build file-first user messages and the attachment-aware title.
- [x] Add optional per-thread image budget.
- [x] Add `Filename` to `PartView` and populate it.
- [x] Extend the HTTP request struct and add the per-route body limit.
- [x] Map oversized bodies to `413`.
- [x] Add the image instruction to default instructions.
- [x] Add app and HTTP tests, including a text-only regression.
- [x] Update the scripted test model to advertise vision where needed.

Frontend:

- [x] Add `filename` and `AttachmentInput` to `types.ts`.
- [x] Extend `api.startMessage`.
- [x] Thread attachments through `useCodjent`, `ThreadView`, `EmptyState`, and
      `Composer`.
- [x] Add picker, drag/drop, paste, previews, removal, and validation.
- [x] Build optimistic messages with file parts.
- [x] Render user attachments in the transcript.
- [x] Add client-side downscale/compression if included in the first cut.
- [x] Run lint, TypeScript, and the static build.

Docs:

- [x] Add an "Image attachments" section to `codjent/README.md` with the API
      example, limits, supported formats, provider caveat, and privacy note.
- [x] Update the "Current scope" list.

## 10. Risks and mitigations

| Risk | Mitigation |
| --- | --- |
| Base64 inflates payloads by ~33% | v1 caps plus client-side downscale; revisit multipart or a dedicated upload endpoint if needed. |
| Thread JSON becomes large after many image messages | Per-message/per-thread caps now; add lazy `GET /api/messages/{id}/parts/{index}` or an attachment table later. |
| Provider history replay grows on every turn | Per-thread image budget; future pruning, Files API uploads, or file-ID reuse. |
| Gemini's 20 MB inline request limit | Conservative per-image/thread limits; client-side resize; surface provider errors clearly. |
| Text-only model selected | Run fails with a provider error; optional per-model vision flag later. |
| Screenshot text becomes unreadable after compression | Preserve PNG for PNG sources; cap by long edge rather than aggressive JPEG compression. |
| Unsupported format from a phone or design tool | Reject with a specific message and allow browser-decodable formats to be converted to PNG/JPEG client-side. |
| No auth on the server | Keep loopback binding and origin allowlist; document that images leave the machine for the provider. |

## 11. Future extensions

1. **Documents**: allow PDF and plain text once provider-specific capability
   rules are modeled. OpenAI Chat Completions cannot send non-image file parts;
   Responses, Anthropic, and Google can.
2. **Lazy attachments**: serve bytes from
   `GET /api/messages/{messageID}/parts/{index}` and omit inline `data` from
   thread detail. This is the clean fix if transcripts grow past a few MB.
3. **Provider Files API**: upload once and persist `FileID`/File URL instead of
   replaying base64 on every turn.
4. **Per-model capabilities**: add `vision`, `documents`, and size hints to
   codjent's `ModelInfo` so the composer can prevent invalid sends.
5. **Image output**: render assistant/tool file parts as first-class artifacts,
   with download and preview.
6. **History pruning**: replace old images in the model request with text
   placeholders while keeping the full transcript for display.

## 12. Recommended decisions

- Inline base64 JSON in v1; no upload endpoint, no schema change.
- Universal MIME set of PNG, JPEG, and WebP.
- Images before text in canonical content.
- 4 images, 5 MiB each, 8 MiB per message, 16 MiB route body.
- Client-side downscale to 2048 px long edge, preserving PNG for screenshots.
- No core or provider adapter changes.
