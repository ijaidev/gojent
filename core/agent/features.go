package agent

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ijaidev/gojent/core/llm"
)

// checkRequestFeatures rejects requests the model cannot honor instead of
// letting an adapter silently degrade them (D12).
func checkRequestFeatures(modelID string, features llm.Features, request *llm.Request) error {
	var missing []string
	add := func(feature string) {
		if !slices.Contains(missing, feature) {
			missing = append(missing, feature)
		}
	}

	if len(request.Tools) > 0 && !features.Tools {
		add("tools")
	}
	if request.State.EffectiveMode() == llm.StateModeServer && !features.ServerState {
		add("server state")
	}
	if request.Reasoning != nil &&
		request.Reasoning.Level != "" && request.Reasoning.Level != llm.ReasoningLevelNone &&
		!features.Reasoning {
		add("reasoning")
	}
	if request.ResponseFormat != nil && !features.StrictJSONSchema {
		switch request.ResponseFormat.Type {
		case llm.ResponseFormatJSONSchema:
			add("json schema")
		case llm.ResponseFormatJSON:
			add("json output")
		}
		if request.ResponseFormat.Strict {
			add("strict json schema")
		}
	}

	for _, definition := range request.Tools {
		switch definition.EffectiveKind() {
		case llm.ToolKindServer:
			if !features.HostedTools {
				add("hosted tools")
			}
		case llm.ToolKindMCP:
			if !features.MCP {
				add("mcp")
			}
		}
	}

	for _, message := range request.Messages {
		for _, part := range message.Content {
			switch p := part.(type) {
			case *llm.FilePart:
				switch {
				case strings.HasPrefix(p.MIMEType, "image/"):
					if !features.Vision {
						add("vision")
					}
				case strings.HasPrefix(p.MIMEType, "audio/"):
					if !features.AudioIn {
						add("audio input")
					}
				case strings.HasPrefix(p.MIMEType, "video/"):
					if !features.Video {
						add("video")
					}
				default:
					if !features.Documents {
						add("documents")
					}
				}
			case *llm.ToolCallPart:
				switch p.EffectiveKind() {
				case llm.ToolKindServer:
					if !features.HostedTools {
						add("hosted tools")
					}
				case llm.ToolKindMCP:
					if !features.MCP {
						add("mcp")
					}
				}
			}
		}
	}

	if len(missing) == 0 {
		return nil
	}
	slices.Sort(missing)
	return fmt.Errorf("%w: model %q does not support %s", llm.ErrUnsupported, modelID, strings.Join(missing, ", "))
}
