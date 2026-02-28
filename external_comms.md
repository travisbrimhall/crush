# External Communications Technical Design

## Overview

This document describes the architecture for integrating Crush with external
context sources: VS Code, Chrome DevTools (browser console), and Docker. The
design follows an event-aware, user-triggered model—external sources push
structured failure states, but Crush only acts when the user explicitly
requests help.

### Scope

This is **internal infrastructure for Crush only**. The local API server and
context ingestion are not designed as a general-purpose failure bus or platform
for third-party tools. We're building plumbing so the Crush agent has richer
context—not a registry or event system for the broader ecosystem.

### Runtime Model

Crush runs short-lived. Context is **in-memory only** and discarded on exit.
There is no persistence or archival layer.

- Bounded buffer: max 50 contexts per session
- Overflow: drop oldest context when full
- No expiry timers, no archive state, no lifecycle complexity
- Session ends → everything gone

### Design Philosophy

The magic moment is not "Crush sees what I selected." The magic moment is
"Crush understands my failure without me explaining it."

**Guiding principles:**

1. **Event-aware, user-triggered**: Sources prepare rich context, but the user
   initiates the request.
2. **Structured payloads**: No free-form text—every event has a schema.
3. **Metadata-only ingestion**: Payloads contain ranges and identifiers, never
   file contents. Crush pulls canonical file state via MCP at execution time.
4. **No ambient streaming (v1)**: Avoid continuous file/cursor sync. It's
   creepy and hard to reason about.
5. **Leverage existing signals**: Diagnostics, test failures, console errors,
   and container logs are already structured. We tap into them; we don't invent
   new ones.
6. **Internal only**: This is Crush's context ingestion layer, not a platform.
   No external consumers, no plugin API, no ecosystem compatibility concerns.

---

## Architecture

```
External Sources (VS Code, Chrome, Docker)
        │
        │ POST /context (metadata only)
        ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                    In-Memory Bounded Signal Buffer                          │
│                         (session-scoped, max 50)                            │
├─────────────────────────────────────────────────────────────────────────────┤
│  - receivedAt stamped at ingestion (system clock authoritative)             │
│  - Dedupe via hash(eventType + filePath + key fields)                       │
│  - Oldest dropped on overflow                                               │
└─────────────────────────────────────────────────────────────────────────────┘
        │
        │ User reviews in TUI, presses Enter
        ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                           Agent Coordinator                                 │
│  - Pulls canonical file state via MCP using ranges from context             │
│  - Checks file version drift (warn if changed since context capture)        │
│  - Merges structured context into prompt                                    │
│  - Executes agent                                                           │
└─────────────────────────────────────────────────────────────────────────────┘
```

No persistence. No archive. No auto-execution. No cross-session state.

### File Version Drift

When pulling file contents at execution time, the coordinator checks:

```go
if currentFileVersion != context.FileVersion {
    // Render warning: "⚠ File changed since diagnostic was captured"
    // Continue execution—do not auto-reject
}
```

This handles the case where user edits the file after clicking "Fix with Crush"
but before submitting. The agent sees the warning and can reason appropriately.

### Prompt Assembly Budget

At submit time, the coordinator merges contexts into a single prompt. To prevent
token explosion (50 contexts × 500 log lines × 50 stack frames = disaster):

```go
const (
    MaxPromptTokens    = 120_000 // Model-dependent, primary guardrail
    MaxContextsInPrompt = 20     // Secondary guardrail, hard cap
)

func assemblePrompt(contexts []*ContextEntry) string {
    // Sort by ReceivedAt (newest first)
    sort.Slice(contexts, func(i, j int) bool {
        return contexts[i].ReceivedAt.After(contexts[j].ReceivedAt)
    })

    // Secondary guardrail: cap context count regardless of tokens
    if len(contexts) > MaxContextsInPrompt {
        contexts = contexts[:MaxContextsInPrompt]
    }

    var prompt strings.Builder
    tokenCount := 0

    for _, ctx := range contexts {
        rendered := renderContext(ctx)
        tokens := estimateTokens(rendered)

        if tokenCount + tokens > MaxPromptTokens {
            prompt.WriteString("\n⚠ Some older contexts omitted due to size limits.\n")
            break
        }

        prompt.WriteString(rendered)
        tokenCount += tokens
    }

    return prompt.String()
}
```

Newest contexts have priority. Oldest are truncated first. Two-layer protection:
count cap catches pathological cases even if token estimation is off.

---

## Phase 1: VS Code Integration

### Scope

- Diagnostic-aware "Fix with Crush" context menu
- Test failure integration
- Manual trigger only—no background sync

### VS Code Extension APIs

#### Diagnostics

```typescript
// Event: fires when diagnostics change globally
vscode.languages.onDidChangeDiagnostics: Event<DiagnosticChangeEvent>

interface DiagnosticChangeEvent {
  readonly uris: readonly Uri[];
}

interface Diagnostic {
  range: Range;
  message: string;
  severity: DiagnosticSeverity;  // Error=0, Warning=1, Info=2, Hint=3
  source?: string;               // e.g., "typescript", "eslint"
  code?: string | number | { target: Uri; value: string | number };
  relatedInformation?: DiagnosticRelatedInformation[];
  tags?: DiagnosticTag[];        // Unnecessary=1, Deprecated=2
}

interface DiagnosticRelatedInformation {
  location: Location;
  message: string;
}
```

#### Tests

```typescript
interface TestController {
  createTestRun(request: TestRunRequest, name?: string): TestRun;
}

interface TestRun {
  failed(test: TestItem, message: TestMessage | readonly TestMessage[], duration?: number): void;
  errored(test: TestItem, message: TestMessage | readonly TestMessage[], duration?: number): void;
  appendOutput(output: string, location?: Location, test?: TestItem): void;
}

interface TestMessage {
  message: string | MarkdownString;
  location?: Location;
  expectedOutput?: string;
  actualOutput?: string;
  stackTrace?: TestMessageStackFrame[];
}

interface TestMessageStackFrame {
  readonly label: string;
  readonly uri?: Uri;
  readonly position?: Position;
}
```

