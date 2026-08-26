# Computer Use MCP Server

A small, local MCP server that lets vision-capable models inspect and control the desktop through standard MCP tools.

## Features

- Screenshot the desktop, active window, or a selected window
- Move the pointer
- Click mouse buttons
- Type text
- Send key combinations
- Return screenshots as standard MCP image content
- Run as one stdio executable with no background service

## Safety model

The default transport is local stdio. The client launches the executable and communicates over standard input and output. The server does not expose a network listener, shell, filesystem browser, or full-screen overlay.

Desktop input is powerful. Keep the executable and client configuration private, use a separate test desktop for automation, and stop the client if an action is unexpected.

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

Each operating system has its own screen-capture and input permissions. X11 and Wayland are different backends; Wayland may ask for screen and input access through the desktop portal. macOS requires the normal Screen Recording and Accessibility permissions.

The server is designed to capture on demand. It does not continuously render a preview or place a full-screen transparent surface over the desktop.

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
