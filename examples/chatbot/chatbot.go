// Package chatbot provides an interactive, streaming, and stateful chatbot
// that can be embedded into any provider example or application.
package chatbot

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
	"github.com/ijaidev/gojent/core/tool"
)

// DefaultInstructions is the default system prompt guiding the chatbot.
const DefaultInstructions = "You are a friendly, concise, and helpful AI assistant. Answer user questions clearly and accurately."

// Config defines the configuration options for a chatbot session.
type Config struct {
	AgentID      string
	AgentName    string
	Instructions string
	Storage      storage.Storage
	ThreadID     string
	ResourceID   string
	Tools        []tool.Tool
	Input        io.Reader
	Output       io.Writer
	Banner       string
	PromptPrefix string
	ExitCommands []string
}

// Option configures a chatbot session.
type Option func(*Config)

// WithAgentID sets the identifier for the underlying agent.
func WithAgentID(id string) Option {
	return func(c *Config) {
		if id != "" {
			c.AgentID = id
		}
	}
}

// WithAgentName sets the human-readable display name of the agent.
func WithAgentName(name string) Option {
	return func(c *Config) {
		if name != "" {
			c.AgentName = name
		}
	}
}

// WithInstructions sets the system prompt instructions guiding the chatbot.
func WithInstructions(instructions string) Option {
	return func(c *Config) {
		if instructions != "" {
			c.Instructions = instructions
		}
	}
}

// WithStorage sets the storage backend used to persist conversation threads and history.
// If not set, an in-memory storage instance is automatically used.
func WithStorage(s storage.Storage) Option {
	return func(c *Config) {
		c.Storage = s
	}
}

// WithThreadID sets the thread ID for multi-turn conversation memory.
func WithThreadID(id string) Option {
	return func(c *Config) {
		if id != "" {
			c.ThreadID = id
		}
	}
}

// WithResourceID sets the owner or tenant resource ID scoping the thread.
func WithResourceID(id string) Option {
	return func(c *Config) {
		c.ResourceID = id
	}
}

// WithTools adds tools to the chatbot agent for tool execution.
func WithTools(tools ...tool.Tool) Option {
	return func(c *Config) {
		c.Tools = append(c.Tools, tools...)
	}
}

// WithIO sets custom input reader and output writer streams.
// Useful for automated tests or custom terminal wrappers.
func WithIO(r io.Reader, w io.Writer) Option {
	return func(c *Config) {
		if r != nil {
			c.Input = r
		}
		if w != nil {
			c.Output = w
		}
	}
}

// WithBanner sets a custom welcome banner printed at the start of the session.
func WithBanner(banner string) Option {
	return func(c *Config) {
		c.Banner = banner
	}
}

// WithPromptPrefix sets the prompt string printed before the user types (default: "You > ").
func WithPromptPrefix(prefix string) Option {
	return func(c *Config) {
		c.PromptPrefix = prefix
	}
}

// WithExitCommands overrides the list of commands that terminate the chat session.
func WithExitCommands(cmds ...string) Option {
	return func(c *Config) {
		if len(cmds) > 0 {
			c.ExitCommands = cmds
		}
	}
}

type printer struct {
	w io.Writer
}

func (p *printer) print(a ...any) {
	_, _ = fmt.Fprint(p.w, a...)
}

func (p *printer) println(a ...any) {
	_, _ = fmt.Fprintln(p.w, a...)
}

func (p *printer) printf(format string, a ...any) {
	_, _ = fmt.Fprintf(p.w, format, a...)
}

// Run starts an interactive, stateful, streaming chatbot loop powered by the provided LLM.
// It reads input line-by-line, streams tokens from the LLM in real-time, persists conversation
// history in storage across turns, and terminates cleanly on exit commands or context cancellation.
func Run(ctx context.Context, model llm.LLM, opts ...Option) error {
	if model == nil {
		return errors.New("chatbot: model is required")
	}

	cfg := &Config{
		AgentID:      "chatbot-assistant",
		AgentName:    "Assistant",
		Instructions: DefaultInstructions,
		ThreadID:     "chat-session-default",
		Input:        os.Stdin,
		Output:       os.Stdout,
		PromptPrefix: "You > ",
		ExitCommands: []string{"exit", "quit", "/q", ":q", "q"},
	}

	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.Storage == nil {
		cfg.Storage = storage.NewMockStorage()
	}

	bot, err := agent.New(agent.Config{
		ID:           cfg.AgentID,
		Name:         cfg.AgentName,
		Instructions: cfg.Instructions,
		Model:        model,
		Tools:        cfg.Tools,
		Storage:      cfg.Storage,
	})
	if err != nil {
		return fmt.Errorf("chatbot: failed to create agent: %w", err)
	}

	// Ensure the thread exists in storage
	_, _ = cfg.Storage.SaveThread(ctx, &storage.Thread{
		ID:         cfg.ThreadID,
		ResourceID: cfg.ResourceID,
		Title:      fmt.Sprintf("%s Chat Session", cfg.AgentName),
	})

	pr := &printer{w: cfg.Output}

	// Print welcome banner
	if cfg.Banner != "" {
		pr.println(cfg.Banner)
	} else {
		pr.println("============================================================")
		pr.printf("🤖 Gojent Interactive Chatbot (%s)\n", cfg.AgentName)
		pr.println("Type your message and press Enter.")
		pr.println("Type 'exit' or 'quit' to end the conversation.")
		pr.println("============================================================")
	}

	scanner := bufio.NewScanner(cfg.Input)

	for {
		select {
		case <-ctx.Done():
			pr.println("\nChat session closed.")
			return ctx.Err()
		default:
		}

		pr.print(cfg.PromptPrefix)

		if !scanner.Scan() {
			if scanErr := scanner.Err(); scanErr != nil {
				return fmt.Errorf("chatbot: input error: %w", scanErr)
			}
			pr.println("\nGoodbye!")
			return nil
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// Check for exit commands
		lower := strings.ToLower(input)
		isExit := false
		for _, cmd := range cfg.ExitCommands {
			if lower == cmd {
				isExit = true
				break
			}
		}
		if isExit {
			pr.println("Goodbye!")
			return nil
		}

		// Stream the response from the agent
		pr.printf("%s > ", cfg.AgentName)

		stream, err := bot.Stream(ctx, input,
			agent.WithStorage(cfg.Storage),
			agent.WithThreadID(cfg.ThreadID),
			agent.WithResourceID(cfg.ResourceID),
		)
		if err != nil {
			pr.printf("\n[Error starting response: %v]\n", err)
			continue
		}

		for {
			chunk, recvErr := stream.Recv()
			if errors.Is(recvErr, io.EOF) {
				break
			}
			if recvErr != nil {
				pr.printf("\n[Stream error: %v]\n", recvErr)
				break
			}
			if chunk != nil && chunk.Delta != "" {
				pr.print(chunk.Delta)
			}
		}
		_ = stream.Close()
		pr.println()
	}
}

// RunChatbot is a convenience alias for Run.
func RunChatbot(ctx context.Context, model llm.LLM, opts ...Option) error {
	return Run(ctx, model, opts...)
}