#### Debug (Phase 2)

```typescript
// Events
vscode.debug.onDidStartDebugSession: Event<DebugSession>
vscode.debug.onDidTerminateDebugSession: Event<DebugSession>
vscode.debug.onDidChangeActiveStackItem: Event<DebugThread | DebugStackFrame | undefined>

interface DebugSession {
  readonly id: string;
  readonly type: string;
  readonly name: string;
  customRequest(command: string, args?: any): Thenable<any>;
}

// Capture variables via Debug Adapter Protocol
const variables = await session.customRequest('variables', {
  variablesReference: scopeRef
});
```

### Payload Schema: Diagnostic Fix Request

```json
{
  "schemaVersion": "1.0",
  "event": "diagnostic_fix_request",
  "source": "vscode",
  "timestamp": "2024-01-15T10:30:00.000Z",
  "workspace": {
    "id": "a1b2c3d4e5f67890",
    "root": "/Users/dev/myproject",
    "name": "myproject"
  },
  "file": {
    "path": "src/utils/parser.ts",
    "languageId": "typescript",
    "version": 42
  },
  "diagnostic": {
    "range": {
      "start": { "line": 15, "character": 8 },
      "end": { "line": 15, "character": 24 }
    },
    "message": "Property 'foo' does not exist on type 'Bar'",
    "severity": "error",
    "source": "typescript",
    "code": 2339
  },
  "relatedDiagnostics": [
    {
      "file": "src/types/index.ts",
      "range": { "start": { "line": 5, "character": 0 }, "end": { "line": 10, "character": 1 } },
      "message": "Type 'Bar' is defined here"
    }
  ],
  "context": {
    "diagnosticRange": {
      "start": { "line": 10, "character": 0 },
      "end": { "line": 20, "character": 0 }
    },
    "importRanges": [
      { "start": { "line": 0, "character": 0 }, "end": { "line": 0, "character": 35 } }
    ],
    "fileVersion": 42
  }
}
```

### Payload Schema: Test Failure Request

```json
{
  "schemaVersion": "1.0",
  "event": "test_failure_fix_request",
  "source": "vscode",
  "timestamp": "2024-01-15T10:35:00.000Z",
  "workspace": {
    "id": "a1b2c3d4e5f67890",
    "root": "/Users/dev/myproject",
    "name": "myproject"
  },
  "test": {
    "id": "src/utils/parser.test.ts::parseJSON::handles nested objects",
    "name": "handles nested objects",
    "file": "src/utils/parser.test.ts",
    "line": 42
  },
  "failure": {
    "message": "Expected 5 but received 3",
    "expected": "5",
    "actual": "3",
    "stackTrace": [
      { "function": "parseJSON", "file": "src/utils/parser.ts", "line": 15, "column": 8 },
      { "function": "Object.<anonymous>", "file": "src/utils/parser.test.ts", "line": 45, "column": 20 }
    ]
  },
  "context": {
    "testRange": {
      "start": { "line": 40, "character": 0 },
      "end": { "line": 50, "character": 0 }
    },
    "relatedSourceRanges": [
      { "file": "src/utils/parser.ts", "range": { "start": 10, "end": 25 } }
    ],
    "fileVersion": 12
  }
}
```

### UX Flow

1. User sees red squiggle in VS Code.
2. User right-clicks → "Fix with Crush".
3. Extension collects:
   - Primary diagnostic
   - Related diagnostics
   - Range metadata (Crush pulls actual code via MCP)
   - File metadata
4. Extension POSTs to `http://localhost:9119/context`.
5. Crush TUI shows context block: "VS Code: TypeScript error in parser.ts:15".
6. User reviews and presses Enter (or types additional context).
7. Agent coordinator pulls file contents via MCP using range metadata.
8. Agent runs with full structured context.

**Design note**: Payloads contain metadata only, not file contents. Crush pulls
code via MCP at execution time. This prevents stale code (file may change
between context push and user submission) and keeps payloads lightweight.

---

## Phase 2: Chrome DevTools Integration

### Scope

- Console error capture (user-triggered)
- Exception details with stack traces
- Network failure context (optional)

### Chrome DevTools Protocol (CDP)

Chrome exposes a WebSocket-based debugging protocol on port 9222 when launched
with `--remote-debugging-port=9222`.

#### Connection

```bash
# Start Chrome with remote debugging
google-chrome --remote-debugging-port=9222

# Discover targets
curl http://localhost:9222/json/list
```

Returns:

```json
[
  {
    "id": "DAB7FB6...",
    "type": "page",
    "url": "https://example.com",
    "webSocketDebuggerUrl": "ws://localhost:9222/devtools/page/DAB7FB6..."
  }
]
```

#### Runtime Domain Events

##### `Runtime.consoleAPICalled`

Fires on `console.log()`, `console.error()`, etc.

```json
{
  "method": "Runtime.consoleAPICalled",
  "params": {
    "type": "error",
    "args": [
      {
        "type": "string",
        "value": "Failed to fetch user data"
      },
      {
        "type": "object",
        "subtype": "error",
        "className": "TypeError",
        "description": "TypeError: Cannot read properties of undefined (reading 'id')"
      }
    ],
    "executionContextId": 1,
    "timestamp": 1705312200000,
    "stackTrace": {
      "callFrames": [
        {
          "functionName": "fetchUser",
          "scriptId": "42",
          "url": "https://example.com/app.js",
          "lineNumber": 127,
          "columnNumber": 15
        },
        {
          "functionName": "handleClick",
          "scriptId": "42",
          "url": "https://example.com/app.js",
          "lineNumber": 89,
          "columnNumber": 8
        }
      ]
    }
  }
}
```

