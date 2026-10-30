# Gojent

Gojent is a small, strongly typed agent runtime for Go. It gives you one
streaming-first execution loop, typed tools, provider-neutral messages, and a
minimal persistence contract. Everything else is optional.

- **Small core.** Four packages: `agent`, `llm`, `tool`, and `session`.
- **Zero third-party dependencies** in the root module.
- **Streaming first.** `Generate` and `Stream` are adapters over the same loop.
- **Typed by default.** Tool input and output are Go types; JSON Schema is
  generated and validated before a handler runs.
- **No workflow engine.** Go is the workflow language. Compose agents with
  `agent.AsTool`, ordinary functions, and goroutines.

## Install

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
	})
	if err != nil {
		log.Fatal(err)
	}

	response, err := assistant.GenerateWith(ctx, "What is the weather in Tokyo?", agent.RunOptions{
		Store:   session.NewMemory(),
		Session: &session.Session{ID: "demo", ResourceID: "user-1"},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(response.Text)
}
```

### Streaming

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

`Run.Events` is an observation stream. A `RunEnd` event is always last. The
agent prefers a model's `Stream` method when it implements `llm.Streamer`.

## Packages

| Package | Responsibility |
| --- | --- |
| `core/agent` | Immutable `Agent` behavior and its execution adapters. |
| `core/llm` | `Model`, canonical `Message`/`Request`/`Response`, streams, errors. |
| `core/tool` | `Tool`, typed `Config`, JSON Schema generation and validation. |
| `core/session` | `Session` identity, the two-method `Store` contract, `Memory`. |
| `tools/...` | Ready-made filesystem, command, web, and task tools. |
| `providers/mockllm` | Deterministic model for tests and examples (part of the root module). |
| `providers/anthropic` | Anthropic Messages adapter module. |
| `providers/google` | Google Gemini adapter module. |
| `providers/openai` | OpenAI Responses and Chat Completions adapter module. |
| `stores/sqlite` | Durable SQLite session store module. |
| `stores/postgres` | Durable PostgreSQL session store module. |
| `examples/...` | Runnable examples; `examples/google` is its own module. |

### Interfaces only at the seams

The core exposes four extension points:

- `llm.Model` (optionally `llm.Streamer`); `Info()` is required
- `tool.Tool`
- `session.Store`
- `agent.Policy`, `agent.Approver`, and `agent.Sink`

The execution loop itself is private, not an interface. `Generate` and
`Stream` are the public adapters. Middleware such as tracing, caching, or
redaction belongs in wrappers around these seams.

## Sessions and stores

A store has exactly two operations:

```go
type Store interface {
	Load(ctx context.Context, session Session) ([]llm.Message, error)
	Append(ctx context.Context, session Session, messages ...llm.Message) error
}
```

`Append` is atomic. The agent persists an assistant tool-call message and all
of its tool results in one call, so a crash cannot leave a transcript with
unanswered tool calls. Durability features such as checkpoints, leases, and
search belong in store modules, not in the core.

`session.Memory` is built in for tests and ephemeral applications. Use
`stores/sqlite` or `stores/postgres` when transcripts must survive a restart.

A store can be configured once on the agent (`agent.Config.Store`). The session
identity remains per-run, which keeps tenant scoping explicit.

## Tools

Tools receive validated, typed input:

```go
type SearchInput struct {
	Query string `json:"query" description:"Search query"`
	Limit int    `json:"limit,omitempty" description:"Maximum results"`
}

search := tool.MustNew(tool.Config[SearchInput, []string]{
	ID:          "search",
	Description: "Search the knowledge base.",
	Annotations: tool.Annotations{ReadOnly: true},
	Execute: func(ctx context.Context, in SearchInput) ([]string, error) {
		return nil, nil
	},
})
```

The constructor generates a JSON Schema from the input type. The agent
validates model arguments before executing the handler and reports validation
failures back to the model as tool errors. Tool panics, timeouts, and
cancellations are contained by the runtime.

Ready-made bundles live in `tools`:

```go
import (
	"github.com/ijaidev/gojent/tools"
	"github.com/ijaidev/gojent/tools/fs"
)

fsTools, _ := tools.StandardFilesystemTools(fs.Config{BaseDir: "/workspace"})
```

## Policies and approvals

Approval is a policy decision, not a model-invoked tool. That keeps a model
from skipping or spoofing the check.

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
		// Show the call to a human and return their decision.
		return confirmWithUser(req.Call), nil
	}),
}

response, err := assistant.GenerateWith(ctx, "Clean up the build directory.", opts)
```

`Ask` without an approver is denied. `Terminate` aborts the run. `RunOptions`
is the only public per-run configuration type; execution internals are not
exported.

## Agent defaults and run overrides

Behavior that should apply to every run belongs on `agent.Config`. A run
overrides it with `RunOptions`; unset values inherit the agent configuration.

```go
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
and `RetryPolicy` merge field by field, so overriding one field keeps the
others. `Session` is always per-run; an agent `Store` still requires a session.
`Sink` and `Media` stay per-run because they belong to a specific execution.

`(*Agent).ApplyAgentOptions` applies the same layering to a `RunOptions` value.
The runtime calls it for every run, so custom execution engines can reuse it
instead of duplicating the merge rules.

## Subagents

`Agent.AsTool` exposes an agent as a typed tool. Composition is ordinary Go
code:

```go
researcher, _ := agent.New(agent.Config{
	Name:        "researcher",
	Description: "Finds primary sources",
	Model:       model,
})

writer, _ := agent.New(agent.Config{
	Name:  "writer",
	Model: model,
	Tools: []tool.Tool{researcher.AsTool()},
})
```

There is no sequential, parallel, or loop DSL in the core. Use Go.

## Observability

Implement `agent.Sink` or pass `agent.SinkFunc` to receive `RunStart`,
`StepStart`, `TextDelta`, `ThinkingDelta`, `ToolStart`, `ToolEnd`, and
`RunEnd` events. Policies observe and decide; sinks only observe.

## Development

The repository is a Go workspace with a root module and several optional
modules. `go.work` is intentionally not tracked; create it once after cloning:

```bash
just workspace
```

Each module has its own `justfile`. The root recipes delegate to every module:

```bash
just test    # unit tests
just lint    # static analysis
just tidy    # go mod tidy
just check   # formatting, tidy check, lint, and tests
```

Run a single module's recipe with `just --justfile <dir>/justfile <recipe>`,
for example:

```bash
just --justfile providers/google/justfile test
```

Shared conformance suites catch adapter drift:

- `core/llm/llmtest` checks that generation and streaming agree on text,
  reasoning, tool calls, usage, and finish reasons.
- `core/session/sessiontest` checks ordering, tenant isolation, atomic append,
  replay state, validation, and cancellation.

The PostgreSQL integration suite is opt-in:

```bash
GOJENT_POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/gojent_test?sslmode=disable' \
  just --justfile stores/postgres/justfile test-integration
```

## Versioning

The root module and each optional module are versioned independently. Tag a
module release with its directory prefix so `go get` can resolve it:

```bash
git tag providers/google/v0.1.0
git tag stores/sqlite/v0.1.0
git tag stores/postgres/v0.1.0
```

The optional modules carry `replace` directives that resolve the root module
from this checkout during local development. Remove them in the release commit
before tagging.

## Non-goals

Gojent deliberately does not include:

- a workflow engine, graph DSL, or node editor;
- a UI, hosted service, or control plane;
- a vector database or retrieval framework;
- provider-specific concepts in the core;
- mandatory third-party dependencies in the root module.

Optional features belong in separate modules that you fetch with `go get`.

## Repository layout

```
core/agent         immutable agents, run options, policies, events
core/llm           model contract, messages, streams, errors
core/tool          typed tools, schemas, validation, results
core/session       session identity, store contract, memory store
tools              filesystem, command, web, and task tools
providers/anthropic Anthropic Messages adapter (separate module)
providers/google   Gemini adapter (separate module)
providers/openai   OpenAI Responses and Chat Completions adapter (separate module)
providers/mockllm  deterministic test model
stores/sqlite      SQLite store (separate module)
stores/postgres    PostgreSQL store (separate module)
examples           runnable demonstrations
```

## License

Apache 2.0. See [LICENSE](LICENSE).
