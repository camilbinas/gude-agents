// Example: Tools that return images.
//
// Demonstrates tool.NewRich for tools that return images alongside text.
// The color_swatch tool generates a solid-color PNG and returns it to the
// LLM, which can then see and describe the image.
//
// Run:
//
//	go run ./tool-images

package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/logging/debug"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
	"github.com/joho/godotenv"
)

type SwatchInput struct {
	R    int `json:"r" description:"Red component 0-255" required:"true"`
	G    int `json:"g" description:"Green component 0-255" required:"true"`
	B    int `json:"b" description:"Blue component 0-255" required:"true"`
	Size int `json:"size" description:"Width and height in pixels (default 64)"`
}

func main() {
	godotenv.Load() //nolint

	swatchTool := tool.NewRich("color_swatch", "Generate a solid-color PNG swatch image from RGB values.",
		func(ctx context.Context, in SwatchInput) (*tool.Output, error) {
			size := in.Size
			if size <= 0 {
				size = 64
			}

			c := color.RGBA{R: uint8(in.R), G: uint8(in.G), B: uint8(in.B), A: 255}
			img := image.NewRGBA(image.Rect(0, 0, size, size))
			for y := range size {
				for x := range size {
					img.Set(x, y, c)
				}
			}

			var buf bytes.Buffer
			if err := png.Encode(&buf, img); err != nil {
				return nil, fmt.Errorf("encode PNG: %w", err)
			}

			data := buf.Bytes()

			// Save to tmp for inspection.
			_ = os.MkdirAll("tmp", 0o755)
			path := filepath.Join("tmp", fmt.Sprintf("swatch_%d_%d_%d.png", in.R, in.G, in.B))
			_ = os.WriteFile(path, data, 0o644)

			return &tool.Output{
				Text:   fmt.Sprintf("Generated %dx%d swatch rgb(%d,%d,%d) — saved to %s", size, size, in.R, in.G, in.B, path),
				Images: []tool.Image{{Data: data, MIMEType: "image/png"}},
			}, nil
		},
	)

	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.APE{
			Action:      "Use the color_swatch tool to generate images, then describe what you see.",
			Purpose:     "Demonstrate that tools can return images for visual analysis.",
			Expectation: "When an image is returned, describe its contents in detail.",
		},
		[]tool.Tool{swatchTool},
		debug.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Tool images agent ready. Type 'quit' to exit.")
	fmt.Println("Try: generate a red swatch and a blue swatch")
	fmt.Println()

	utils.Chat(context.Background(), a)
}