##### `Runtime.exceptionThrown`

Fires on unhandled exceptions.

```json
{
  "method": "Runtime.exceptionThrown",
  "params": {
    "timestamp": 1705312200000,
    "exceptionDetails": {
      "exceptionId": 1,
      "text": "Uncaught TypeError: Cannot read properties of undefined",
      "lineNumber": 127,
      "columnNumber": 15,
      "scriptId": "42",
      "url": "https://example.com/app.js",
      "stackTrace": {
        "callFrames": [...]
      },
      "exception": {
        "type": "object",
        "subtype": "error",
        "className": "TypeError",
        "description": "TypeError: Cannot read properties of undefined (reading 'id')\n    at fetchUser (app.js:127:15)\n    at handleClick (app.js:89:8)"
      }
    }
  }
}
```

#### Enabling the Runtime Domain

```javascript
const CDP = require('chrome-remote-interface');

async function captureConsole() {
  const client = await CDP({ port: 9222 });
  const { Runtime } = client;

  await Runtime.enable();

  client.on('Runtime.consoleAPICalled', (params) => {
    if (params.type === 'error') {
      // Capture and format for Crush
    }
  });

  client.on('Runtime.exceptionThrown', (params) => {
    // Capture unhandled exceptions
  });
}
```

### Security Considerations

- CDP has **no built-in authentication**.
- **Never expose port 9222 to the network** (`--remote-debugging-address=0.0.0.0` is dangerous).
- Use localhost only or SSH tunneling for remote access.
- The Crush browser extension should connect only to `localhost:9222`.

### Implementation Paths

There are two approaches for Chrome integration, with different tradeoffs:

#### Path A: CDP Direct (Development/Power Users)

Requires user to launch Chrome with `--remote-debugging-port=9222`.

**Pros**: Full protocol access, simpler extension code.
**Cons**: Requires Chrome relaunch, port 9222 exposure (localhost-only but still a surface).

#### Path B: Chrome Extension with Native Messaging (Recommended for Distribution)

Uses standard Chrome Extension APIs without CDP port exposure:

```
Chrome Extension
  ├── chrome.devtools.inspectedWindow (access to console)
  ├── chrome.devtools.network (network failures)
  ├── Background service worker
  └── chrome.runtime.connectNative() → Native messaging host → Crush
```

**Pros**: No port exposure, no Chrome relaunch, standard extension distribution.
**Cons**: More moving parts, requires native messaging host binary.

**Recommendation**: Start with CDP for internal development. Build the native
messaging path before public distribution. The payload schema remains identical
for both paths.

### Payload Schema: Console Error Request

```json
{
  "schemaVersion": "1.0",
  "event": "console_error_fix_request",
  "source": "chrome",
  "timestamp": "2024-01-15T10:40:00.000Z",
  "browser": {
    "name": "Chrome",
    "version": "120.0.6099.129"
  },
  "page": {
    "url": "https://example.com/dashboard",
    "title": "Dashboard - MyApp"
  },
  "error": {
    "type": "error",
    "message": "Failed to fetch user data",
    "stackTrace": [
      {
        "function": "fetchUser",
        "file": "https://example.com/app.js",
        "line": 127,
        "column": 15
      },
      {
        "function": "handleClick",
        "file": "https://example.com/app.js",
        "line": 89,
        "column": 8
      }
    ]
  },
  "exception": {
    "className": "TypeError",
    "message": "Cannot read properties of undefined (reading 'id')",
    "fullDescription": "TypeError: Cannot read properties of undefined (reading 'id')\n    at fetchUser (app.js:127:15)"
  },
  "context": {
    "recentConsoleEntries": [
      { "type": "log", "message": "Fetching user 42..." },
      { "type": "warn", "message": "API response missing 'user' field" }
    ],
    "networkContext": {
      "lastRequest": {
        "url": "https://api.example.com/users/42",
        "status": 404,
        "statusText": "Not Found"
      }
    }
  }
}
```

### Node.js Libraries

| Library                    | Use Case                     | Notes                          |
| -------------------------- | ---------------------------- | ------------------------------ |
| `chrome-remote-interface`  | Direct CDP access            | Full protocol, low-level       |
| `puppeteer`                | High-level browser control   | Includes `createCDPSession()`  |
| `playwright`               | Cross-browser automation     | CDP access via `cdpSession()`  |

Recommendation: Use `chrome-remote-interface` for the Crush integration—it's
lightweight and provides direct access to all CDP events.

### UX Flow

1. User has Chrome open with remote debugging enabled.
2. Browser extension shows console errors in a sidebar.
3. User clicks "Send to Crush" on an error entry.
4. Extension collects:
   - Error message and stack trace
   - Recent console history (context)
   - Last network request (if relevant)
   - Page URL and metadata
5. Extension POSTs to `http://localhost:9119/context`.
6. Crush TUI shows: "Chrome: TypeError in app.js:127".
7. User reviews and submits.

---

## Phase 3: Docker Integration

### Scope

- Container log streaming (user-triggered)
- Container event capture (start, stop, die, OOM)
- Compose service context

### Docker Engine API

Docker exposes a REST API via Unix socket (`/var/run/docker.sock`) or TCP.

#### Connection Methods

| Method       | Address                         | Security                        |
| ------------ | ------------------------------- | ------------------------------- |
| Unix socket  | `/var/run/docker.sock`          | File permissions (recommended)  |
| TCP          | `tcp://localhost:2375`          | Unencrypted (not recommended)   |
| TLS          | `tcp://hostname:2376`           | Mutual TLS with certificates    |

#### Container Logs Endpoint

```
GET /v1.43/containers/{id}/logs?stdout=true&stderr=true&timestamps=true&tail=100&follow=false
```

**Query Parameters:**

