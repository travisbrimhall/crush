# VS Code MCP Server Technical Design

## Problem Statement

Crush can receive context from VS Code (diagnostics, selections) but cannot
command VS Code to perform actions. When the agent says "see line 42 of
parser.go", the user must manually navigate there. The magic moment we want:
"Crush shows me exactly where to look."

## Goals

1. Agent can open files in VS Code at specific lines
2. Agent can highlight code ranges to draw attention
3. Agent can show diagnostics panel for a file

## Non-Goals

- Replacing the existing context ingestion flow (that stays as-is)
- Building a general-purpose VS Code automation platform
- Supporting other editors in this design (Cursor, Zed, etc. can follow later)
- Applying edits via VS Code (agent edits files directly via `edit` tool)
- Participating in Copilot's MCP tool ecosystem

---

## Buy vs Build Analysis

### Option A: Use Existing MCP Server

**Searched for**: VS Code MCP servers that expose editor actions.

**Finding**: No existing MCP server exposes VS Code editor commands like
`openFile`, `highlightRange`, or `showDiagnostics`. The closest are:

- `@modelcontextprotocol/server-filesystem`: File read/write, not editor control
- Various "codebase" servers: Read-only analysis, no editor integration
- VS Code's built-in MCP support: Consumes MCP servers, doesn't expose editor APIs

**Verdict**: Must build. No off-the-shelf solution exists.

### Option B: Build Minimal MCP Server in Extension

Extend our existing `vscode-crush` extension to also act as an MCP server. The
extension already has VS Code API access and connects to Crush.

**Pros**:
- Single extension, no additional install
- Direct access to `vscode.*` APIs
- Reuses existing connection discovery (`.crush/server.json`)
- Simpler deployment (one artifact)

**Cons**:
- Extension becomes bidirectional (context push + MCP serve)
- Must implement MCP server protocol in TypeScript

**Verdict**: Build this. Consolidates functionality, leverages existing code.

### Option C: Separate Native MCP Server Binary

Ship a standalone binary that VS Code extension spawns, communicating via stdio.

**Pros**:
- Clean separation of concerns
- Could be reused by other editors

**Cons**:
- Two artifacts to maintain
- Binary must somehow call back into VS Code (complex IPC)
- Adds deployment complexity

**Verdict**: Reject. The IPC problem is nasty—VS Code APIs are only accessible
from within the extension process.

---

## Why Not Use VS Code's Built-in MCP?

VS Code has native MCP support, but it's wired specifically for Copilot:

```
MCP Server → McpService → McpLanguageModelToolContribution → LanguageModelToolsService (Copilot)
```

The `LanguageModelToolsService` is Copilot's tool registry, not a generic tool bus. Key limitations:

- `vscode.lm.tools` is **discoverable but not invokable** from extensions
- No `invokeTool()` API exposed to third parties
- Tool execution is coupled to Copilot's chat pipeline

**We evaluated three paths:**

| Path | Approach | Verdict |
|------|----------|---------|
| A | Fork VS Code's MCP handling | Massive maintenance burden, unjustifiable |
| B | Proxy through `vscode.lm.tools` | Blocked by design—no invoke API |
| C | Build minimal MCP server with direct `vscode.*` APIs | ✓ Correct choice |

**The insight:** VS Code MCP is an AI tool routing layer. We just need editor RPC.

We're not trying to participate in Copilot's tool ecosystem. We're trying to let
Crush say "open this file" and have VS Code do it. Different problem, simpler
solution.

---

## Infrastructure Decisions

These decisions ensure the system behaves like infrastructure—recoverable,
reconnectable, deterministic, and never hanging.

### 1. Transport: Plain HTTP

| Option | Verdict | Rationale |
|--------|---------|-----------|
| HTTP (JSON-RPC) | ✓ Use this | Tools are short-lived editor actions |
| SSE | ✗ | No streaming needed |
| Streamable HTTP | ✗ | Overkill for request/response |

We don't need token streaming or server push. Keep it boring.

### 2. Extension Activation: Start Immediately

MCP server starts on extension activation, not lazily.

**Flow**:
1. VS Code opens workspace
2. Extension activates
3. MCP server binds to random available port
4. Discovery file written to `.crush/vscode-mcp.json`
5. Server ready for connections

