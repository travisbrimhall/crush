import * as vscode from 'vscode';
import * as http from 'http';
import * as fs from 'fs';
import * as path from 'path';

const TOOL_TIMEOUT_MS = 5000;
const HIGHLIGHT_DURATION_MS = 3000;

// Discovery file schema
interface DiscoveryFile {
  protocol: 'mcp-http';
  version: '1.0';
  host: '127.0.0.1';
  port: number;
  workspaceRoot: string;
  pid: number;
  startedAt: string;
}

// MCP error codes
class McpError extends Error {
  constructor(
    public readonly code: number,
    message: string,
    public readonly data?: Record<string, unknown>
  ) {
    super(message);
    this.name = 'McpError';
  }
}

// Wrap all tool operations with timeout
async function withTimeout<T>(promise: Promise<T>, name: string): Promise<T> {
  const timeout = new Promise<never>((_, reject) =>
    setTimeout(() => reject(new McpError(504, `Tool ${name} timed out after 5s`)), TOOL_TIMEOUT_MS)
  );
  return Promise.race([promise, timeout]);
}

// Validate path is within workspace (call BEFORE any operation)
function validateWorkspacePath(pathArg: string): void {
  if (pathArg.startsWith('/') || pathArg.startsWith('~') || pathArg.includes('..')) {
    throw new McpError(403, 'Access denied: path must be workspace-relative', { path: pathArg });
  }
  const uri = resolveWorkspaceUri(pathArg);
  const folder = vscode.workspace.getWorkspaceFolder(uri);
  if (!folder) {
    throw new McpError(403, 'Access denied: path outside workspace', { path: pathArg });
  }
}

// Resolve workspace-relative path to URI
function resolveWorkspaceUri(pathArg: string): vscode.Uri {
  const folders = vscode.workspace.workspaceFolders;
  if (!folders || folders.length === 0) {
    throw new McpError(500, 'No workspace folder open');
  }
  return vscode.Uri.joinPath(folders[0].uri, pathArg);
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

// Tool implementations
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

async function openFile(params: { path: string; preview?: boolean }) {
  validateWorkspacePath(params.path);
  const uri = resolveWorkspaceUri(params.path);
  const doc = await vscode.workspace.openTextDocument(uri);
  await vscode.window.showTextDocument(doc, { preview: params.preview ?? false });
  return { success: true, languageId: doc.languageId };
}

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
  let editor = vscode.window.visibleTextEditors.find((e) => e.document.uri.toString() === uri.toString());

  if (!editor) {
    editor = await vscode.window.showTextDocument(doc, { preview: true });
  }

  const startPos = doc.positionAt(params.start);
  const endPos = doc.positionAt(params.end);
  const range = new vscode.Range(startPos, endPos);

  const decoration = createHighlightDecoration();
  editor.setDecorations(decoration, [{ range }]);
  editor.revealRange(range, vscode.TextEditorRevealType.InCenter);

  return { success: true };
}

async function highlightRange(params: { path: string; version: number; start: number; end: number }) {
  return withTimeout(highlightRangeImpl(params), 'highlight_range');
}

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
    validateRange(doc, params.range.start, params.range.end);
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

async function getDiagnostics(params: { path: string }) {
  validateWorkspacePath(params.path);
  const uri = resolveWorkspaceUri(params.path);
  const doc = await vscode.workspace.openTextDocument(uri);
  const diagnostics = vscode.languages.getDiagnostics(uri);

  const severityMap = ['error', 'warning', 'info', 'hint'];

  return {
    version: doc.version,
    diagnostics: diagnostics.map((d) => ({
      message: d.message,
      severity: severityMap[d.severity] ?? 'info',
      start: doc.offsetAt(d.range.start),
      end: doc.offsetAt(d.range.end),
    })),
  };
}

