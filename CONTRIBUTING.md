# Contributing

## Scope

Keep changes focused on the MCP server, desktop capture, desktop input, platform permissions, packaging, and verification.

Do not add a GUI, full-screen overlay, network listener, shell execution, or filesystem-control surface without an explicit design decision.

## Development

Run formatting and tests before committing:

~~~text
gofmt -w .
go test ./...
go vet ./...
~~~

For platform work, build the affected target and record the operating system, display server, architecture, and permission state used for verification.

## Tool behavior

Tool schemas must be explicit and easy for models to use. Validate coordinates, text, key names, button names, image formats, and target identifiers before invoking platform APIs.

Screenshots should be returned as standard MCP image content with concise metadata. Do not log screenshot bytes or typed text.

## Commits

Use small, descriptive commits. Keep unrelated work out of the change and do not commit generated binaries, credentials, screenshots, or local client configuration.