| Parameter    | Type    | Default | Description                              |
| ------------ | ------- | ------- | ---------------------------------------- |
| `stdout`     | boolean | false   | Include stdout                           |
| `stderr`     | boolean | false   | Include stderr                           |
| `follow`     | boolean | false   | Stream continuously                      |
| `timestamps` | boolean | false   | Add RFC3339Nano timestamps               |
| `tail`       | string  | "all"   | Number of lines from end                 |
| `since`      | string  | -       | Only logs since timestamp                |
| `until`      | string  | -       | Only logs until timestamp                |

**Response:**

Multiplexed stream with header format:

```
[STREAM_TYPE: 1 byte][0][0][0][SIZE: 4 bytes][PAYLOAD: SIZE bytes]

STREAM_TYPE:
  1 = stdout
  2 = stderr
```

#### Container Events Endpoint

```
GET /v1.43/events?filters={"type":["container"],"event":["die","oom","start","stop"]}
```

**Event Types:**

| Type        | Actions                                                                 |
| ----------- | ----------------------------------------------------------------------- |
| `container` | create, start, stop, restart, pause, unpause, kill, die, oom, destroy  |
| `image`     | pull, push, delete, tag                                                |
| `network`   | create, connect, disconnect, destroy                                   |
| `volume`    | create, mount, unmount, destroy                                        |

**Event Payload:**

```json
{
  "Type": "container",
  "Action": "die",
  "Actor": {
    "ID": "abc123...",
    "Attributes": {
      "name": "myapp",
      "image": "myimage:latest",
      "exitCode": "1"
    }
  },
  "scope": "local",
  "time": 1705312200,
  "timeNano": 1705312200000000000
}
```

#### Container Inspection

```
GET /v1.43/containers/{id}/json
```

Returns full container state including:

- `State.Status`: running, exited, paused, etc.
- `State.ExitCode`: exit code if stopped
- `State.Error`: error message if failed
- `Config.Cmd`: command being run
- `Config.Env`: environment variables
- `LogPath`: path to log file

### Payload Schema: Container Failure Request

```json
{
  "schemaVersion": "1.0",
  "event": "container_failure_fix_request",
  "source": "docker",
  "timestamp": "2024-01-15T10:45:00.000Z",
  "container": {
    "id": "abc123def456",
    "name": "myapp-web-1",
    "image": "myapp:latest",
    "status": "exited",
    "exitCode": 1
  },
  "compose": {
    "project": "myapp",
    "service": "web",
    "file": "/Users/dev/myapp/docker-compose.yml"
  },
  "failure": {
    "type": "exit",
    "exitCode": 1,
    "oomKilled": false,
    "error": "Error: Cannot connect to database"
  },
  "logs": {
    "tail": [
      "2024-01-15T10:44:55Z Starting application...",
      "2024-01-15T10:44:56Z Connecting to postgres://db:5432...",
      "2024-01-15T10:44:58Z ERROR: Connection refused",
      "2024-01-15T10:44:58Z Error: Cannot connect to database",
      "2024-01-15T10:44:58Z    at Database.connect (index.js:42:15)"
    ],
    "stderr": [
      "ERROR: Connection refused"
    ]
  },
  "context": {
    "relatedContainers": [
      {
        "name": "myapp-db-1",
        "status": "running",
        "health": "healthy"
      }
    ],
    "networks": ["myapp_default"],
    "recentEvents": [
      { "action": "start", "time": "2024-01-15T10:44:55Z" },
      { "action": "die", "time": "2024-01-15T10:44:58Z" }
    ]
  }
}
```

### UX Flow

**Important**: Docker integration is user-triggered, not auto-push. The watcher
caches failure state but does not automatically POST to `/context`. This
maintains consistency with the "event-aware, user-triggered" philosophy and
prevents context flooding from crash loops.

1. Docker watcher (background) detects container crash or OOM.
2. Watcher caches failure state locally (container ID, exit code, timestamp).
3. Crush TUI shows indicator: "Docker: 1 container failed" (pull from cache).
4. User clicks indicator or runs `crush docker inspect myapp-web-1`.
5. Crush pulls on demand:
   - Recent logs (tail 100)
   - Container state and exit code
   - Related container health
   - Compose context
6. TUI renders context block: "Docker: myapp-web-1 exited with code 1".
7. User reviews and asks for help.

---

## Local API Server

Binds to `127.0.0.1:9119` only (no network exposure).

### Endpoints

#### `POST /context`

Inject context into the pending message. User must manually submit.

**Request:**

```json
{
  "event": "diagnostic_fix_request",
  "source": "vscode",
  ...
}
```

**Response:**

```json
{
  "status": "accepted",
  "contextId": "ctx_abc123"
}
```

#### `POST /submit`

Inject context and auto-trigger agent run.

**Request:**

```json
{
  "event": "test_failure_fix_request",
  "source": "vscode",
  "autoRun": true,
  "prompt": "Fix this test failure",
  ...
}
```

**Response (success):**

```json
{
  "status": "running",
  "sessionId": "sess_xyz789"
}
```

**Response (agent already running):**

```json
{
  "status": "error",
  "code": 409,
  "message": "Agent already running"
}
```

**Concurrency protection**: Max one agent run at a time. If `/submit` is called
while agent is executing, return `409 Conflict`. Caller should wait or retry.

#### `GET /status`

Return current Crush state.

**Response:**

```json
{
  "running": true,
  "session": {
    "id": "sess_xyz789",
    "model": "claude-sonnet-4-20250514"
  },
  "pendingContext": [
    {
      "id": "ctx_abc123",
      "source": "vscode",
      "event": "diagnostic_fix_request",
      "summary": "TypeScript error in parser.ts:15"
    }
  ]
}
```

### Context Block Rendering

When context is received, the TUI renders a styled, collapsible block with age
indicator based on `ReceivedAt`:

