# Gojent

[![Go Reference](https://pkg.go.dev/badge/github.com/ijaidev/gojent.svg)](https://pkg.go.dev/github.com/ijaidev/gojent)
[![CI](https://github.com/ijaidev/gojent/actions/workflows/ci.yml/badge.svg)](https://github.com/ijaidev/gojent/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

**A small, strongly typed agent runtime for Go.**

Gojent gives you one streaming-first execution loop, typed tools,
provider-neutral messages, and a minimal persistence contract. There is no
workflow engine and no hidden state: an agent is a Go value, a run is a method
call, and composition is ordinary Go code.

- **Small core.** Four packages: `agent`, `llm`, `tool`, and `session`.
  Zero third-party dependencies in the root module.
- **Typed tools.** Tool input and output are Go types. JSON Schema is generated
  and validated before your handler runs.
- **Streaming first.** `Generate` and `Stream` are adapters over the same loop.
- **Provider neutral.** OpenAI, Anthropic, and Gemini adapters live in separate
  modules behind one `llm.Model` interface.
- **No framework lock-in.** Policies, stores, observability, and providers are
  small interfaces you can implement yourself.

The repository also ships [codjent](#reference-app-codjent), a sample coding
agent that demonstrates the runtime in a real application.

## Contents

- [Install](#install)
- [Quickstart](#quickstart)
- [Reference app: codjent](#reference-app-codjent)
- [Design](#design)
- [Packages](#packages)
- [Tools](#tools)
- [Streaming](#streaming)
- [Sessions and stores](#sessions-and-stores)
- [Policies and approvals](#policies-and-approvals)
- [Structured output](#structured-output)
- [Subagents](#subagents)
- [Observability](#observability)
- [Models and providers](#models-and-providers)
- [Extending](#extending)
- [Repository layout](#repository-layout)
- [Development](#development)
- [Versioning](#versioning)
- [Non-goals](#non-goals)
- [License](#license)

## Install

Go 1.25.5 or newer is required.

```bash
go get github.com/ijaidev/gojent@latest
```

Optional providers and stores are separate modules:

```bash
go get github.com/ijaidev/gojent/providers/anthropic
go get github.com/ijaidev/gojent/providers/google
go get github.com/ijaidev/gojent/providers/openai
go get github.com/ijaidev/gojent/stores/sqlite
go get github.com/ijaidev/gojent/stores/postgres
```

## Quickstart

This program needs no API key and no modules beyond the root package. It uses
the deterministic mock model that ships with Gojent:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/providers/mockllm"
)

func main() {
	model := mockllm.New("Gojent is a small, typed agent runtime for Go.")

	assistant, err := agent.New(agent.Config{
		Name:         "assistant",
		Instructions: "Answer in one sentence.",
		Model:        model,
	})
	if err != nil {
		log.Fatal(err)
	}

	response, err := assistant.Generate(context.Background(), "What is Gojent?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(response.Text)
}
```

```text
Gojent is a small, typed agent runtime for Go.
```

### A real model and a tool

Swap the mock for a provider, define a tool from Go types, and the runtime runs
the model/tool loop until the model answers:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/core/tool"
	"github.com/ijaidev/gojent/providers/google"
)

type WeatherInput struct {
	City string `json:"city" description:"City name, e.g. Tokyo"`
}

type WeatherOutput struct {
	City         string  `json:"city"`
	TemperatureC float64 `json:"temperature_c"`
}

func main() {
	ctx := context.Background()

	model, err := google.New(ctx, google.Config{
		Model:  "gemini-2.5-flash",
		APIKey: os.Getenv("GEMINI_API_KEY"),
	})
	if err != nil {
		log.Fatal(err)
	}

	weather := tool.MustNew(tool.Config[WeatherInput, WeatherOutput]{
		ID:          "get_weather",
		Description: "Get the current weather for a city.",
		Annotations: tool.Annotations{ReadOnly: true},
		Execute: func(_ context.Context, in WeatherInput) (WeatherOutput, error) {
			return WeatherOutput{City: in.City, TemperatureC: 21.5}, nil
		},
	})

	assistant, err := agent.New(agent.Config{
		Name:         "assistant",
		Description:  "A concise weather assistant",
		Instructions: "Use the weather tool when asked about current conditions.",
		Model:        model,
		Tools:        []tool.Tool{weather},
		Store:        session.NewMemory(),
	})
	if err != nil {
		log.Fatal(err)
	}

	response, err := assistant.Generate(ctx, "What is the weather in Tokyo?",
		agent.WithSession("demo", "user-1"),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(response.Text)
}
```

Install the provider module first:

```bash
go get github.com/ijaidev/gojent/providers/google
export GEMINI_API_KEY="..."
```

See [Models and providers](#models-and-providers) for OpenAI and Anthropic, and
[`examples/`](examples) for runnable versions of these programs.

## Reference app: codjent

[Codjent](codjent/README.md) is a sample coding agent built on Gojent: a Go
backend, an embedded Next.js UI, SQLite-backed threads, streaming runs, tool
calls, and approval prompts.

It exists to show what the runtime looks like when assembled into a real
application. **It is a sample, not a supported product** — its scope is
deliberately small, and it has no authentication, multi-tenant isolation, or
deployment story. Treat it as a reference, not a platform.

```bash
go install github.com/ijaidev/gojent/codjent/cmd/codjent@latest
codjent
```

Then open <http://127.0.0.1:8787>. The frontend is embedded in the binary, so no
extra files are needed. Configure a provider under `/settings` on first run, or
start with `GEMINI_API_KEY` set to seed a Google provider.

Codjent is its own Go module and is versioned separately with `codjent/vX.Y.Z`
tags. If it is useful, copy the parts you need from
[`codjent/internal`](codjent/internal) into your own application.

## Design

Gojent is intentionally small. The core is four packages and no third-party
dependencies; adapters add weight only when you import them.

### Interfaces only at the seams

The core exposes four extension points:

- `llm.Model` (optionally `llm.Streamer`); `Info()` is required
- `tool.Tool`
- `session.Store`
- `agent.Policy`, `agent.Approver`, and `agent.Sink`

The execution loop itself is private, not an interface. `Generate` and `Stream`
are the public adapters. Middleware such as tracing, caching, or redaction
belongs in wrappers around these seams.

### Immutable agents, per-run configuration

An `agent.Agent` is reusable behavior: instructions, a model, tools, and
defaults. A run cannot mutate it. Per-run state lives in `agent.RunOptions`,
which is also the only public per-run configuration type.

`Generate` and `Stream` accept functional options for convenience. `GenerateWith`
and `StreamWith` accept a complete `RunOptions` value when you want to build one
explicitly.

### Fail closed

Without a policy, tools are allowed. Once a policy is installed, it fails closed:
an unrecognized decision is treated as deny, and an `Ask` decision without an
approver is denied. Providers reject requests they cannot honor with
`llm.ErrUnsupported` instead of silently dropping options.

### Atomic persistence

A store has exactly two methods, and `Append` is atomic. The runtime persists an
assistant tool-call message and all of its tool results in one call, so a crash
cannot leave a transcript with unanswered tool calls.

## Packages

| Package | Responsibility |
| --- | --- |
| [`core/agent`](core/agent) | Immutable `Agent` behavior and its execution adapters. |
| [`core/llm`](core/llm) | `Model`, canonical `Message`/`Request`/`Response`, streams, errors. |
| [`core/tool`](core/tool) | `Tool`, typed `Config`, JSON Schema generation and validation. |
| [`core/session`](core/session) | `Session` identity, the two-method `Store` contract, `Memory`. |
| [`tools`](tools) | Ready-made filesystem, command, web, and task tools. |
| [`providers/mockllm`](providers/mockllm) | Deterministic model for tests and examples (root module). |
| [`providers/anthropic`](providers/anthropic) | Anthropic Messages adapter (separate module). |
| [`providers/google`](providers/google) | Google Gemini adapter (separate module). |
| [`providers/openai`](providers/openai) | OpenAI Responses and Chat Completions adapter (separate module). |
| [`stores/sqlite`](stores/sqlite) | Durable SQLite session store (separate module). |
| [`stores/postgres`](stores/postgres) | Durable PostgreSQL session store (separate module). |
| [`examples`](examples) | Runnable examples; `examples/google` is its own module. |

## Tools

A tool is a typed function plus metadata. The constructor generates a JSON
Schema from the input type, and the runtime validates model arguments against
it before executing your handler:

```go
type SearchInput struct {
	Query string `json:"query" description:"Search query"`
	Limit int    `json:"limit,omitempty" description:"Maximum results, 1-20"`
}

type SearchResult struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

search := tool.MustNew(tool.Config[SearchInput, SearchResult]{
	ID:          "search",
	Description: "Search the knowledge base.",
	Annotations: tool.Annotations{ReadOnly: true, Idempotent: true},
	Execute: func(ctx context.Context, in SearchInput) (SearchResult, error) {
		return SearchResult{Title: "Example", URL: "https://example.com"}, nil
	},
})
```

Struct tags drive the schema:

- `json` sets the property name and `omitempty` marks it optional;
- `description` becomes the JSON Schema description shown to the model;
- `enum` constrains accepted values, for example `enum:"low,medium,high"`.

Use `tool.New` when you want an error, or `tool.MustNew` when a failure to build
a tool is a programming mistake and should panic at startup.

### What the runtime guarantees

- Invalid arguments are reported back to the model as a tool error; the handler
  never runs.
- Panics, timeouts, and context cancellation are contained by the runtime and
  surfaced as tool errors. The default tool timeout is two minutes; set
  `agent.Config.ToolTimeout` or `agent.WithToolTimeout` to change it. A negative
  value disables the timeout.
- Tool results are persisted with the assistant message that requested them.

### Annotations

`tool.Annotations` describes side effects so policies can make decisions without
knowing the implementation:

```go
type Annotations struct {
	ReadOnly    bool // does not mutate state
	Destructive bool // may delete or overwrite data
	Idempotent  bool // repeating the call has the same effect
	OpenWorld   bool // talks to an unbounded external system
}
```

### Results and multimodal output

Returning a typed value makes it available as structured `Data` and as JSON text
for the model. To return files or media, use `tool.Result`:

```go
tool.MustNew(tool.Config[struct{}, tool.Result]{
	ID: "read_image",
	Execute: func(ctx context.Context, _ struct{}) (tool.Result, error) {
		return tool.Result{Content: []llm.Part{llm.NewTextPart("rendered")}}, nil
	},
})
```

### Ready-made tools

The root [`tools`](tools) package bundles the common tool sets:

```go
import (
	"github.com/ijaidev/gojent/tools"
	"github.com/ijaidev/gojent/tools/exec"
	"github.com/ijaidev/gojent/tools/fs"
	"github.com/ijaidev/gojent/tools/web"
)

fsTools, err := tools.StandardFilesystemTools(fs.Config{BaseDir: "/workspace"})
webTools, err := tools.StandardWebTools(web.FetchConfig{})
execTools, err := tools.StandardExecTools(exec.Config{BaseDir: "/workspace"})
tracker, taskTools, err := tools.StandardTaskTools()
_ = tracker
```

Filesystem and command tools are sandboxed to `BaseDir` when one is set. Command
execution is a destructive capability: gate it with a policy or approver before
giving it to a model.

## Streaming

`Stream` starts a run and returns a handle. Events are observations; they cannot
change execution. `RunEnd` is always the last event.

```go
run, err := assistant.Stream(ctx, "Explain Go interfaces briefly.",
	agent.WithStore(session.NewMemory()),
	agent.WithSession("demo", "user-1"),
)
if err != nil {
	log.Fatal(err)
}

for event := range run.Events() {
	switch event := event.(type) {
	case agent.TextDelta:
		fmt.Print(event.Text)
	case agent.RunEnd:
		if event.Err != nil {
			log.Print(event.Err)
		}
	}
}

response, err := run.Wait(ctx)
```

`Run.Events` returns a channel that closes after `RunEnd`. `Wait` does not drain
events, so a consumer that ignores `Events` and never cancels the run may block
the dispatcher. Either consume events or call `Cancel` when you abandon a run.

The runtime uses a model's `Stream` implementation when it also implements
`llm.Streamer`, and falls back to `Generate` otherwise. `TextDelta.Replace`
reports that `Text` is a full snapshot of the part rather than an increment.

## Sessions and stores

A `session.Session` identifies a conversation and its owner. A store has exactly
two operations:

```go
type Store interface {
	Load(ctx context.Context, session Session) ([]llm.Message, error)
	Append(ctx context.Context, session Session, messages ...llm.Message) error
}
```

Durability features such as checkpoints, leases, search, and conversation
metadata belong in store modules or your application, not in the core.

`session.Memory` is built in for tests and ephemeral applications. Use
`stores/sqlite` or `stores/postgres` when transcripts must survive a restart:

```go
import (
	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/stores/sqlite"
)

store, err := sqlite.New("file:gojent.db")
if err != nil {
	log.Fatal(err)
}
defer store.Close()

assistant, err := agent.New(agent.Config{
	Name:  "assistant",
	Model: model,
	Store: store,
})

response, err := assistant.Generate(ctx, "Continue where we left off.",
	agent.WithSession("thread-1", "user-1"),
)
```

`stores/sqlite` uses `mattn/go-sqlite3` and requires CGO to be enabled at build
time.

The session identity is always per-run, even when the store is configured once on
the agent. That keeps tenant scoping explicit. `ResourceID` isolates
conversations that happen to share an `ID`.

## Policies and approvals

Approval is a policy decision, not a model-invoked tool. A model cannot skip or
spoof the check.

```go
opts := agent.RunOptions{
	Policy: agent.Hooks{
		OnBeforeTool: func(_ context.Context, req *agent.ToolRequest) (agent.Decision, error) {
			if req.Spec.Annotations.Destructive {
				return agent.Ask, nil
			}
			return agent.Allow, nil
		},
	},
	Approver: agent.ApproverFunc(func(_ context.Context, req agent.ToolRequest) (bool, error) {
		// Show req.Call and req.Spec to a human and return their decision.
		return confirmWithUser(req.Call), nil
	}),
}

response, err := assistant.GenerateWith(ctx, "Clean up the build directory.", opts)
```

The decisions are:

| Decision | Effect |
| --- | --- |
| `agent.Allow` | Execute the tool. |
| `agent.Deny` | Skip the tool and report the denial to the model. |
| `agent.Ask` | Require the approver. Without one, the call is denied. |
| `agent.Terminate` | Abort the run. |

A full `Policy` also has `BeforeModel` and `AfterModel` hooks, which can reject
or observe model requests and responses. `agent.PolicyChain` composes policies in
order; `agent.Hooks` is the function-field implementation for the common case.

### Agent defaults and run overrides

Behavior that should apply to every run belongs on `agent.Config`. A run
overrides it with `RunOptions`; unset values inherit the agent configuration.

```go
temperature := 0.2

assistant, _ := agent.New(agent.Config{
	// ...
	Approver:    approver,
	Store:       session.NewMemory(),
	Budget:      agent.Budget{MaxSteps: 16, MaxDuration: 10 * time.Minute},
	Retry:       agent.RetryPolicy{MaxAttempts: 3},
	ToolTimeout: 30 * time.Second,
	Temperature: &temperature,
})

// This run raises only the step limit. The duration cap, store, approver,
// retry policy, and tool timeout still apply.
response, err := assistant.Generate(ctx, "Analyze this repository.",
	agent.WithMaxSteps(32),
	agent.WithSession("review-1", "user-1"),
)
```

Precedence is package defaults, then `agent.Config`, then `RunOptions`. `Budget`
and `RetryPolicy` merge field by field, so overriding one field keeps the others.
`Session` is always per-run; an agent `Store` still requires a session. `Sink` and
`Media` stay per-run because they belong to a specific execution.

Useful defaults:

- `agent.DefaultMaxSteps` is 8 when `Budget.MaxSteps` is zero.
- `agent.DefaultToolTimeout` is 2 minutes.
- `RetryPolicy.MaxAttempts` counts total attempts, including the first. Zero or
  one disables retries.

`(*Agent).ApplyAgentOptions` applies the same layering to a `RunOptions` value.
The runtime calls it for every run, so custom execution engines can reuse it
instead of duplicating the merge rules.

## Structured output

`GenerateStructured` constrains the response to a JSON Schema generated from `T`
and decodes it for you:

```go
type WeatherReport struct {
	City        string   `json:"city" description:"City name"`
	Temperature float64  `json:"temperature_c" description:"Current temperature in Celsius"`
	Conditions  []string `json:"conditions" description:"Short condition labels"`
}

report, response, err := agent.GenerateStructured[WeatherReport](
	ctx, assistant, "Report the weather in Tokyo.",
)
if err != nil {
	log.Fatal(err)
}
fmt.Printf("%s: %.1f°C\n", report.City, report.Temperature)
```

The same behavior is available as a run option:

```go
response, err := assistant.Generate(ctx, "Report the weather in Tokyo.",
	agent.WithOutputSchema[WeatherReport](),
)
var report WeatherReport
err = response.Unmarshal(&report)
```

`Response.Unmarshal` tolerates a single Markdown JSON code fence around the
output. Use `WithOutputSchema[T](opts...)` with `tool.SchemaOption` values when
you need to customize schema generation. Providers that cannot enforce a strict
schema fail with `llm.ErrUnsupported` rather than silently relaxing the request;
the Anthropic adapter currently does not advertise strict JSON Schema support.

## Subagents

`Agent.AsTool` exposes an agent as a typed tool. Composition is ordinary Go code:

```go
researcher, _ := agent.New(agent.Config{
	Name:         "researcher",
	Description:  "Finds primary sources",
	Instructions: "Find and cite primary sources.",
	Model:        model,
})

writer, _ := agent.New(agent.Config{
	Name:         "writer",
	Instructions: "Draft an article using the researcher's findings.",
	Model:        model,
	Tools:        []tool.Tool{researcher.AsTool()},
})
```

Use `AsToolWith` to override the tool name or description, or to give the
subagent its own `RunOptions`:

```go
researcher.AsToolWith(agent.AsToolConfig{
	Name:        "research",
	Description: "Research a topic and return sources.",
	Options:     agent.RunOptions{Budget: agent.Budget{MaxSteps: 12}},
})
```

There is no sequential, parallel, or loop DSL in the core. Use goroutines,
`errgroup`, and ordinary functions.

## Observability

Implement `agent.Sink` or pass `agent.SinkFunc` to receive run events without
affecting execution:

```go
sink := agent.SinkFunc(func(event agent.Event) {
	switch event := event.(type) {
	case agent.TextDelta:
		fmt.Print(event.Text)
	case agent.ToolStart:
		log.Printf("tool %s: %s", event.Call.Name, event.Call.Arguments)
	case agent.ToolEnd:
		log.Printf("tool %s finished with error: %v", event.Call.Name, event.Err)
	}
})

response, err := assistant.Generate(ctx, "Summarize this repository.",
	agent.WithSink(sink),
)
```

Events include `RunStart`, `StepStart`, `MessageStart`, `TextDelta`,
`ThinkingDelta`, `ToolCallDelta`, `MessageEnd`, `ToolStart`, `ToolUpdate`,
`ToolEnd`, and `RunEnd`. `ToolStart`/`ToolEnd` are paired, including for denied,
failed, or policy-aborted calls. `ToolUpdate` is reserved for tools and
schedulers that surface progress; the current runner does not emit it. Policies
observe and decide; sinks only observe. A panicking sink does not prevent other
sinks from receiving the event.

For metrics, wrap the seams instead: an `llm.Model` wrapper can record latency
and token usage, and a `session.Store` wrapper can record persistence failures.

## Models and providers

| Provider | Module | Constructor | Credentials |
| --- | --- | --- | --- |
| Google Gemini | `providers/google` | `google.New` | `GEMINI_API_KEY` or `GOOGLE_API_KEY` |
| OpenAI Responses | `providers/openai` | `openai.New` | `OPENAI_API_KEY` |
| OpenAI Chat Completions | `providers/openai` | `openai.NewChatCompletion` | `OPENAI_API_KEY` |
| Anthropic Messages | `providers/anthropic` | `anthropic.New` | `ANTHROPIC_API_KEY` |
| Deterministic mock | `providers/mockllm` | `mockllm.New` | none |

```go
import (
	"github.com/ijaidev/gojent/providers/anthropic"
	"github.com/ijaidev/gojent/providers/google"
	"github.com/ijaidev/gojent/providers/openai"
)

gemini, err := google.New(ctx, google.Config{
	Model:  "gemini-2.5-flash",
	APIKey: os.Getenv("GEMINI_API_KEY"),
})

gpt, err := openai.New(ctx, openai.Config{
	Model:  "gpt-5",
	APIKey: os.Getenv("OPENAI_API_KEY"),
})

claude, err := anthropic.New(ctx, anthropic.Config{
	Model:  "claude-sonnet-4-5",
	APIKey: os.Getenv("ANTHROPIC_API_KEY"),
})
```

Every adapter accepts extra request headers. OpenAI and Anthropic also accept a
custom base URL for OpenAI-compatible gateways, proxies, or local servers; Google
accepts a pre-configured `ClientConfig` or `Client` instead. Header maps are
copied at construction; `HeaderFunc` computes per-call headers from `ctx` when
the value must change between runs.

Capabilities are explicit. `llm.Info` reports limits, features, and pricing, and
the runner rejects a request the model cannot honor. If you configure reasoning,
vision, or structured output, check `Features` first or let the adapter return
`llm.ErrUnsupported`.

## Extending

### A custom model

Implement three methods:

```go
type Model interface {
	ModelID() string
	Info() llm.Info
	Generate(ctx context.Context, req *llm.Request) (*llm.Response, error)
}
```

Implement `llm.Streamer` as well to opt into incremental generation:

```go
type Streamer interface {
	Model
	Stream(ctx context.Context, req *llm.Request) (llm.Stream, error)
}
```

`Info` must be accurate: the runner checks it before building a request instead
of probing for optional behavior.

### A custom store

Implement `Load` and atomic `Append`. Return messages in chronological order,
isolate `ResourceID`, and deep-copy values if your backend reuses buffers. The
conformance suite in [`core/session/sessiontest`](core/session/sessiontest)
checks ordering, tenant isolation, atomic append, replay state, validation, and
cancellation:

```go
func TestStore(t *testing.T) {
	sessiontest.Run(t, func(t *testing.T) session.Store {
		return newTestStore(t)
	})
}
```

The same pattern is available for model adapters in
[`core/llm/llmtest`](core/llm/llmtest), which checks that generation and
streaming agree on text, reasoning, tool calls, usage, and finish reasons.

### Middleware

Wrap a seam instead of adding hooks to the core:

```go
type loggingModel struct {
	next llm.Model
}

func (m loggingModel) ModelID() string   { return m.next.ModelID() }
func (m loggingModel) Info() llm.Info    { return m.next.Info() }
func (m loggingModel) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	start := time.Now()
	resp, err := m.next.Generate(ctx, req)
	log.Printf("model=%s duration=%s err=%v", m.next.ModelID(), time.Since(start), err)
	return resp, err
}
```

Because `llm.Model` is a small interface, wrappers compose without the runtime
knowing about them.

## Repository layout

```text
core/agent          immutable agents, run options, policies, events
core/llm            model contract, messages, streams, errors
core/tool           typed tools, schemas, validation, results
core/session        session identity, store contract, memory store
tools               filesystem, command, web, and task tools
providers/anthropic Anthropic Messages adapter (separate module)
providers/google    Gemini adapter (separate module)
providers/openai    OpenAI Responses and Chat Completions adapter (separate module)
providers/mockllm   deterministic test model
stores/sqlite       SQLite store (separate module)
stores/postgres     PostgreSQL store (separate module)
codjent             sample coding agent (separate module)
examples            runnable demonstrations
```

## Development

The repository is a Go workspace with a root module and several optional
modules. `go.work` is intentionally not tracked; create it once after cloning.
See [`CONTRIBUTING.md`](CONTRIBUTING.md) for module, release, commit, and
pull-request workflow.

```bash
just workspace
```

Each module has its own `justfile`. The root recipes delegate to every module:

```bash
just test            # unit tests
just vet             # go vet
just build           # go build
just lint            # static analysis
just tidy            # go mod tidy
just frontend-check  # rebuild codjent UI and verify committed assets
just check           # formatting, tidy, vet, build, lint, tests, and frontend
```

Run a single module's recipe with `just --justfile <dir>/justfile <recipe>`, for
example:

```bash
just --justfile providers/google/justfile test
```

The PostgreSQL integration suite is opt-in:

```bash
GOJENT_POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/gojent_test?sslmode=disable' \
  just --justfile stores/postgres/justfile test-integration
```

### Conformance suites

Shared suites catch adapter drift:

- [`core/llm/llmtest`](core/llm/llmtest) checks that generation and streaming
  agree on text, reasoning, tool calls, usage, and finish reasons.
- [`core/session/sessiontest`](core/session/sessiontest) checks ordering, tenant
  isolation, atomic append, replay state, validation, and cancellation.

Every provider and store module includes the relevant suite. The PostgreSQL
store runs its conformance checks as part of the opt-in integration tests.

## Versioning

The root module and each optional module are versioned independently. See
[`CHANGELOG.md`](CHANGELOG.md) for released changes to the root module; adapter
and codjent release notes belong with their own tags. Tag the root module as
`vX.Y.Z` and every submodule with its directory prefix, so `go get` can resolve
each one:

```bash
git tag v0.2.0                    # root module
git tag providers/google/v0.2.0
git tag providers/openai/v0.2.0
git tag providers/anthropic/v0.2.0
git tag stores/sqlite/v0.2.0
git tag stores/postgres/v0.2.0
git tag codjent/v0.2.0
```

Optional modules require a released root module version in their `go.mod`. When
the root module changes, bump that requirement (and tidy) before tagging the
dependent modules, otherwise consumers resolve an older core.

Until v1, minor versions may contain breaking changes. Adapter modules follow the
same rule.

## Non-goals

The core deliberately does not include:

- a workflow engine, graph DSL, or node editor;
- a UI or hosted service (codjent is an optional sample outside the core);
- a vector database or retrieval framework;
- provider-specific concepts in the core;
- mandatory third-party dependencies in the root module.

Optional features belong in separate modules that you fetch with `go get`.

## License

Apache 2.0. See [LICENSE](LICENSE).
