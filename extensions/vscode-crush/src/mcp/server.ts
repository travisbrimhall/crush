import * as vscode from 'vscode';
import * as http from 'http';
import * as fs from 'fs';
import * as path from 'path';

const TOOL_TIMEOUT_MS = 5000;
const HIGHLIGHT_DURATION_MS = 3000;
const ANNOTATION_DEFAULT_DURATION = 0; // 0 = permanent until next annotate call

// Track active annotations for cleanup
let activeAnnotations: vscode.TextEditorDecorationType[] = [];

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
  const size = doc.getText().length;
  const sizeStr = size > 1024 ? `${(size / 1024).toFixed(1)}KB` : `${size}B`;
  return `${params.path}: ${sizeStr}, ${doc.languageId}, v${doc.version}`;
}

async function openFile(params: { path: string; preview?: boolean }) {
  validateWorkspacePath(params.path);
  const uri = resolveWorkspaceUri(params.path);
  const doc = await vscode.workspace.openTextDocument(uri);
  await vscode.window.showTextDocument(doc, { preview: params.preview ?? false });
  return `Opened ${params.path}`;
}

// Each highlight gets its own decoration type to allow stacking
function createHighlightDecoration(style?: 'green' | 'red' | 'default') {
  const colors: Record<string, { bg: string; border: string }> = {
    green: { bg: 'rgba(40, 167, 69, 0.15)', border: 'rgba(40, 167, 69, 0.4)' },
    red: { bg: 'rgba(220, 53, 69, 0.15)', border: 'rgba(220, 53, 69, 0.4)' },
    default: { bg: 'rgba(255, 213, 79, 0.15)', border: 'rgba(255, 213, 79, 0.4)' },
  };
  const c = colors[style || 'default'];
  return vscode.window.createTextEditorDecorationType({
    backgroundColor: c.bg,
    border: `2px solid ${c.border}`,
  });
}

interface HighlightParams {
  path: string;
  version: number;
  // Option 1: byte offsets
  start?: number;
  end?: number;
  // Option 2: line numbers (1-based)
  startLine?: number;
  endLine?: number;
  // Option 3: text search
  find?: string;
  style?: 'green' | 'red';
}

async function highlightRangeImpl(params: HighlightParams) {
  validateWorkspacePath(params.path);
  const uri = resolveWorkspaceUri(params.path);
  const doc = await vscode.workspace.openTextDocument(uri);

  validateVersion(doc, params.version);

  let range: vscode.Range;

  if (params.find !== undefined) {
    // Option 3: Find text and highlight it
    const text = doc.getText();
    const idx = text.indexOf(params.find);
    if (idx === -1) {
      throw new McpError(404, `Text not found: "${params.find.substring(0, 50)}..."`);
    }
    const startPos = doc.positionAt(idx);
    const endPos = doc.positionAt(idx + params.find.length);
    range = new vscode.Range(startPos, endPos);
  } else if (params.startLine !== undefined) {
    // Option 2: Line numbers (1-based)
    const startLine = params.startLine - 1; // Convert to 0-based
    const endLine = (params.endLine ?? params.startLine) - 1;
    if (startLine < 0 || endLine >= doc.lineCount) {
      throw new McpError(400, `Line out of range (file has ${doc.lineCount} lines)`);
    }
    const startPos = new vscode.Position(startLine, 0);
    const endPos = doc.lineAt(endLine).range.end;
    range = new vscode.Range(startPos, endPos);
  } else if (params.start !== undefined && params.end !== undefined) {
    // Option 1: Byte offsets
    validateRange(doc, params.start, params.end);
    const startPos = doc.positionAt(params.start);
    const endPos = doc.positionAt(params.end);
    range = new vscode.Range(startPos, endPos);
  } else {
    throw new McpError(400, 'Must provide (start, end), (startLine, endLine), or (find)');
  }

  // Check if file is already visible to avoid stealing focus
  let editor = vscode.window.visibleTextEditors.find((e) => e.document.uri.toString() === uri.toString());

  if (!editor) {
    editor = await vscode.window.showTextDocument(doc, { preview: true });
  }

  const decoration = createHighlightDecoration(params.style);
  editor.setDecorations(decoration, [{ range }]);
  editor.revealRange(range, vscode.TextEditorRevealType.InCenter);

  const startLine = range.start.line + 1;
  const endLine = range.end.line + 1;
  const lineRange = startLine === endLine ? `line ${startLine}` : `lines ${startLine}-${endLine}`;
  return `Highlighted ${lineRange} in ${params.path}`;
}

