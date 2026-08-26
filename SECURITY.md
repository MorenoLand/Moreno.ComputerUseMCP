# Security

This server can read the desktop and inject mouse and keyboard input. Treat its executable and MCP configuration as sensitive local automation credentials.

## Safe deployment

- Prefer the stdio transport.
- Do not expose a future HTTP transport beyond localhost without authentication and origin validation.
- Run the client and server at the same privilege level when possible.
- Keep screenshot and typed-text logging disabled.
- Use a dedicated desktop or test account for unattended automation.
- Keep an immediate stop mechanism available.

The server must reject malformed or out-of-bounds tool arguments before calling operating-system APIs. It must not provide shell execution, arbitrary file access, or credential storage.

## Reporting

Report security issues privately to the repository owner before public disclosure. Include the affected version, operating system, display server, reproduction steps, and impact without attaching secrets or private screenshots.
