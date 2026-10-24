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
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/core/tool"
)

// DefaultInstructions is the default system prompt guiding the chatbot.
const DefaultInstructions = "You are a friendly, concise, and helpful AI assistant. Answer user questions clearly and accurately."

// Config defines the configuration options for a chatbot session.
type Config struct {
	AgentName    string
	Instructions string
	Store        session.Store
	SessionID    string
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

// WithStore sets the session store used to persist conversation history.
func WithStore(store session.Store) Option {
	return func(c *Config) {
		c.Store = store
	}
}

// WithSessionID sets the conversation identifier used for multi-turn memory.
func WithSessionID(id string) Option {
	return func(c *Config) {
		if id != "" {
			c.SessionID = id
		}
	}
}

// WithResourceID sets the owner or tenant scope for the conversation.
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

// WithPromptPrefix sets the prompt string printed before the user types.
func WithPromptPrefix(prefix string) Option {
	return func(c *Config) {
		c.PromptPrefix = prefix
	}
}

// WithExitCommands overrides the commands that terminate the chat session.
func WithExitCommands(cmds ...string) Option {
	return func(c *Config) {
		if len(cmds) > 0 {
			c.ExitCommands = cmds
		}
	}
}

// Run starts an interactive, stateful, streaming chatbot loop.
//
// Each turn runs through the same Runner, so history is loaded from and
// appended to the configured store. Events are consumed for streaming output.
func Run(ctx context.Context, model llm.Model, opts ...Option) error {
	if model == nil {
		return errors.New("chatbot: model is required")
	}

	cfg := &Config{
		AgentName:    "Assistant",
		Instructions: DefaultInstructions,
		SessionID:    "chat-session-default",
		Input:        os.Stdin,
		Output:       os.Stdout,
		PromptPrefix: "You > ",
		ExitCommands: []string{"exit", "quit", "/q", ":q", "q"},
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.Store == nil {
		cfg.Store = session.NewMemory()
	}

	bot, err := agent.New(agent.Config{
		Name:         "chatbot",
		Description:  "Interactive assistant",
		Instructions: cfg.Instructions,
		Model:        model,
		Tools:        cfg.Tools,
	})
	if err != nil {
		return fmt.Errorf("chatbot: failed to create agent: %w", err)
	}
	runner := &agent.Runner{Store: cfg.Store}
	sess := &session.Session{ID: cfg.SessionID, ResourceID: cfg.ResourceID}

	println := func(a ...any) { _, _ = fmt.Fprintln(cfg.Output, a...) }
	printf := func(format string, a ...any) { _, _ = fmt.Fprintf(cfg.Output, format, a...) }

	if cfg.Banner != "" {
		println(cfg.Banner)
	} else {
		println("============================================================")
		printf("Gojent Interactive Chatbot (%s)\n", cfg.AgentName)
		println("Type your message and press Enter.")
		println("Type 'exit' or 'quit' to end the conversation.")
		println("============================================================")
	}

	scanner := bufio.NewScanner(cfg.Input)
	for {
		select {
		case <-ctx.Done():
			println("\nChat session closed.")
			return ctx.Err()
		default:
		}

		printf("%s", cfg.PromptPrefix)
		if !scanner.Scan() {
			if scanErr := scanner.Err(); scanErr != nil {
				return fmt.Errorf("chatbot: input error: %w", scanErr)
			}
			println("\nGoodbye!")
			return nil
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if isExitCommand(input, cfg.ExitCommands) {
			println("Goodbye!")
			return nil
		}

		printf("%s > ", cfg.AgentName)
		run, err := runner.Start(ctx, bot, sess, llm.UserMessage(input))
		if err != nil {
			printf("\n[Error starting response: %v]\n", err)
			continue
		}

		for event := range run.Events() {
			if delta, ok := event.(agent.TextDelta); ok {
				_, _ = fmt.Fprint(cfg.Output, delta.Text)
			}
		}
		if _, err := run.Wait(ctx); err != nil && !errors.Is(err, context.Canceled) {
			printf("\n[Error: %v]\n", err)
			continue
		}
		println()
	}
}

func isExitCommand(input string, commands []string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	for _, command := range commands {
		if lower == strings.ToLower(command) {
			return true
		}
	}
	return false
}

// RunChatbot is a convenience alias for Run.
func RunChatbot(ctx context.Context, model llm.Model, opts ...Option) error {
	return Run(ctx, model, opts...)
}
