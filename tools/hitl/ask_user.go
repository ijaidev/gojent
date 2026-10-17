// Package hitl provides Human-in-the-Loop (HITL) tools allowing autonomous agents to request input or confirmation.
package hitl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ijaidev/gojent/core/tool"
)

// AskUserInput defines parameters for asking the user a question.
type AskUserInput struct {
	Question string   `json:"question" description:"The question or clarification needed from the user"`
	Options  []string `json:"options,omitempty" description:"Optional predefined options for multiple-choice selection"`
}

// AskUserOutput represents the user's response.
type AskUserOutput struct {
	Answer string `json:"answer"`
}

// PromptFunc defines a custom callback to handle user input prompts.
type PromptFunc func(ctx context.Context, question string, options []string) (string, error)

// Config configures the ask_user tool.
type Config struct {
	PromptHandler PromptFunc
	Reader        io.Reader
	Writer        io.Writer
}

// NewAskUserTool creates a Tool that pauses agent execution to solicit input or decisions from the user.
func NewAskUserTool(cfg ...Config) (tool.Tool, error) {
	var c Config
	if len(cfg) > 0 {
		c = cfg[0]
	}

	handler := c.PromptHandler
	if handler == nil {
		r := c.Reader
		if r == nil {
			r = os.Stdin
		}
		w := c.Writer
		if w == nil {
			w = os.Stdout
		}

		handler = func(_ context.Context, question string, options []string) (string, error) {
			_, _ = fmt.Fprintf(w, "\n[Agent Question]: %s\n", question)
			if len(options) > 0 {
				_, _ = fmt.Fprintf(w, "Options: %s\n", strings.Join(options, ", "))
			}
			_, _ = fmt.Fprint(w, "> ")

			scanner := bufio.NewScanner(r)
			if scanner.Scan() {
				return strings.TrimSpace(scanner.Text()), nil
			}
			if err := scanner.Err(); err != nil {
				return "", err
			}
			return "", fmt.Errorf("ask_user: unexpected EOF")
		}
	}

	return tool.New(tool.Config[AskUserInput, AskUserOutput]{
		ID:          "ask_user",
		Description: "Ask the user a question to obtain clarification, missing information, or confirmation.",
		Execute: func(ctx context.Context, in AskUserInput) (AskUserOutput, error) {
			if in.Question == "" {
				return AskUserOutput{}, fmt.Errorf("ask_user: question cannot be empty")
			}

			answer, err := handler(ctx, in.Question, in.Options)
			if err != nil {
				return AskUserOutput{}, fmt.Errorf("ask_user: %w", err)
			}

			return AskUserOutput{
				Answer: answer,
			}, nil
		},
	})
}
