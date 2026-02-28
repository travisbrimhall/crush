# VS Code Crush Extension

Send diagnostics and test failures to [Crush](https://github.com/charmbracelet/crush) for AI-assisted fixing.

## Features

- **Fix with Crush**: Right-click on any error/warning squiggle and select "Fix with Crush" from the quick fix menu
- Sends structured diagnostic context including:
  - Error message and location
  - Related diagnostics
  - Import ranges for context
  - File metadata

## Setup

1. Start Crush in your terminal
2. Copy the session token printed at startup
3. Run command "Crush: Set Session Token" in VS Code (Cmd+Shift+P)
4. Paste the token

Alternatively, set `crush.token` in your VS Code settings.

## Configuration

| Setting | Default | Description |
|---------|---------|-------------|
| `crush.apiUrl` | `http://localhost:9119` | Crush context API URL |
| `crush.token` | (empty) | Session token from Crush startup |

## Development

```bash
cd extensions/vscode-crush
npm install
npm run compile
```

Press F5 in VS Code to launch extension development host.

## How It Works

The extension registers a code action provider that adds "Fix with Crush" to the quick fix menu for errors and warnings. When triggered, it:

1. Collects the diagnostic and related information
2. Computes a deterministic workspace ID
3. POSTs structured metadata to Crush's local API
4. Crush uses MCP to pull actual file contents at execution time

No file contents are sent in the payload - only metadata and ranges. This keeps payloads lightweight and ensures Crush sees the current file state when it runs.