async function highlightRange(params: HighlightParams) {
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

  const lines = content.split('\n').length;
  const suffix = truncated ? ' (truncated)' : '';
  return `${params.path}: ${lines} lines, ${fullText.length} bytes, v${doc.version}${suffix}\n\n${content}`;
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

// Annotate: add temporary inline notes
interface Annotation {
  // Option 1: byte offset
  offset?: number;
  // Option 2: line number (1-based)
  line?: number;
  // Option 3: text search
  find?: string;
  text: string;
  style?: 'info' | 'warning' | 'error';
}

async function annotateImpl(params: { path: string; version: number; annotations: Annotation[]; duration?: number }) {
  validateWorkspacePath(params.path);
  const uri = resolveWorkspaceUri(params.path);
  const doc = await vscode.workspace.openTextDocument(uri);

  validateVersion(doc, params.version);

  // Clear previous annotations
  activeAnnotations.forEach((d) => d.dispose());
  activeAnnotations = [];

  let editor = vscode.window.visibleTextEditors.find((e) => e.document.uri.toString() === uri.toString());
  if (!editor) {
    editor = await vscode.window.showTextDocument(doc, { preview: true });
  }

  const styleColors: Record<string, { bg: string; fg: string }> = {
    info: { bg: 'rgba(59, 130, 246, 0.8)', fg: '#ffffff' },
    warning: { bg: 'rgba(245, 158, 11, 0.8)', fg: '#000000' },
    error: { bg: 'rgba(239, 68, 68, 0.8)', fg: '#ffffff' },
  };

  const fullText = doc.getText();
  let firstPos: vscode.Position | undefined;

  for (const ann of params.annotations) {
    let pos: vscode.Position;

    if (ann.find !== undefined) {
      // Option 3: Find text - position at end of the line containing the match
      const idx = fullText.indexOf(ann.find);
      if (idx === -1) continue;
      const foundPos = doc.positionAt(idx);
      pos = new vscode.Position(foundPos.line, doc.lineAt(foundPos.line).text.length);
    } else if (ann.line !== undefined) {
      // Option 2: Line number (1-based) - position at end of line
      const lineIdx = ann.line - 1;
      if (lineIdx < 0 || lineIdx >= doc.lineCount) continue;
      pos = new vscode.Position(lineIdx, doc.lineAt(lineIdx).text.length);
    } else if (ann.offset !== undefined) {
      // Option 1: Byte offset - position at end of line containing offset
      if (ann.offset < 0 || ann.offset > fullText.length) continue;
      const offsetPos = doc.positionAt(ann.offset);
      pos = new vscode.Position(offsetPos.line, doc.lineAt(offsetPos.line).text.length);
    } else {
      continue;
    }

    if (!firstPos) {
      firstPos = pos;
    }

    const range = new vscode.Range(pos, pos);
    const colors = styleColors[ann.style || 'info'];

    // Use 'before' with absolute positioning to appear right after line content
    // This ensures our annotation appears before GitLens blame
    const decoration = vscode.window.createTextEditorDecorationType({
      before: {
        contentText: `  ← ${ann.text}`,
        backgroundColor: colors.bg,
        color: colors.fg,
        fontStyle: 'italic',
        textDecoration: ';position:relative;',
      },
      textDecoration: 'none;position:relative;',
    });

    editor.setDecorations(decoration, [{ range }]);
    activeAnnotations.push(decoration);
  }

  // Reveal first annotation
  if (firstPos) {
    editor.revealRange(new vscode.Range(firstPos, firstPos), vscode.TextEditorRevealType.InCenter);
  }

  // Auto-clear after duration (0 = permanent)
  const duration = params.duration ?? ANNOTATION_DEFAULT_DURATION;
  if (duration > 0) {
    setTimeout(() => {
      activeAnnotations.forEach((d) => d.dispose());
      activeAnnotations = [];
    }, duration * 1000);
  }

  return `Added ${params.annotations.length} annotation(s) to ${params.path}`;
}

async function annotate(params: { path: string; version: number; annotations: Annotation[]; duration?: number }) {
  return withTimeout(annotateImpl(params), 'annotate');
}

// Split view: open two files side by side
async function splitViewImpl(params: { left: string; right: string; focus?: 'left' | 'right' }) {
  validateWorkspacePath(params.left);
  validateWorkspacePath(params.right);

  const leftUri = resolveWorkspaceUri(params.left);
  const rightUri = resolveWorkspaceUri(params.right);

  const leftDoc = await vscode.workspace.openTextDocument(leftUri);
  const rightDoc = await vscode.workspace.openTextDocument(rightUri);

  await vscode.window.showTextDocument(leftDoc, { viewColumn: vscode.ViewColumn.One, preview: false });
  await vscode.window.showTextDocument(rightDoc, { viewColumn: vscode.ViewColumn.Two, preview: false });

  // Focus the requested side
  if (params.focus === 'left') {
    await vscode.window.showTextDocument(leftDoc, { viewColumn: vscode.ViewColumn.One });
  }

  return `Split view: ${params.left} | ${params.right}`;
}

async function splitView(params: { left: string; right: string; focus?: 'left' | 'right' }) {
  return withTimeout(splitViewImpl(params), 'split_view');
}

// Show diff: compare two files or show git diff
async function showDiffImpl(params: { left?: string; right?: string; path?: string; ref?: string; title?: string }) {
  // Option A: Compare two files
  if (params.left && params.right) {
    validateWorkspacePath(params.left);
    validateWorkspacePath(params.right);

    const leftUri = resolveWorkspaceUri(params.left);
    const rightUri = resolveWorkspaceUri(params.right);
    const title = params.title || `${params.left} ↔ ${params.right}`;

    await vscode.commands.executeCommand('vscode.diff', leftUri, rightUri, title);
    return `Showing diff: ${title}`;
  }

  // Option B: Git diff for a single file
  if (params.path) {
    validateWorkspacePath(params.path);
    const uri = resolveWorkspaceUri(params.path);
    const ref = params.ref || 'HEAD';

    // Use git.openDiffFromUri or fall back to scm
    const gitUri = vscode.Uri.parse(`git:${params.path}?ref=${ref}`);

    try {
      await vscode.commands.executeCommand('vscode.diff', gitUri, uri, `${params.path} (${ref} ↔ Working)`);
      return `Showing diff for ${params.path} vs ${ref}`;
    } catch {
      // Fall back to opening the file if git diff isn't available
      await vscode.commands.executeCommand('git.openChange', uri);
      return `Showing git changes for ${params.path}`;
    }
  }

  throw new McpError(400, 'Must provide either (left, right) or (path) parameters');
}

async function showDiff(params: { left?: string; right?: string; path?: string; ref?: string; title?: string }) {
  return withTimeout(showDiffImpl(params), 'show_diff');
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
      required: ['path', 'version'],
      properties: {
        path: { type: 'string', description: 'Workspace-relative file path' },
        version: { type: 'integer', minimum: 0, description: 'Document version from get_document_metadata' },
        start: { type: 'integer', minimum: 0, description: 'Start offset (UTF-16 code units)' },
        end: { type: 'integer', minimum: 0, description: 'End offset (UTF-16 code units)' },
        startLine: { type: 'integer', minimum: 1, description: 'Start line number (1-based)' },
        endLine: { type: 'integer', minimum: 1, description: 'End line number (1-based, defaults to startLine)' },
        find: { type: 'string', description: 'Text to find and highlight' },
        style: { type: 'string', enum: ['green', 'red'], description: 'Highlight color (default: yellow)' },
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
  {
    name: 'annotate',
    description: 'Adds temporary inline annotations to code. Use this to point out multiple things at once with explanatory notes.',
    inputSchema: {
      type: 'object',
      required: ['path', 'version', 'annotations'],
      properties: {
        path: { type: 'string', description: 'Workspace-relative file path' },
        version: { type: 'integer', minimum: 0, description: 'Document version' },
        annotations: {
          type: 'array',
          items: {
            type: 'object',
            required: ['text'],
            properties: {
              offset: { type: 'integer', minimum: 0, description: 'Position offset in file' },
              line: { type: 'integer', minimum: 1, description: 'Line number (1-based)' },
              find: { type: 'string', description: 'Text to find and annotate' },
              text: { type: 'string', description: 'Annotation text to display' },
              style: { type: 'string', enum: ['info', 'warning', 'error'], description: 'Annotation style (default: info)' },
            },
          },
        },
        duration: { type: 'number', description: 'Auto-clear after N seconds (default: 10)' },
      },
    },
  },
  {
    name: 'split_view',
    description: 'Opens two files side-by-side for comparison.',
    inputSchema: {
      type: 'object',
      required: ['left', 'right'],
      properties: {
        left: { type: 'string', description: 'Left file path' },
        right: { type: 'string', description: 'Right file path' },
        focus: { type: 'string', enum: ['left', 'right'], description: 'Which side to focus (default: right)' },
      },
    },
  },
  {
    name: 'show_diff',
    description: 'Shows a diff view. Either compare two files (left/right) or show git diff for a single file (path).',
    inputSchema: {
      type: 'object',
      properties: {
        left: { type: 'string', description: 'Left file path (for file comparison)' },
        right: { type: 'string', description: 'Right file path (for file comparison)' },
        path: { type: 'string', description: 'File path (for git diff)' },
        ref: { type: 'string', description: 'Git ref to compare against (default: HEAD)' },
        title: { type: 'string', description: 'Custom title for diff tab' },
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
      return highlightRange(args as unknown as HighlightParams);
    case 'read_file':
      return readFile(args as { path: string; version?: number; range?: { start: number; end: number }; maxBytes?: number });
    case 'get_diagnostics':
      return getDiagnostics(args as { path: string });
    case 'get_definitions':
      return getDefinitions(args as { path: string; version: number; position: number });
    case 'annotate':
      return annotate(args as { path: string; version: number; annotations: Annotation[]; duration?: number });
    case 'split_view':
      return splitView(args as { left: string; right: string; focus?: 'left' | 'right' });
    case 'show_diff':
      return showDiff(args as { left?: string; right?: string; path?: string; ref?: string; title?: string });
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
        // DEBUG: Write tools info to file
        const fs = require('fs');
        const debugPath = workspaceRoot + '/.crush/mcp-debug.json';
        const debugInfo = {
          timestamp: new Date().toISOString(),
          toolCount: TOOLS.length,
          toolNames: TOOLS.map((t: { name: string }) => t.name),
          sourceFile: __filename,
          nodeVersion: process.version,
        };
        try {
          fs.writeFileSync(debugPath, JSON.stringify(debugInfo, null, 2));
        } catch (e) {
          // ignore
        }

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
        if (request.method === 'tools/call') {
          const params = request.params as { name: string };
          this.outputChannel.appendLine(`Tool: ${params?.name}`);
        }

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