**Rationale**: Predictable. No race conditions. Crush can connect anytime.

### 3. Port Assignment: OS-Assigned Random Port

No hardcoded port. Let the OS assign an available port.

**Flow**:
1. `server.listen(0)` — OS assigns free port
2. Read assigned port from `server.address().port`
3. Write to discovery file

**Rationale**: Eliminates port conflict logic entirely. Discovery file is the
source of truth.

### 4. Discovery File Contract

**Location**: `.crush/vscode-mcp.json` (workspace root)

**Schema**:
```json
{
  "protocol": "mcp-http",
  "version": "1.0",
  "host": "127.0.0.1",
  "port": 53421,
  "workspaceRoot": "/Users/dev/myproject",
  "pid": 12345,
  "startedAt": "2024-01-15T10:30:00.000Z"
}
```

**Lifecycle**:
- Written on extension activation
- Overwritten on every activation (handles port changes)
- Deleted on extension deactivation (clean shutdown)
- Stale file (PID dead) should be ignored by Crush

### 5. Crush-side Auto-Discovery

**Startup behavior**:
1. Check for `.crush/vscode-mcp.json`
2. If exists and PID alive → connect
3. If missing or stale → no VS Code MCP available (not an error)

**Runtime behavior**:
- Watch file for changes (fsnotify)
- On change → reconnect with new port
- On delete → mark VS Code MCP as disconnected

**Retry logic**:
- If Crush starts before VS Code: poll every 2s for up to 30s, then give up
- User can manually trigger reconnect via TUI command

### 6. Error Handling Model

All errors return structured JSON-RPC responses. Never crash.

| Scenario | Error Code | Message |
|----------|------------|---------|
| File not found | -32001 | "File not found: {path}" |
| Path outside workspace | -32002 | "Access denied: path outside workspace" |
| VS Code busy/timeout | -32003 | "Editor timeout: operation took >5s" |
| Invalid parameters | -32602 | Standard JSON-RPC invalid params |

**Timeouts**: All tool operations have a 5s max execution time. If exceeded,
return timeout error and let Crush retry or report to user.

**Connection errors**: Crush treats connection reset, invalid JSON, or timeout
as "editor disconnected" and triggers rediscovery.

### 7. Agent Prompt Integration

Two layers, both required:

**A. Tool Descriptions (capability)**

Each tool has a description that tells the model *what* it does:

```
vscode_highlight_range:
  Highlights a code range in VS Code with a temporary visual decoration.
  Use this to show the user exactly where to look instead of describing
  line numbers. The highlight auto-clears after 3 seconds.
```

**B. System Prompt (behavior)**

Crush's agent system prompt includes:

```
<vscode-integration>
You are connected to VS Code for navigation and context (read-only).

Available actions:
- open_file: Open files in the editor
- highlight_range: Show the user specific code locations visually
- read_file: Get file contents (useful for unsaved buffers)
- get_diagnostics: Get compiler/linter errors and warnings
- get_definitions: Find where symbols are defined

Prefer showing over telling. Use highlight_range instead of describing line
numbers. Do not attempt to edit files via these tools—edits are handled
separately via the edit tool.

If VS Code tools are unavailable, fall back to describing file paths and
line numbers.
</vscode-integration>
```

**Conditional inclusion**: This block is only added when VS Code MCP is connected.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           VS Code Extension                                  │
│                                                                              │
│  ┌─────────────────────┐          ┌─────────────────────────────────────┐   │
│  │  Context Push       │          │  MCP Server (HTTP, read-only)       │   │
│  │  (existing)         │          │                                     │   │
│  │                     │          │  Tools:                             │   │
│  │  POST /context ────────────►   │  - get_document_metadata            │   │
│  │                     │          │  - open_file                        │   │
│  │                     │          │  - highlight_range                  │   │
│  │                     │          │  - read_file                        │   │
│  │                     │          │  - get_diagnostics                  │   │
│  │                     │          │  - get_definitions                  │   │
│  └─────────────────────┘          └─────────────────────────────────────┘   │
│                                              │                               │
│                                              │ HTTP (random port)            │
│                                              │ written to discovery file     │
└──────────────────────────────────────────────│───────────────────────────────┘
                                               │
                                               ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Crush                                           │
