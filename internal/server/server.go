package server

import (
	"context"
	"encoding/json"

	"github.com/MorenoLand/Moreno.ComputerUseMCP/internal/computer"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type screenshotInput struct {
	Target        string `json:"target,omitempty" jsonschema:"target: screen, active_window, or window"`
	WindowID      string `json:"window_id,omitempty" jsonschema:"window identifier returned by computer_list_windows"`
	Format        string `json:"format,omitempty" jsonschema:"image format: jpeg or png"`
	Quality       int    `json:"quality,omitempty" jsonschema:"jpeg quality from 1 to 100"`
	MaxWidth      int    `json:"max_width,omitempty" jsonschema:"optional maximum output width in pixels"`
	IncludeCursor bool   `json:"include_cursor,omitempty" jsonschema:"include the pointer in the image"`
}

type moveInput struct {
	X int `json:"x" jsonschema:"absolute screen x coordinate in physical pixels"`
	Y int `json:"y" jsonschema:"absolute screen y coordinate in physical pixels"`
}

type clickInput struct {
	X         int      `json:"x" jsonschema:"absolute screen x coordinate in physical pixels"`
	Y         int      `json:"y" jsonschema:"absolute screen y coordinate in physical pixels"`
	Button    string   `json:"button,omitempty" jsonschema:"mouse button: left, right, middle, x1, or x2"`
	Clicks    int      `json:"clicks,omitempty" jsonschema:"number of clicks from 1 to 3"`
	Modifiers []string `json:"modifiers,omitempty" jsonschema:"modifier keys held during the click"`
}

type typeInput struct {
	Text string `json:"text" jsonschema:"text to type into the focused application"`
}

type keypressInput struct {
	Keys []string `json:"keys" jsonschema:"keys pressed together or in sequence"`
}

func New(backend computer.Backend) *mcp.Server {
	service := computer.NewService(backend)
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "computer-use", Version: "0.1.0"}, nil)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "computer_screenshot",
		Description: "Capture the desktop or a visible application and return it as an image.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input screenshotInput) (*mcp.CallToolResult, any, error) {
		screenshot, err := service.Screenshot(ctx, computer.ScreenshotOptions{
			Target:        computer.Target(input.Target),
			WindowID:      input.WindowID,
			Format:        input.Format,
			Quality:       input.Quality,
			MaxWidth:      input.MaxWidth,
			IncludeCursor: input.IncludeCursor,
		})
		if err != nil {
			return nil, nil, err
		}
		metadata, err := json.Marshal(map[string]any{
			"target":       screenshot.Target,
			"window_id":    screenshot.WindowID,
			"width":        screenshot.Width,
			"height":       screenshot.Height,
			"origin_x":     screenshot.OriginX,
			"origin_y":     screenshot.OriginY,
			"scale_factor": screenshot.ScaleFactor,
			"mime_type":    screenshot.MIMEType,
		})
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.ImageContent{Data: screenshot.Data, MIMEType: screenshot.MIMEType},
			&mcp.TextContent{Text: string(metadata)},
		}}, nil, nil
	})
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "computer_list_windows",
		Description: "List visible desktop windows and their screen coordinates.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		windows, err := service.ListWindows(ctx)
		if err != nil {
			return nil, nil, err
		}
		data, err := json.Marshal(windows)
		if err != nil {
			return nil, nil, err
		}
		return textResult(string(data)), nil, nil
	})
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "computer_move",
		Description: "Move the pointer to an absolute screen coordinate.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input moveInput) (*mcp.CallToolResult, any, error) {
		if err := service.Move(ctx, computer.Point{X: input.X, Y: input.Y}); err != nil {
			return nil, nil, err
		}
		return textResult("pointer moved"), nil, nil
	})
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "computer_click",
		Description: "Move to an absolute screen coordinate and click a mouse button.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input clickInput) (*mcp.CallToolResult, any, error) {
		if err := service.Click(ctx, computer.ClickOptions{
			Point:     computer.Point{X: input.X, Y: input.Y},
			Button:    input.Button,
			Clicks:    input.Clicks,
			Modifiers: input.Modifiers,
		}); err != nil {
			return nil, nil, err
		}
		return textResult("mouse click sent"), nil, nil
	})
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "computer_type",
		Description: "Type text into the currently focused application.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input typeInput) (*mcp.CallToolResult, any, error) {
		if err := service.Type(ctx, computer.TypeOptions{Text: input.Text}); err != nil {
			return nil, nil, err
		}
		return textResult("text typed"), nil, nil
	})
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "computer_keypress",
		Description: "Press a key or key combination, such as CTRL and L.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input keypressInput) (*mcp.CallToolResult, any, error) {
		if err := service.Keypress(ctx, computer.KeypressOptions{Keys: input.Keys}); err != nil {
			return nil, nil, err
		}
		return textResult("key press sent"), nil, nil
	})
	return mcpServer
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}