```
┌─ VS Code: TypeScript error (2m ago) ─────────────────────────────────────────┐
│ File: src/utils/parser.ts:15                                                 │
│ Error: Property 'foo' does not exist on type 'Bar' (TS2339)                  │
│ Source: typescript                                                           │
│                                                                              │
│ Related: Type 'Bar' is defined in src/types/index.ts:5                       │
└──────────────────────────────────────────────────────────────────────────────┘
┌─ Docker: myapp-web-1 exited (14m ago) ───────────────────────────────────────┐
│ Exit code: 1                                                                 │
│ Image: myapp:latest                                                          │
└──────────────────────────────────────────────────────────────────────────────┘
```

Age helps users reason about relevance without adding expiry complexity.
Duplicates show count: "VS Code: TypeScript error (×3, 2m ago)".

---

## MCP Integration (Outbound)

In addition to receiving context (inbound), Crush can use MCP servers for
outbound actions:

### VS Code MCP Server

```json
{
  "mcp": {
    "vscode": {
      "type": "stdio",
      "command": "node",
      "args": ["/path/to/vscode-mcp-server.js"]
    }
  }
}
```

**Tools provided:**

- `vscode_open_file(path, line?)`: Open file in editor
- `vscode_highlight_range(path, range)`: Highlight code range
- `vscode_apply_edit(path, edits)`: Apply text edits
- `vscode_show_diagnostics(path)`: Show diagnostics panel

### Docker MCP Server

```json
{
  "mcp": {
    "docker": {
      "type": "stdio",
      "command": "docker-mcp-server"
    }
  }
}
```

**Tools provided:**

- `docker_logs(container, tail?, since?)`: Get container logs
- `docker_exec(container, command)`: Execute command in container
- `docker_inspect(container)`: Get container details
- `docker_compose_up(service?)`: Start services
- `docker_compose_restart(service)`: Restart service

---

## Phased Rollout

### Phase 1: Diagnostic-Aware Fix (v1)

- VS Code extension with "Fix with Crush" context menu
- Diagnostic payload with structured context
- Local API server with `/context` endpoint
- TUI context block rendering
- No background sync, no ambient awareness

### Phase 2: Test Failures + Docker (v1.1)

- Test failure integration via VS Code Test API
- Docker log capture and container event monitoring
- `/submit` endpoint for auto-run scenarios
- MCP servers for VS Code and Docker actions

### Phase 3: Chrome Console (v1.2)

- Browser extension for Chrome
- CDP connection for development (native messaging for distribution)
- Network failure context
- Stack traces as-is (defer source map resolution to DevTools)

### Phase 4: Debug State Snapshot (v2)

- Capture variables and call stack on breakpoint hit
- User-triggered snapshot ("Explain why this is null")
- Variable serialization with depth limits
- No continuous streaming

### Phase 5: Ambient Awareness (v2+, Maybe)

- Explicit toggle in settings
- Clear status indicator ("Crush is watching")
- Debounced state streaming (cursor, visible range)
- Fully transparent behavior
- Requires careful UX framing

---

## Security Considerations

### Local API Server

- Bind to `127.0.0.1:9119` only (no network exposure)
- Rate limit `/submit` to prevent runaway agent invocations

### CSRF Protection

Even bound to localhost, malicious webpages can POST to `http://localhost:9119`.
Token requirements depend on the source:

**Trusted sources (no token required)**:
- `vscode`: Extensions run in a trusted context, not a browser sandbox
- `docker`: Local process, no web attack vector

**Untrusted sources (token required)**:
- `chrome`: Browser extensions can be triggered by malicious web content
- Any unknown or missing source

```go
// Trusted sources run in isolated contexts with no CSRF risk.
var trustedSources = map[string]bool{
    "vscode": true,
    "docker": true,
}

func (s *Server) withAuth(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // GET requests don't need auth.
        if r.Method == http.MethodGet {
            next.ServeHTTP(w, r)
            return
        }

        // Check X-Crush-Source header for trusted sources.
        source := r.Header.Get("X-Crush-Source")
        if trustedSources[source] {
            next.ServeHTTP(w, r)
            return
        }

        // Untrusted sources (browser, unknown) require token.
        token := r.Header.Get("X-Crush-Token")
        if token != s.session.Token() {
            http.Error(w, "Invalid or missing X-Crush-Token", http.StatusUnauthorized)
            return
        }

        next.ServeHTTP(w, r)
    })
}
```

Trusted extensions should set `X-Crush-Source: vscode` (or `docker`). Browser
extensions must still provide the token via `X-Crush-Token` header.

### Chrome DevTools Protocol

- CDP has no authentication—localhost only
- Never expose `--remote-debugging-port` to network
- Browser extension should validate origin of messages

### Docker API

- Unix socket access requires Docker group membership
- Avoid TCP without TLS in production
- MCP server should not expose dangerous operations (rm, prune) by default

---

## Safety Features

### Context Buffer Limits

Session-scoped bounded buffer with hard limits:

| Limit               | Value      | Behavior                                 |
| ------------------- | ---------- | ---------------------------------------- |
| Max contexts        | 50         | Drop oldest on overflow                  |
| Max total memory    | 20 MB      | Drop oldest until under limit            |
| Max payload size    | 1 MB       | Reject with 413 if exceeded              |
| Max `logs.tail`     | 500 lines  | Truncate oldest if exceeded              |
| Max `stackTrace`    | 50 frames  | Truncate deepest frames                  |
| Max `recentConsole` | 20 entries | Rolling window, oldest dropped           |

No expiry timers. Buffer clears when session ends.

### Context Deduplication

Session-scoped deduplication to prevent UI clutter:

```go
type ContextEntry struct {
    ID         string
    Hash       string    // SHA256 of normalized fields (see below)
    Count      int       // Increment on duplicate
    ReceivedAt time.Time // System clock at ingestion (authoritative)
}
```

**Hash includes** (for uniqueness):
- `eventType`
- `filePath`
- `range.start.line`
- `range.end.line` (multi-line diagnostics can shift meaning)
- `diagnostic.code` (e.g., TS2339)
- `diagnostic.message`
- `container.exitCode`
- `exception.className`

