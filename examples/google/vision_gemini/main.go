// Package main demonstrates using the Google Gen AI provider with Gojent
// for multimodal visual reasoning and multipart tool results.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
	"github.com/ijaidev/gojent/providers/google"
)

// A minimal 1x1 valid PNG in bytes for demonstration
var samplePngBytes = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func main() {
	ctx := context.Background()

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		fmt.Println("INFO: GEMINI_API_KEY or GOOGLE_API_KEY is not set.")
		fmt.Println("Set the key to execute live calls against Google Gemini.")
		return
	}

	// 1. Initialize Google Gemini Provider
	gemini, err := google.New(ctx, google.Config{
		Model:  "gemini-2.5-flash",
		APIKey: apiKey,
	})
	if err != nil {
		log.Fatalf("Failed to initialize Google Gen AI provider: %v", err)
	}

	// 2. Define a multipart tool that returns both JSON metadata and image parts
	chartTool := tool.MustNew(tool.Config[struct {
		Metric string `json:"metric" description:"Metric to plot"`
	}, tool.Result]{
		ID:          "generate_chart",
		Description: "Generates a visualization chart for a given metric and returns the image",
		Execute: func(_ context.Context, in struct {
			Metric string `json:"metric" description:"Metric to plot"`
		}) (tool.Result, error) {
			img := llm.ImageFromBytes(samplePngBytes, "image/png")
			return tool.NewImageResult(
				fmt.Sprintf(`{"status":"success","metric":%q,"points":10}`, in.Metric),
				img,
			), nil
		},
	})

	// 3. Initialize Agent with instructions and tools
	analyst, err := agent.New(agent.Config{
		Name:         "analyst-agent",
		Instructions: "You are a visual data analyst. Call tools to generate charts when requested.",
		Model:        gemini,
		Tools:        []tool.Tool{chartTool},
	})
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}

	// 4. Run agent with multipart media input
	imageInput := llm.ImageFromBytes(samplePngBytes, "image/png")
	fmt.Println("Running agent with multimodal image input...")
	res, err := analyst.Generate(
		ctx,
		"Please inspect this sample image, then generate a chart for 'user_growth' and summarize the findings.",
		agent.WithMedia(imageInput),
	)
	if err != nil {
		log.Fatalf("Agent generation failed: %v", err)
	}

	fmt.Printf("\n--- Agent Response ---\n%s\n", res.Text)
}