async function getDefinitionsImpl(params: { path: string; version: number; position: number }) {
  validateWorkspacePath(params.path);
  const uri = resolveWorkspaceUri(params.path);
  const doc = await vscode.workspace.openTextDocument(uri);

  validateVersion(doc, params.version);

  const pos = doc.positionAt(params.position);

  const locations = await vscode.commands.executeCommand<vscode.Location[]>('vscode.executeDefinitionProvider', uri, pos);

  if (!locations || locations.length === 0) {
    return { definitions: [] };
  }

  // Filter to workspace-only definitions
  const workspaceLocations = locations.filter((loc) => isWithinWorkspace(loc.uri));

  const definitions = await Promise.all(
    workspaceLocations.map(async (loc) => {
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

async function getDefinitions(params: { path: string; version: number; position: number }) {
  return withTimeout(getDefinitionsImpl(params), 'get_definitions');
}

// Tool definitions for MCP
const TOOLS = [
  {
    name: 'get_document_metadata',
    description: 'Returns document version and size without content. Use before operations that require version.',
    inputSchema: {
      type: 'object',
      required: ['path'],
      properties: {
        path: { type: 'string', description: 'Workspace-relative file path' },
      },
    },
  },
  {
    name: 'open_file',
    description: 'Opens a file in VS Code editor.',
    inputSchema: {
      type: 'object',
      required: ['path'],
      properties: {
        path: { type: 'string', description: 'Workspace-relative file path' },
        preview: { type: 'boolean', description: 'Open in preview tab', default: false },
      },
    },
  },
  {
    name: 'highlight_range',
    description:
      'Highlights a code range in VS Code with a temporary visual decoration. Use this to show the user exactly where to look instead of describing line numbers. The highlight auto-clears after 3 seconds.',
    inputSchema: {
      type: 'object',
      required: ['path', 'version', 'start', 'end'],
      properties: {
        path: { type: 'string', description: 'Workspace-relative file path' },
        version: { type: 'integer', minimum: 0, description: 'Document version from get_document_metadata' },
        start: { type: 'integer', minimum: 0, description: 'Start offset (UTF-16 code units)' },
        end: { type: 'integer', minimum: 0, description: 'End offset (UTF-16 code units)' },
      },
    },
  },
  {
    name: 'read_file',
    description: "Returns file contents from VS Code's in-memory buffer (includes unsaved changes).",
    inputSchema: {
      type: 'object',
      required: ['path'],
      properties: {
        path: { type: 'string', description: 'Workspace-relative file path' },
        version: { type: 'integer', minimum: 0, description: 'Expected document version (optional)' },
        range: {
          type: 'object',
          properties: {
            start: { type: 'integer', minimum: 0 },
            end: { type: 'integer', minimum: 0 },
          },
        },
        maxBytes: { type: 'integer', minimum: 1, maximum: 200000, description: 'Max bytes to return (default 200000)' },
      },
    },
  },
  {
    name: 'get_diagnostics',
    description: 'Returns compiler/linter diagnostics for a file.',
    inputSchema: {
      type: 'object',
      required: ['path'],
      properties: {
        path: { type: 'string', description: 'Workspace-relative file path' },
      },
    },
  },
  {
    name: 'get_definitions',
    description: 'Returns symbol definition locations at a position. Only returns definitions within the workspace.',
    inputSchema: {
      type: 'object',
      required: ['path', 'version', 'position'],
      properties: {
        path: { type: 'string', description: 'Workspace-relative file path' },
        version: { type: 'integer', minimum: 0, description: 'Document version' },
        position: { type: 'integer', minimum: 0, description: 'Position offset (UTF-16 code units)' },
      },
    },
  },
];

// Tool dispatcher
async function callTool(name: string, args: Record<string, unknown>): Promise<unknown> {
  switch (name) {
    case 'get_document_metadata':
      return getDocumentMetadata(args as { path: string });
    case 'open_file':
      return openFile(args as { path: string; preview?: boolean });
    case 'highlight_range':
      return highlightRange(args as { path: string; version: number; start: number; end: number });
    case 'read_file':
      return readFile(args as { path: string; version?: number; range?: { start: number; end: number }; maxBytes?: number });
    case 'get_diagnostics':
      return getDiagnostics(args as { path: string });
    case 'get_definitions':
      return getDefinitions(args as { path: string; version: number; position: number });
    default:
      throw new McpError(400, `Unknown tool: ${name}`);
  }
}

// JSON-RPC request/response types
interface JsonRpcRequest {
  jsonrpc: '2.0';
  id: string | number;
  method: string;
  params?: Record<string, unknown>;
}

interface JsonRpcResponse {
  jsonrpc: '2.0';
  id: string | number | null;
  result?: unknown;
  error?: {
    code: number;
    message: string;
    data?: unknown;
  };
}

// Handle JSON-RPC request
async function handleRequest(req: JsonRpcRequest): Promise<JsonRpcResponse> {
  const workspaceRoot = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath || '';

  try {
    switch (req.method) {
      case 'initialize': {
        return {
          jsonrpc: '2.0',
          id: req.id,
          result: {
            protocolVersion: '2024-11-05',
            serverInfo: {
              name: 'crush-vscode-mcp',
              version: '1.0.0',
            },
            capabilities: {
              tools: {},
            },
          },
        };
      }

      case 'initialized': {
        // Notification, no response needed but we return success for simplicity
        return {
          jsonrpc: '2.0',
          id: req.id,
          result: {},
        };
      }

      case 'tools/list': {
        return {
          jsonrpc: '2.0',
          id: req.id,
          result: {
            tools: TOOLS,
            serverName: 'crush-vscode-mcp',
            protocolVersion: '1.0',
            toolSchemaVersion: '1.0',
            workspaceRoot,
          },
        };
      }

      case 'tools/call': {
        const params = req.params as { name: string; arguments?: Record<string, unknown> };
        if (!params?.name) {
          throw new McpError(400, 'Missing tool name');
        }
        const result = await callTool(params.name, params.arguments || {});
        return {
          jsonrpc: '2.0',
          id: req.id,
          result: {
            content: [{ type: 'text', text: JSON.stringify(result) }],
          },
        };
      }

      default:
        throw new McpError(-32601, `Method not found: ${req.method}`);
    }
  } catch (err) {
    if (err instanceof McpError) {
      return {
        jsonrpc: '2.0',
        id: req.id,
        error: {
          code: err.code,
          message: err.message,
          data: err.data,
        },
      };
    }
    return {
      jsonrpc: '2.0',
      id: req.id,
      error: {
        code: 500,
        message: err instanceof Error ? err.message : 'Unknown error',
      },
    };
  }
}

export class McpServer {
  private server: http.Server | null = null;
  private port: number = 0;
  private outputChannel: vscode.OutputChannel;

  constructor() {
    this.outputChannel = vscode.window.createOutputChannel('Crush MCP Server');
  }

  async start(): Promise<void> {
    if (this.server) {
      this.outputChannel.appendLine('MCP server already running');
      return;
    }

    const folders = vscode.workspace.workspaceFolders;
    if (!folders || folders.length === 0) {
      this.outputChannel.appendLine('No workspace folder open, MCP server not started');
      return;
    }

    const workspaceRoot = folders[0].uri.fsPath;

    this.server = http.createServer(async (req, res) => {
      // Only accept POST to /mcp
      if (req.method !== 'POST' || req.url !== '/mcp') {
        res.writeHead(404, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: 'Not found' }));
        return;
      }

      // Read body
      let body = '';
      for await (const chunk of req) {
        body += chunk;
      }

      try {
        const request = JSON.parse(body) as JsonRpcRequest;
        this.outputChannel.appendLine(`Request: ${request.method}`);

        const response = await handleRequest(request);
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify(response));
      } catch {
        res.writeHead(400, { 'Content-Type': 'application/json' });
        res.end(
          JSON.stringify({
            jsonrpc: '2.0',
            id: null,
            error: { code: -32700, message: 'Parse error' },
          })
        );
      }
    });

    // Bind to random available port on localhost only
    await new Promise<void>((resolve, reject) => {
      this.server!.listen(0, '127.0.0.1', () => {
        const addr = this.server!.address();
        if (addr && typeof addr === 'object') {
          this.port = addr.port;
          this.outputChannel.appendLine(`MCP server started on port ${this.port}`);
          resolve();
        } else {
          reject(new Error('Failed to get server address'));
        }
      });
      this.server!.on('error', reject);
    });

    // Write discovery file
    await this.writeDiscoveryFile(workspaceRoot);
  }

  private async writeDiscoveryFile(workspaceRoot: string): Promise<void> {
    const crushDir = path.join(workspaceRoot, '.crush');
    const discoveryPath = path.join(crushDir, 'vscode-mcp.json');

    const discovery: DiscoveryFile = {
      protocol: 'mcp-http',
      version: '1.0',
      host: '127.0.0.1',
      port: this.port,
      workspaceRoot,
      pid: process.pid,
      startedAt: new Date().toISOString(),
    };

    try {
      // Ensure .crush directory exists
      if (!fs.existsSync(crushDir)) {
        fs.mkdirSync(crushDir, { recursive: true });
      }

      fs.writeFileSync(discoveryPath, JSON.stringify(discovery, null, 2));
      this.outputChannel.appendLine(`Discovery file written: ${discoveryPath}`);
    } catch (err) {
      this.outputChannel.appendLine(`Failed to write discovery file: ${err}`);
    }
  }

  async stop(): Promise<void> {
    if (!this.server) {
      return;
    }

    // Delete discovery file
    const folders = vscode.workspace.workspaceFolders;
    if (folders && folders.length > 0) {
      const discoveryPath = path.join(folders[0].uri.fsPath, '.crush', 'vscode-mcp.json');
      try {
        if (fs.existsSync(discoveryPath)) {
          fs.unlinkSync(discoveryPath);
          this.outputChannel.appendLine(`Discovery file deleted: ${discoveryPath}`);
        }
      } catch (err) {
        this.outputChannel.appendLine(`Failed to delete discovery file: ${err}`);
      }
    }

    // Close server
    await new Promise<void>((resolve) => {
      this.server!.close(() => {
        this.outputChannel.appendLine('MCP server stopped');
        resolve();
      });
    });

    this.server = null;
    this.port = 0;
  }

  dispose(): void {
    this.stop();
    this.outputChannel.dispose();
  }
}