**Hash excludes** (volatile/non-deterministic):
- All timestamps
- Container full IDs (normalize to first 12 chars)
- Counts and sequence numbers
- File versions (handled separately via drift detection)

**Schema versioning**:
```go
const HashVersion = "1" // Bump when changing hash fields
hash := SHA256(HashVersion + eventType + filePath + ...)
```

**Rules**:
- If `Hash` matches existing context: increment `Count`, return existing `contextId`.
- Display count in TUI: "VS Code: TypeScript error in parser.ts:15 (×3)".
- No cross-session dedupe needed.

### Workspace Routing

Every payload must include a deterministic workspace identifier:

```json
{
  "workspace": {
    "id": "a1b2c3d4e5f67890",
    "root": "/Users/dev/myproject",
    "name": "myproject"
  }
}
```

**Workspace ID generation**:
- Deterministic: `SHA256(absoluteRootPath).slice(0, 16)`
- No persistence required—same path always produces same ID

**Routing rules**:
- One Crush process == one workspace session.
- First context for unknown workspace **lazily creates session**.
- If context arrives for a *different* workspace than the active session:
  → Reject with `409 Conflict` and message: "Context workspace does not match active session."
- **Critical**: Validate workspace BEFORE dedupe and buffering to prevent cross-session contamination.
- Never queue contexts across sessions.
- Never defer routing—deterministic behavior only.

```go
func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
    var payload ContextPayload
    // ... parse payload ...

    // Validate workspace FIRST, before any state mutation
    if s.activeWorkspaceID != "" && payload.Workspace.ID != s.activeWorkspaceID {
        http.Error(w, "Context workspace does not match active session", http.StatusConflict)
        return
    }

    // Now safe to dedupe and buffer
    // ...
}
```

### Timestamp Handling

External timestamps are advisory metadata only. At `/context` ingestion:

```go
entry.ReceivedAt = time.Now() // System clock is authoritative
entry.SourceTimestamp = payload.Timestamp // Optional, for debugging
```

This prevents ordering bugs across sources with inconsistent timestamp formats
(CDP monotonic time, Docker epoch seconds, etc.).

---

## Resolved Questions

### 1. Context Merging

**Decision**: Never auto-merge. Render as stacked blocks:

```
┌─ VS Code: TypeScript error ──────────────────────────────┐
│ ...                                                      │
└──────────────────────────────────────────────────────────┘
┌─ Docker: container exited ───────────────────────────────┐
│ ...                                                      │
└──────────────────────────────────────────────────────────┘
```

At submit time, the agent coordinator merges structured contexts into a single
prompt. The UI remains compositional; synthesis happens in the agent layer.

### 2. Context Lifetime

**Decision**: No expiry. Bounded buffer with oldest-drop overflow.

- Max 50 contexts in session
- Oldest dropped when full
- All contexts cleared on session end
- No timers, no archive state, no lifecycle complexity

### 3. Source Maps

**Decision**: Defer to DevTools in v1. Do not implement server-side resolution.

**Rationale**:
- Requires fetching `.map` files (network, CORS, auth complexity)
- Path rewriting is error-prone
- Security implications of fetching arbitrary URLs
- DevTools already resolves stack frames when source maps are available

**Approach**:
- If DevTools provides resolved frames: use them.
- Otherwise: send raw minified stack. Agent can still reason about error type and message.

### 4. Multi-Workspace

**Decision**: Namespace contexts via deterministic `workspace.id`.

Minimum viable: prevent collisions between two VS Code windows pointing at
different projects. Full session orchestration is out of scope.

### 5. Offline Mode

**Decision**: Extensions should implement simple retry with backoff.

```typescript
async function sendWithRetry(payload: object, maxRetries = 3) {
  for (let i = 0; i < maxRetries; i++) {
    try {
      const response = await fetch(`${CRUSH_API}/context`, { ... });
      if (response.ok) return;
    } catch {
      await sleep(1000 * Math.pow(2, i)); // 1s, 2s, 4s
    }
  }
  // Surface "Crush unavailable" in extension UI
}
```

Do not queue indefinitely. After max retries, show user that Crush is not
running. They can retry manually when Crush is available.

---

## Appendix A: VS Code Extension Skeleton