│                                                                              │
│  ┌─────────────────────┐          ┌─────────────────────────────────────┐   │
│  │  MCP Client         │          │  Agent                              │   │
│  │  (existing)         │◄─────────│                                     │   │
│  │                     │  tools   │  "Show me the error in parser.go"   │   │
│  │  Auto-configured    │          │         │                           │   │
│  │  from discovery     │          │         ▼                           │   │
│  │  file               │          │  highlight_range({                  │   │
│  │                     │          │    path: "parser.go",               │   │
│  │                     │          │    version: 12,                     │   │
│  │                     │          │    start: 1842,                     │   │
│  │                     │          │    end: 1923                        │   │
│  └─────────────────────┘          │  })                                 │   │
│                                   └─────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Why HTTP (not stdio)?

The extension runs in VS Code's extension host. We can't spawn it as a child
process from Crush. Instead:

1. Extension starts an HTTP server on a random available port
2. Extension writes port to `.crush/vscode-mcp.json`
3. Crush reads discovery file and connects as MCP client

This mirrors how the context API works (extension → Crush HTTP), just reversed.

### Discovery File

Extension writes to `.crush/vscode-mcp.json`:

```json
{
  "protocol": "mcp-http",
  "version": "1.0",
  "host": "127.0.0.1",
  "port": 53421,
  "workspaceRoot": "/Users/dev/myproject",
  "pid": 12345,
  "startedAt": "2024-01-15T10:30:00.000Z"
}
```

Crush watches this file and auto-connects when it appears or changes.

---

## MCP Tools Specification

### Design Principles

The VS Code MCP server is a **read-only navigation and context surface**:

- **Read-only**: No file mutations
- **Navigation-only**: Open files, highlight ranges, show locations
- **Context-providing**: Return diagnostics, definitions, file contents
- **Non-destructive**: Cannot break a workspace

Crush owns all text changes via its own `edit` tool.

### Global Conventions

**Path Convention**:
- All paths are workspace-relative
- Never absolute paths, `../`, or tilde expansion
- Validated server-side; violations return error code 403

