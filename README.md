# Computer Use MCP Server

A small, local MCP server that lets vision-capable models inspect and control the desktop through standard MCP tools.

## Features

- Screenshot the desktop, active window, or a selected window
- Move the pointer
- Click mouse buttons
- Type text
- Send key combinations
- Return screenshots as standard MCP image content
- Show a small click-through watermark identifying the controlling client
- Abort active computer input with Escape
- Run as one stdio executable with no background service

## Safety model

The default transport is local stdio. The client launches the executable and communicates over standard input and output. The server does not expose a network listener, shell, filesystem browser, or full-screen overlay.

Desktop input is powerful. Keep the executable and client configuration private, use a separate test desktop for automation, and press Escape if an action is unexpected. During input actions, a small click-through watermark identifies the controlling MCP client and shows the Escape stop key. It is not a full-screen overlay.

## Client configuration

Point an MCP client at the executable:

~~~json
{
  "mcpServers": {
    "computer": {
      "command": "/absolute/path/to/computer-use-mcp"
    }
  }
}
~~~

On Windows, use the absolute path to the ".exe" file.

## Platform notes

The current native backends are Windows and Linux X11. On Windows, active and selected window screenshots render the target window directly, so focus and overlapping windows do not change the returned image. Minimized windows are restored without activation at an off-screen position, captured, and minimized again with their original placement, so the user's focus and screen are left alone; the reported origin is where the window would appear if restored. Native Wayland and macOS builds are kept behind explicit unsupported errors until their permission and capture APIs are implemented. Windows and X11 show the control watermark and watch for Escape. X11 input requires the XTEST extension; X11 text entry currently supports ASCII and the active keyboard layout.

Each operating system has its own screen-capture and input permissions. Wayland may ask for screen and input access through the desktop portal. macOS requires the normal Screen Recording and Accessibility permissions.

The server is designed to capture on demand. It does not continuously render a preview or place a full-screen transparent surface over the desktop; screenshots and window listings are stealthy and do not show the indicator, while input actions show a small static watermark and four-edge control line for two seconds after each action.

## Development

Requirements:

- Go
- A desktop session for live platform checks

Run the server:

~~~text
go run ./cmd/computer-use-mcp
~~~

Run tests:

~~~text
go test ./...
~~~

Build a local executable:

~~~text
go build -o bin/computer-use-mcp ./cmd/computer-use-mcp
~~~

The protocol layer is platform-neutral. Capture and input implementations live behind platform-specific interfaces so the stdio server remains small and consistent across operating systems.