```typescript
import * as vscode from 'vscode';
import * as crypto from 'crypto';

const CRUSH_API = 'http://localhost:9119';

// Deterministic workspace ID from root path (no persistence needed).
function getWorkspaceId(root: string): string {
  return crypto.createHash('sha256').update(root).digest('hex').slice(0, 16);
}

interface DiagnosticPayload {
  schemaVersion: '1.0';
  event: 'diagnostic_fix_request';
  source: 'vscode';
  timestamp: string;
  workspace: { id: string; root: string; name: string };
  file: { path: string; languageId: string; version: number };
  diagnostic: {
    range: { start: { line: number; character: number }; end: { line: number; character: number } };
    message: string;
    severity: string;
    source?: string;
    code?: string | number;
  };
  relatedDiagnostics: Array<{ file: string; range: object; message: string }>;
  context: {
    // Metadata only. Crush pulls actual code via MCP at execution time.
    diagnosticRange: { start: { line: number; character: number }; end: { line: number; character: number } };
    importRanges: Array<{ start: { line: number; character: number }; end: { line: number; character: number } }>;
    fileVersion: number;
  };
}

export function activate(context: vscode.ExtensionContext) {
  const fixWithCrush = vscode.commands.registerCommand(
    'crush.fixDiagnostic',
    async (diagnostic: vscode.Diagnostic, uri: vscode.Uri) => {
      const document = await vscode.workspace.openTextDocument(uri);
      const workspaceFolder = vscode.workspace.getWorkspaceFolder(uri);
      const workspaceRoot = workspaceFolder?.uri.fsPath || '';

      const payload: DiagnosticPayload = {
        schemaVersion: '1.0',
        event: 'diagnostic_fix_request',
        source: 'vscode',
        timestamp: new Date().toISOString(),
        workspace: {
          id: getWorkspaceId(workspaceRoot),
          root: workspaceRoot,
          name: workspaceFolder?.name || '',
        },
        file: {
          path: vscode.workspace.asRelativePath(uri),
          languageId: document.languageId,
          version: document.version,
        },
        diagnostic: {
          range: {
            start: { line: diagnostic.range.start.line, character: diagnostic.range.start.character },
            end: { line: diagnostic.range.end.line, character: diagnostic.range.end.character },
          },
          message: diagnostic.message,
          severity: vscode.DiagnosticSeverity[diagnostic.severity],
          source: diagnostic.source,
          code: typeof diagnostic.code === 'object' ? diagnostic.code.value : diagnostic.code,
        },
        relatedDiagnostics: (diagnostic.relatedInformation || []).map((info) => ({
          file: vscode.workspace.asRelativePath(info.location.uri),
          range: info.location.range,
          message: info.message,
        })),
        context: {
          diagnosticRange: {
            start: { line: Math.max(0, diagnostic.range.start.line - 10), character: 0 },
            end: { line: diagnostic.range.end.line + 10, character: 0 },
          },
          importRanges: getImportRanges(document),
          fileVersion: document.version,
        },
      };

      await sendWithRetry(payload);
    }
  );

  // Register as code action provider
  const codeActionProvider = vscode.languages.registerCodeActionsProvider(
    { scheme: 'file' },
    {
      provideCodeActions(document, range, context) {
        const actions: vscode.CodeAction[] = [];

        for (const diagnostic of context.diagnostics) {
          if (diagnostic.severity === vscode.DiagnosticSeverity.Error) {
            const action = new vscode.CodeAction('Fix with Crush', vscode.CodeActionKind.QuickFix);
            action.command = {
              command: 'crush.fixDiagnostic',
              title: 'Fix with Crush',
              arguments: [diagnostic, document.uri],
            };
            action.diagnostics = [diagnostic];
            actions.push(action);
          }
        }

        return actions;
      },
    }
  );

  context.subscriptions.push(fixWithCrush, codeActionProvider);
}

// Returns line ranges where imports are located (metadata only).
function getImportRanges(
  document: vscode.TextDocument
): Array<{ start: { line: number; character: number }; end: { line: number; character: number } }> {
  const ranges: Array<{ start: { line: number; character: number }; end: { line: number; character: number } }> = [];
  const text = document.getText();
  const importRegex = /^import\s+.*$/gm;
  let match;
  while ((match = importRegex.exec(text)) !== null) {
    const pos = document.positionAt(match.index);
    ranges.push({
      start: { line: pos.line, character: 0 },
      end: { line: pos.line, character: match[0].length },
    });
  }
  return ranges;
}

async function sendWithRetry(payload: object, maxRetries = 3): Promise<void> {
  for (let i = 0; i < maxRetries; i++) {
    try {
      const response = await fetch(`${CRUSH_API}/context`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      if (response.ok) {
        vscode.window.showInformationMessage('Context sent to Crush');
        return;
      }
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 1000 * Math.pow(2, i)));
    }
  }
  vscode.window.showErrorMessage('Crush is not running');
}
```

---

## Appendix B: Chrome Extension Skeleton

**Note**: Like Docker, Chrome caches errors locally. It does NOT auto-push to
`/context`. User clicks "Send to Crush" in the extension sidebar to POST.

```typescript
// background.ts
import CDP from 'chrome-remote-interface';

interface CachedError {
  id: string;
  type: string;
  message: string;
  stackTrace: Array<{ function: string; file: string; line: number; column: number; minified: boolean }>;
  pageUrl: string;
  pageTitle: string;
  timestamp: Date;
}

interface ConsoleErrorPayload {
  schemaVersion: '1.0';
  event: 'console_error_fix_request';
  source: 'chrome';
  timestamp: string;
  page: { url: string; title: string };
  error: {
    type: string;
    message: string;
    stackTrace: Array<{ function: string; file: string; line: number; column: number; minified: boolean }>;
  };
}

const CRUSH_API = 'http://localhost:9119';

// In-memory cache of errors (sidebar displays this)
const errorCache: Map<string, CachedError> = new Map();
const MAX_CACHED_ERRORS = 50;

async function connectToCDP() {
  try {
    const client = await CDP({ port: 9222 });
    const { Runtime, Page } = client;

    await Runtime.enable();
    await Page.enable();

    // Cache errors—do NOT auto-push to Crush
    client.on('Runtime.consoleAPICalled', (params) => {
      if (params.type === 'error') {
        cacheError(params);
      }
    });

    client.on('Runtime.exceptionThrown', (params) => {
      cacheException(params);
    });

    console.log('Connected to Chrome DevTools');
  } catch (error) {
    console.error('Failed to connect to Chrome DevTools:', error);
  }
}

function cacheError(params: any) {
  const id = crypto.randomUUID();
  const cached: CachedError = {
    id,
    type: params.type,
    message: params.args.map((a: any) => a.value || a.description).join(' '),
    stackTrace: (params.stackTrace?.callFrames || []).map((frame: any) => ({
      function: frame.functionName || '<anonymous>',
      file: frame.url,
      line: frame.lineNumber + 1,
      column: frame.columnNumber + 1,
      minified: frame.url?.includes('.min.js') || frame.url?.includes('.min.ts'),
    })),
    pageUrl: '', // Populated from Page domain
    pageTitle: '',
    timestamp: new Date(),
  };

  // Enforce bounded cache
  if (errorCache.size >= MAX_CACHED_ERRORS) {
    const oldest = [...errorCache.keys()][0];
    errorCache.delete(oldest);
  }
  errorCache.set(id, cached);

  // Notify sidebar to update (via chrome.runtime messaging)
  chrome.runtime.sendMessage({ type: 'error_cached', error: cached });
}

// Called when user clicks "Send to Crush" in sidebar
async function sendErrorToCrush(errorId: string): Promise<void> {
  const cached = errorCache.get(errorId);
  if (!cached) return;

  const payload: ConsoleErrorPayload = {
    schemaVersion: '1.0',
    event: 'console_error_fix_request',
    source: 'chrome',
    timestamp: new Date().toISOString(), // Fresh timestamp at send time
    page: { url: cached.pageUrl, title: cached.pageTitle },
    error: {
      type: cached.type,
      message: cached.message,
      stackTrace: cached.stackTrace,
    },
  };

  try {
    await fetch(`${CRUSH_API}/context`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
  } catch (error) {
    console.error('Failed to send to Crush:', error);
  }
}

// Export for sidebar
export { errorCache, sendErrorToCrush };

connectToCDP();
```