**Range Convention**:
- Offsets are **UTF-16 code unit offsets** (VS Code's internal representation)
- NOT byte offsets—this matters for emoji, CJK characters, and surrogate pairs
- `start`: inclusive, `end`: exclusive
- No line/column—offsets are deterministic and LLMs are bad at line math
- All tools validate: `start >= 0`, `end >= start`, `end <= document length`
- Invalid ranges return error code 400

**Document Versioning**:
Tools that operate on file contents require a `version` parameter:
- `version` is `document.version` from VS Code (increments on each change)
- If version mismatches current document, return error code 409
- This catches drift between Crush's view and VS Code's state
- Without versioning, offsets silently point to wrong text after edits

Tools requiring version: `highlight_range`, `read_file` (optional), `get_definitions`
Tools returning version: `get_document_metadata`, `read_file`, `get_diagnostics`, `get_definitions`
Tools not requiring version: `open_file`, `get_document_metadata` (just opens/fetches metadata)

**Error Model**:
Standard JSON-RPC errors with structured data:

```json
{
  "code": 403,
  "message": "Access denied: outside workspace",
  "data": { "path": "../secrets.txt" }
}
```

| Code | Meaning |
|------|---------|
| 400 | Invalid input schema |
| 403 | Security violation (path traversal) |
| 404 | File not found |
| 409 | Document version mismatch |
| 500 | Internal extension error |
| 504 | Timeout (>5s) |

**Server Identity**:
Server metadata included in `tools/list` response:

```json
{
  "serverName": "crush-vscode-mcp",
  "protocolVersion": "1.0",
  "toolSchemaVersion": "1.0",
  "workspaceRoot": "/Users/dev/myproject"
}
```

---

### Implementation Utilities

All tool implementations use these shared helpers:

```typescript
const TOOL_TIMEOUT_MS = 5000;

// Wrap all tool operations with timeout
async function withTimeout<T>(promise: Promise<T>, name: string): Promise<T> {
  const timeout = new Promise<never>((_, reject) =>
    setTimeout(() => reject(new McpError(504, `Tool ${name} timed out after 5s`)), TOOL_TIMEOUT_MS)
  );
  return Promise.race([promise, timeout]);
}

// Validate path is within workspace (call BEFORE any operation)
function validateWorkspacePath(path: string): void {
  if (path.startsWith('/') || path.startsWith('~') || path.includes('..')) {
    throw new McpError(403, 'Access denied: path must be workspace-relative', { path });
  }
  // Multi-root: resolve against first matching workspace folder
  const uri = resolveWorkspaceUri(path);
  const folder = vscode.workspace.getWorkspaceFolder(uri);
  if (!folder) {
    throw new McpError(403, 'Access denied: path outside workspace', { path });
  }
}

// Resolve workspace-relative path to URI
function resolveWorkspaceUri(path: string): vscode.Uri {
  const folders = vscode.workspace.workspaceFolders;
  if (!folders || folders.length === 0) {
    throw new McpError(500, 'No workspace folder open');
  }
  // Multi-root: use first folder as base
  return vscode.Uri.joinPath(folders[0].uri, path);
}

// Validate document version matches expected
function validateVersion(doc: vscode.TextDocument, expected: number): void {
  if (doc.version !== expected) {
    throw new McpError(409, 'Document version mismatch', {
      expectedVersion: expected,
      currentVersion: doc.version,
    });
  }
}

// Validate range is within document bounds
function validateRange(doc: vscode.TextDocument, start: number, end: number): void {
  const length = doc.getText().length;
  if (start < 0 || end < start || end > length) {
    throw new McpError(400, 'Invalid range', {
      start,
      end,
      documentLength: length,
    });
  }
}

// Check if URI is within workspace (for definition results)
function isWithinWorkspace(uri: vscode.Uri): boolean {
  return vscode.workspace.getWorkspaceFolder(uri) !== undefined;
}
```

---

### Tool 0: `get_document_metadata`

Returns document version and size without content. Use this before operations
that require version to avoid fetching full content.

**Input Schema**:
```json
{
  "type": "object",
  "required": ["path"],
  "properties": {
    "path": { "type": "string" }
  }
}
```

**Output Schema**:
```json
{
  "type": "object",
  "required": ["version", "size", "languageId"],
  "properties": {
    "version": { "type": "integer" },
    "size": { "type": "integer" },
    "languageId": { "type": "string" }
  }
}
```

**Implementation**:
```typescript
async function getDocumentMetadata(params: { path: string }) {
  validateWorkspacePath(params.path);
  const uri = resolveWorkspaceUri(params.path);
  const doc = await vscode.workspace.openTextDocument(uri);
  return {
    version: doc.version,
    size: doc.getText().length,
    languageId: doc.languageId,
  };
}
```

---

### Tool 1: `open_file`

Opens a file in VS Code editor.

**Input Schema**:
```json
{
  "type": "object",
  "required": ["path"],
  "properties": {
    "path": {
      "type": "string",
      "description": "Workspace-relative file path"
    },
    "preview": {
      "type": "boolean",
      "description": "Open in preview tab",
      "default": false
    }
  }
}
```

**Output Schema**:
```json
{
  "type": "object",
  "required": ["success"],
  "properties": {
    "success": { "type": "boolean" },
    "languageId": { "type": "string" }
  }
}
```

**Implementation**:
```typescript
async function openFile(params: { path: string; preview?: boolean }) {
  validateWorkspacePath(params.path);
  const uri = resolveWorkspaceUri(params.path);
  const doc = await vscode.workspace.openTextDocument(uri);
  await vscode.window.showTextDocument(doc, { preview: params.preview ?? false });
  return { success: true, languageId: doc.languageId };
}
```

---

### Tool 2: `highlight_range`

Visually highlights and reveals a code range. Auto-clears after 3 seconds.
Multiple highlights stack (each gets its own decoration type).

**Focus behavior**: If file is already visible, highlights without stealing focus.
If file needs to be opened, opens in preview tab with focus.

**Input Schema**:
```json
{
  "type": "object",
  "required": ["path", "version", "start", "end"],
  "properties": {
    "path": { "type": "string" },
    "version": { "type": "integer", "minimum": 0 },
    "start": { "type": "integer", "minimum": 0 },
    "end": { "type": "integer", "minimum": 0 }
  }
}
```

**Output Schema**:
```json
{
  "type": "object",
  "required": ["success"],
  "properties": {
    "success": { "type": "boolean" }
  }
}
```

**Implementation**:
```typescript
// Each highlight gets its own decoration type to allow stacking
function createHighlightDecoration() {
  return vscode.window.createTextEditorDecorationType({
    backgroundColor: new vscode.ThemeColor('editor.findMatchHighlightBackground'),
    border: '1px solid',
    borderColor: new vscode.ThemeColor('editor.findMatchHighlightBorder'),
  });
}

async function highlightRangeImpl(params: { path: string; version: number; start: number; end: number }) {
  validateWorkspacePath(params.path);
  const uri = resolveWorkspaceUri(params.path);
  const doc = await vscode.workspace.openTextDocument(uri);

  validateVersion(doc, params.version);
  validateRange(doc, params.start, params.end);

  // Check if file is already visible to avoid stealing focus
  let editor = vscode.window.visibleTextEditors.find(
    e => e.document.uri.toString() === uri.toString()
  );

  if (!editor) {
    // File not visible, open in preview tab
    editor = await vscode.window.showTextDocument(doc, { preview: true });
  }

  const startPos = doc.positionAt(params.start);
  const endPos = doc.positionAt(params.end);
  const range = new vscode.Range(startPos, endPos);

  const decoration = createHighlightDecoration();
  editor.setDecorations(decoration, [{ range }]);
  editor.revealRange(range, vscode.TextEditorRevealType.InCenter);

  // Auto-clear after 3 seconds and dispose decoration type
  setTimeout(() => {
    editor.setDecorations(decoration, []);
    decoration.dispose();
  }, 3000);

  return { success: true };
}

// Exported handler with timeout wrapper
async function highlightRange(params: { path: string; version: number; start: number; end: number }) {
  return withTimeout(highlightRangeImpl(params), 'highlight_range');
}
```

---

### Tool 3: `read_file`

Returns file contents (full or partial range). Returns current document version
for use in subsequent calls.

**Important**: This reads from VS Code's in-memory buffer, not disk. If the file
has unsaved changes, this returns the buffer contents. This is intentional—it
lets the agent see what the user is actually looking at.

**Input Schema**:
```json
{
  "type": "object",
  "required": ["path"],
  "properties": {
    "path": { "type": "string" },
    "version": { "type": "integer", "minimum": 0 },
    "range": {
      "type": "object",
      "required": ["start", "end"],
      "properties": {
        "start": { "type": "integer", "minimum": 0 },
        "end": { "type": "integer", "minimum": 0 }
      }
    },
    "maxBytes": {
      "type": "integer",
      "minimum": 1,
      "maximum": 200000
    }
  }
}
```

Note: `version` is optional on `read_file`. If provided, validates before
reading. If omitted, returns current content and version (useful for initial
fetch).

**Output Schema**:
```json
{
  "type": "object",
  "required": ["content", "truncated", "version"],
  "properties": {
    "content": { "type": "string" },
    "truncated": { "type": "boolean" },
    "fileSize": { "type": "integer" },
    "version": { "type": "integer" }
  }
}
```

**Implementation**:
```typescript
async function readFile(params: { path: string; version?: number; range?: { start: number; end: number }; maxBytes?: number }) {
  validateWorkspacePath(params.path);
  const uri = resolveWorkspaceUri(params.path);
  const doc = await vscode.workspace.openTextDocument(uri);

  if (params.version !== undefined && doc.version !== params.version) {
    throw new McpError(409, 'Document version mismatch', {
      expectedVersion: params.version,
      currentVersion: doc.version,
    });
  }

  const fullText = doc.getText();
  const maxBytes = params.maxBytes ?? 200000;

  let content: string;
  let truncated = false;

  if (params.range) {
    content = fullText.substring(params.range.start, params.range.end);
  } else {
    content = fullText;
  }

  if (content.length > maxBytes) {
    content = content.substring(0, maxBytes);
    truncated = true;
  }

  return { content, truncated, fileSize: fullText.length, version: doc.version };
}
```

---

### Tool 4: `get_diagnostics`

Returns compiler/linter diagnostics for a file. Returns current document version
so caller can use offsets reliably.

**Input Schema**:
```json
{
  "type": "object",
  "required": ["path"],
  "properties": {
    "path": { "type": "string" }
  }
}
```

**Output Schema**:
```json
{
  "type": "object",
  "required": ["diagnostics", "version"],
  "properties": {
    "version": { "type": "integer" },
    "diagnostics": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["message", "severity", "start", "end"],
        "properties": {
          "message": { "type": "string" },
          "severity": {
            "type": "string",
            "enum": ["error", "warning", "info", "hint"]
          },
          "start": { "type": "integer" },
          "end": { "type": "integer" }
        }
      }
    }
  }
}
```

**Implementation**:
```typescript
async function getDiagnostics(params: { path: string }) {
  validateWorkspacePath(params.path);
  const uri = resolveWorkspaceUri(params.path);
  const doc = await vscode.workspace.openTextDocument(uri);
  const diagnostics = vscode.languages.getDiagnostics(uri);

  const severityMap = ['error', 'warning', 'info', 'hint'];

  return {
    version: doc.version,
    diagnostics: diagnostics.map(d => ({
      message: d.message,
      severity: severityMap[d.severity] ?? 'info',
      start: doc.offsetAt(d.range.start),
      end: doc.offsetAt(d.range.end),
    })),
  };
}
```

---

### Tool 5: `get_definitions`

Returns symbol definition locations at a position. Requires version to ensure
the position offset is valid.

**Workspace scoping**: Definitions outside the workspace (e.g., `node_modules`,
external libraries) are filtered out. Only definitions within workspace folders
are returned.

**Input Schema**:
```json
{
  "type": "object",
  "required": ["path", "version", "position"],
  "properties": {
    "path": { "type": "string" },
    "version": { "type": "integer", "minimum": 0 },
    "position": { "type": "integer", "minimum": 0 }
  }
}
```

**Output Schema**:
```json
{
  "type": "object",
  "required": ["definitions"],
  "properties": {
    "definitions": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["path", "version", "start", "end"],
        "properties": {
          "path": { "type": "string" },
          "version": { "type": "integer" },
          "start": { "type": "integer" },
          "end": { "type": "integer" }
        }
      }
    }
  }
}
```

**Implementation**:
```typescript
async function getDefinitionsImpl(params: { path: string; version: number; position: number }) {
  validateWorkspacePath(params.path);
  const uri = resolveWorkspaceUri(params.path);
  const doc = await vscode.workspace.openTextDocument(uri);

  validateVersion(doc, params.version);

  const pos = doc.positionAt(params.position);

  const locations = await vscode.commands.executeCommand<vscode.Location[]>(
    'vscode.executeDefinitionProvider',
    uri,
    pos
  );

  if (!locations || locations.length === 0) {
    return { definitions: [] };
  }

  // Filter to workspace-only definitions
  const workspaceLocations = locations.filter(loc => isWithinWorkspace(loc.uri));

  const definitions = await Promise.all(
    workspaceLocations.map(async loc => {
      const defDoc = await vscode.workspace.openTextDocument(loc.uri);
      return {
        path: vscode.workspace.asRelativePath(loc.uri),
        version: defDoc.version,
        start: defDoc.offsetAt(loc.range.start),
        end: defDoc.offsetAt(loc.range.end),
      };
    })
  );

  return { definitions };
}

// Exported handler with timeout wrapper
async function getDefinitions(params: { path: string; version: number; position: number }) {
  return withTimeout(getDefinitionsImpl(params), 'get_definitions');
}
```

  return { definitions };
}
```

---

### Explicitly Not Included

These tools are **intentionally omitted**:

| Tool | Reason |
|------|--------|
| `apply_edit` | Crush owns mutations via its `edit` tool |
| `create_file` | Mutation |
| `rename_file` | Mutation |
| `delete_file` | Mutation |
| `write_file` | Mutation |

The MCP server cannot break a workspace. That's a feature.

---

## Implementation Plan

### Phase 1: Core Tools (MVP)

1. **Add HTTP server to extension** (~2 days)
   - Use `@modelcontextprotocol/sdk` TypeScript package
   - Bind to random port via `server.listen(0)`
   - Write discovery file on activation
   - Delete discovery file on deactivation
   - Implement `open_file` tool
   - Add `validateWorkspacePath()` helper

2. **Test with manual connection** (~0.5 days)
   - Verify discovery file written correctly
   - Manually configure MCP in crush.json using discovered port
   - Verify agent can call tool

3. **Auto-discovery in Crush** (~1 day)
   - On startup, check for `.crush/vscode-mcp.json`
   - Validate PID is alive before connecting
   - Watch file for changes (fsnotify)
   - Auto-reconnect when file changes

4. **Add remaining tools** (~1.5 days)
   - `highlight_range` (with offset-to-position conversion)
   - `read_file`
   - `get_diagnostics`
   - `get_definitions`

### Phase 2: Polish & Reliability

5. **Connection lifecycle** (~1 day)
   - Handle VS Code restart gracefully
   - Retry logic: poll for discovery file on startup
   - Status indicator in Crush TUI ("VS Code: connected")

6. **Error handling** (~0.5 days)
   - Structured JSON-RPC error responses
   - 5s timeout on all operations
   - Workspace path validation (reject paths outside root)

7. **Agent integration** (~0.5 days)
   - Strong tool descriptions (what each tool does)
   - Conditional system prompt injection (when VS Code connected)
   - "Prefer showing over telling"

---

## Dependencies

**TypeScript SDK**: `@modelcontextprotocol/sdk` (latest)

```bash
cd extensions/vscode-crush
npm install @modelcontextprotocol/sdk
```

**No new Go dependencies** - Crush already has MCP client support.

---

## Configuration

### Extension Settings

```json
{
  "crush.mcpServer.enabled": {
    "type": "boolean",
    "default": true,
    "description": "Enable MCP server for Crush to control VS Code"
  }
}
```

No port configuration needed—OS assigns a random available port.

### Crush Config (auto-discovery)

Crush automatically discovers VS Code MCP via `.crush/vscode-mcp.json`. No
manual configuration required.

If the discovery file exists and the PID is alive, Crush treats it as an
implicit MCP config:

```json
{
  "mcp": {
    "vscode": {
      "type": "http",
      "url": "http://127.0.0.1:{port}/mcp"
    }
  }
}
```

This is injected at runtime, not written to `crush.json`.

---

## Security Considerations

1. **Read-only by design**: No mutation tools. The MCP server cannot modify,
   create, or delete files. Crush owns all mutations via its `edit` tool.

2. **Localhost only**: HTTP server binds to `127.0.0.1`, never `0.0.0.0`

3. **Workspace scoping**: Tools only operate on files within the workspace root.
   Path traversal (`../`) and absolute paths are rejected with error code 403.

4. **No code execution**: Tools don't execute arbitrary code. `open_file`
   opens files, `highlight_range` decorates—no `eval` or shell access.

5. **Minimal attack surface**: 6 read-only tools. No ambient capabilities.
   The worst case is information disclosure within the workspace.

---

## Alternatives Considered

### WebSocket Instead of HTTP

MCP supports WebSocket transport. Could use it for bidirectional streaming.

**Rejected**: HTTP is simpler, stateless, easier to debug. We don't need
streaming for these tools—they're request/response.

### VS Code Extension as MCP Client

Instead of Crush calling VS Code, have VS Code call Crush.

**Rejected**: Inverts the control flow. Agent needs to command the editor, not
the other way around. Also requires Crush to expose an MCP server, which is
more complexity.

### Use VS Code Command URIs

Encode commands in URIs that VS Code handles: `vscode://file/path:line`.

**Rejected**: Limited to what URI schemes support. No way to do temporary
highlights or pass complex parameters.

---

## Open Questions

1. **Multiple VS Code windows?**
   
   If user has two VS Code windows open on different workspaces, each writes
   its own `.crush/vscode-mcp.json`. Crush in workspace A connects to VS Code
   for workspace A. This should just work, but needs testing.

2. **Highlight duration?**
   
   Current design: 3 second auto-fade. Should this be configurable? Should
   there be a "clear all highlights" tool?

---

## Success Metrics

1. Agent successfully opens files when explaining code locations
2. Highlights draw user attention to relevant code sections
3. < 100ms latency for tool calls (measured end-to-end)
4. Zero security incidents from MCP server

---

## Timeline

| Phase | Duration | Deliverable |
|-------|----------|-------------|
| Phase 1 | 5 days | 6 read-only tools with versioning, discovery file, manual testing |
| Phase 2 | 3 days | Auto-discovery, error handling, agent integration |

**Total MVP**: ~8 days of focused work.