---

## Appendix C: Docker Integration Skeleton

**Note**: This watcher caches failure state only. It does NOT auto-push to
`/context`. The TUI queries this cache and only POSTs when user requests.

```go
package docker

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// CachedFailure holds minimal failure state for TUI display.
// Full payload is built on-demand when user requests context.
type CachedFailure struct {
	ContainerID   string
	ContainerName string
	Image         string
	ExitCode      int
	OOMKilled     bool
	Action        string // "die" or "oom"
	Timestamp     time.Time
}

type DockerWatcher struct {
	client  *client.Client
	mu      sync.RWMutex
	cache   map[string]*CachedFailure // containerID -> failure
}

func NewDockerWatcher() (*DockerWatcher, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return nil, err
	}
	return &DockerWatcher{
		client: cli,
		cache:  make(map[string]*CachedFailure),
	}, nil
}

// WatchEvents listens for container failures and caches them.
// Does NOT push to Crush—that happens on user request via BuildPayload.
func (w *DockerWatcher) WatchEvents(ctx context.Context) error {
	eventsChan, errChan := w.client.Events(ctx, events.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("type", "container"),
			filters.Arg("event", "die"),
			filters.Arg("event", "oom"),
		),
	})

	for {
		select {
		case event := <-eventsChan:
			if event.Action == "die" || event.Action == "oom" {
				w.cacheFailure(ctx, event)
			}
		case err := <-errChan:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (w *DockerWatcher) cacheFailure(ctx context.Context, event events.Message) {
	inspect, err := w.client.ContainerInspect(ctx, event.Actor.ID)
	if err != nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.cache[event.Actor.ID[:12]] = &CachedFailure{
		ContainerID:   event.Actor.ID[:12],
		ContainerName: strings.TrimPrefix(inspect.Name, "/"),
		Image:         inspect.Config.Image,
		ExitCode:      inspect.State.ExitCode,
		OOMKilled:     inspect.State.OOMKilled,
		Action:        event.Action,
		Timestamp:     time.Unix(event.Time, 0),
	}
}

// GetFailures returns cached failures for TUI display.
func (w *DockerWatcher) GetFailures() []*CachedFailure {
	w.mu.RLock()
	defer w.mu.RUnlock()

	failures := make([]*CachedFailure, 0, len(w.cache))
	for _, f := range w.cache {
		failures = append(failures, f)
	}
	return failures
}

// ClearFailure removes a failure from cache (after user dismisses).
func (w *DockerWatcher) ClearFailure(containerID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.cache, containerID)
}

// BuildPayload fetches full context on-demand when user requests.
// Called when user clicks "Investigate" on a cached failure.
func (w *DockerWatcher) BuildPayload(ctx context.Context, containerID string) (*ContainerFailurePayload, error) {
	w.mu.RLock()
	cached, ok := w.cache[containerID]
	w.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no cached failure for container %s", containerID)
	}

	logs, err := w.getRecentLogs(ctx, containerID, 100)
	if err != nil {
		logs = []string{}
	}

	payload := &ContainerFailurePayload{
		SchemaVersion: "1.0",
		Event:         "container_failure_fix_request",
		Source:        "docker",
		Timestamp:     time.Now(), // Use current time, not cached time
	}
	payload.Container.ID = cached.ContainerID
	payload.Container.Name = cached.ContainerName
	payload.Container.Image = cached.Image
	payload.Container.ExitCode = cached.ExitCode
	payload.Failure.Type = cached.Action
	payload.Failure.ExitCode = cached.ExitCode
	payload.Failure.OOMKilled = cached.OOMKilled
	payload.Logs.Tail = logs

	return payload, nil
}

func (w *DockerWatcher) getRecentLogs(ctx context.Context, containerID string, tail int) ([]string, error) {
	reader, err := w.client.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Tail:       fmt.Sprintf("%d", tail),
	})
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	// Docker log streams are multiplexed (stdout/stderr headers).
	// Must demux to avoid binary header bytes in logs.
	var stdoutBuf, stderrBuf bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdoutBuf, &stderrBuf, reader); err != nil {
		return nil, err
	}

	// Combine stdout and stderr, split into lines
	combined := stdoutBuf.String() + stderrBuf.String()
	return strings.Split(strings.TrimSpace(combined), "\n"), nil
}

type ContainerFailurePayload struct {
	SchemaVersion string    `json:"schemaVersion"`
	Event         string    `json:"event"`
	Source        string    `json:"source"`
	Timestamp     time.Time `json:"timestamp"`
	Container     struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Image    string `json:"image"`
		ExitCode int    `json:"exitCode"`
	} `json:"container"`
	Failure struct {
		Type      string `json:"type"`
		ExitCode  int    `json:"exitCode"`
		OOMKilled bool   `json:"oomKilled"`
	} `json:"failure"`
	Logs struct {
		Tail []string `json:"tail"`
	} `json:"logs"`
}
```
