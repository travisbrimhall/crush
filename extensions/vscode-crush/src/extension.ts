import * as vscode from 'vscode';
import * as crypto from 'crypto';
import * as fs from 'fs';
import * as path from 'path';

const SCHEMA_VERSION = '1.0';

interface ServerFile {
  port: number;
  token: string;
  pid: number;
}

// VS Code is a trusted source - no token required, just identify ourselves.
const CRUSH_SOURCE_HEADER = 'X-Crush-Source';
const CRUSH_SOURCE = 'vscode';

interface DiagnosticPayload {
  schemaVersion: string;
  event: 'diagnostic_fix_request';
  source: 'vscode';
  timestamp: string;
  workspace: {
    id: string;
    root: string;
    name: string;
  };
  file: {
    path: string;
    languageId: string;
    version: number;
  };
  diagnostic: {
    range: {
      start: { line: number; character: number };
      end: { line: number; character: number };
    };
    message: string;
    severity: string;
    source?: string;
    code?: string | number;
  };
  relatedDiagnostics: Array<{
    file: string;
    range: { start: { line: number; character: number }; end: { line: number; character: number } };
    message: string;
  }>;
  context: {
    diagnosticRange: {
      start: { line: number; character: number };
      end: { line: number; character: number };
    };
    importRanges: Array<{
      start: { line: number; character: number };
      end: { line: number; character: number };
    }>;
    fileVersion: number;
  };
}

/**
 * Generates a deterministic workspace ID from the root path.
 * SHA256(absoluteRootPath).slice(0, 16)
 */
function getWorkspaceId(root: string): string {
  return crypto.createHash('sha256').update(root).digest('hex').slice(0, 16);
}

/**
 * Returns line ranges where imports are located (metadata only).
 */
function getImportRanges(
  document: vscode.TextDocument
): Array<{ start: { line: number; character: number }; end: { line: number; character: number } }> {
  const ranges: Array<{
    start: { line: number; character: number };
    end: { line: number; character: number };
  }> = [];
  const text = document.getText();

  // Match common import patterns across languages
  const importPatterns = [
    /^import\s+.*$/gm, // JS/TS/Python
    /^from\s+.*\s+import\s+.*$/gm, // Python
    /^require\s*\(.*\).*$/gm, // CommonJS
    /^use\s+.*$/gm, // Rust
    /^#include\s+.*$/gm, // C/C++
    /^package\s+.*$/gm, // Go (package declaration)
  ];

  for (const pattern of importPatterns) {
    let match;
    while ((match = pattern.exec(text)) !== null) {
      const pos = document.positionAt(match.index);
      ranges.push({
        start: { line: pos.line, character: 0 },
        end: { line: pos.line, character: match[0].length },
      });
    }
  }

  return ranges;
}

/**
 * Reads server info from .crush/server.json, walking up directories if needed.
 * Returns null if file doesn't exist or is unreadable.
 */
function readServerFile(workspaceRoot: string): ServerFile | null {
  let dir = workspaceRoot;
  const root = path.parse(dir).root;

  // Walk up directories looking for .crush/server.json
  while (dir !== root) {
    const serverPath = path.join(dir, '.crush', 'server.json');
    try {
      const content = fs.readFileSync(serverPath, 'utf-8');
      return JSON.parse(content) as ServerFile;
    } catch {
      // Try parent directory
      dir = path.dirname(dir);
    }
  }
  return null;
}

/**
 * Sends payload to Crush API with retry logic.
 */
async function sendToCrush(payload: DiagnosticPayload): Promise<boolean> {
  const workspaceRoot = payload.workspace.root;

  // Try to auto-detect from .crush/server.json first.
  const serverFile = readServerFile(workspaceRoot);

  let apiUrl: string;

  if (serverFile) {
    apiUrl = `http://localhost:${serverFile.port}`;
  } else {
    // Fall back to VS Code settings.
    const config = vscode.workspace.getConfiguration('crush');
    apiUrl = config.get<string>('apiUrl', 'http://localhost:9119');
  }

  const maxRetries = 3;
  for (let i = 0; i < maxRetries; i++) {
    try {
      const response = await fetch(`${apiUrl}/context`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          [CRUSH_SOURCE_HEADER]: CRUSH_SOURCE,
        },
        body: JSON.stringify(payload),
      });

      if (response.ok) {
        const data = (await response.json()) as { contextId: string; isNew: boolean; count: number };
        if (data.isNew) {
          vscode.window.showInformationMessage(`Sent to Crush: ${payload.diagnostic.message.slice(0, 50)}...`);
        } else {
          vscode.window.showInformationMessage(`Updated in Crush (×${data.count})`);
        }
        return true;
      }

      if (response.status === 401) {
        vscode.window.showErrorMessage('Crush rejected the request. Is the server running?');
        return false;
      }

      if (response.status === 409) {
        vscode.window.showErrorMessage('Workspace mismatch. Is Crush running in a different project?');
        return false;
      }
    } catch {
      // Retry with backoff
      await new Promise((resolve) => setTimeout(resolve, 1000 * Math.pow(2, i)));
    }
  }

  vscode.window.showErrorMessage('Crush is not running or unreachable');
  return false;
}

/**
 * Builds the payload for a diagnostic.
 */
function buildPayload(
  diagnostic: vscode.Diagnostic,
  document: vscode.TextDocument,
  workspaceFolder: vscode.WorkspaceFolder | undefined
): DiagnosticPayload {
  const workspaceRoot = workspaceFolder?.uri.fsPath || '';

  return {
    schemaVersion: SCHEMA_VERSION,
    event: 'diagnostic_fix_request',
    source: 'vscode',
    timestamp: new Date().toISOString(),
    workspace: {
      id: getWorkspaceId(workspaceRoot),
      root: workspaceRoot,
      name: workspaceFolder?.name || '',
    },
    file: {
      path: vscode.workspace.asRelativePath(document.uri),
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
      range: {
        start: { line: info.location.range.start.line, character: info.location.range.start.character },
        end: { line: info.location.range.end.line, character: info.location.range.end.character },
      },
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
}

export function activate(context: vscode.ExtensionContext) {
  // Command: Fix diagnostic with Crush
  const fixDiagnosticCmd = vscode.commands.registerCommand(
    'crush.fixDiagnostic',
    async (diagnostic: vscode.Diagnostic, uri: vscode.Uri) => {
      const document = await vscode.workspace.openTextDocument(uri);
      const workspaceFolder = vscode.workspace.getWorkspaceFolder(uri);
      const payload = buildPayload(diagnostic, document, workspaceFolder);
      await sendToCrush(payload);
    }
  );

  // Code action provider: adds "Fix with Crush" to quick fix menu
  const codeActionProvider = vscode.languages.registerCodeActionsProvider(
    { scheme: 'file' },
    {
      provideCodeActions(document, _range, context) {
        const actions: vscode.CodeAction[] = [];

        for (const diagnostic of context.diagnostics) {
          // Only show for errors and warnings
          if (
            diagnostic.severity === vscode.DiagnosticSeverity.Error ||
            diagnostic.severity === vscode.DiagnosticSeverity.Warning
          ) {
            const action = new vscode.CodeAction('Fix with Crush', vscode.CodeActionKind.QuickFix);
            action.command = {
              command: 'crush.fixDiagnostic',
              title: 'Fix with Crush',
              arguments: [diagnostic, document.uri],
            };
            action.diagnostics = [diagnostic];
            action.isPreferred = false; // Don't override built-in fixes
            actions.push(action);
          }
        }

        return actions;
      },
    },
    {
      providedCodeActionKinds: [vscode.CodeActionKind.QuickFix],
    }
  );

  // Command: Fix selection (for right-click menu)
  const fixSelectionCmd = vscode.commands.registerCommand('crush.fixSelection', async () => {
    const editor = vscode.window.activeTextEditor;
    if (!editor) return;

    const selection = editor.selection;
    const document = editor.document;
    const workspaceFolder = vscode.workspace.getWorkspaceFolder(document.uri);

    // Get diagnostics in selection range
    const diagnostics = vscode.languages.getDiagnostics(document.uri);
    const relevantDiagnostics = diagnostics.filter(
      (d) => selection.contains(d.range) || d.range.contains(selection) || d.range.intersection(selection)
    );

    if (relevantDiagnostics.length > 0) {
      // Send first diagnostic
      const payload = buildPayload(relevantDiagnostics[0], document, workspaceFolder);
      await sendToCrush(payload);
    } else {
      // Send selection as general context
      const config = vscode.workspace.getConfiguration('crush');
      const apiUrl = config.get<string>('apiUrl', 'http://localhost:9119');

      const workspaceRoot = workspaceFolder?.uri.fsPath || '';
      const payload = {
        schemaVersion: SCHEMA_VERSION,
        event: 'selection_context',
        source: 'vscode',
        timestamp: new Date().toISOString(),
        workspace: {
          id: getWorkspaceId(workspaceRoot),
          root: workspaceRoot,
          name: workspaceFolder?.name || '',
        },
        file: {
          path: vscode.workspace.asRelativePath(document.uri),
          languageId: document.languageId,
          version: document.version,
        },
        selection: {
          range: {
            start: { line: selection.start.line, character: selection.start.character },
            end: { line: selection.end.line, character: selection.end.character },
          },
          text: document.getText(selection),
        },
      };

      try {
        const response = await fetch(`${apiUrl}/context`, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            [CRUSH_SOURCE_HEADER]: CRUSH_SOURCE,
          },
          body: JSON.stringify(payload),
        });
        if (response.ok) {
          vscode.window.showInformationMessage('Selection sent to Crush');
        }
      } catch {
        vscode.window.showErrorMessage('Failed to send to Crush');
      }
    }
  });

  // Hover provider: adds "Fix with Crush" link to diagnostic hovers
  const hoverProvider = vscode.languages.registerHoverProvider(
    { scheme: 'file' },
    {
      provideHover(document, position) {
        const diagnostics = vscode.languages.getDiagnostics(document.uri);
        const diagnostic = diagnostics.find((d) => d.range.contains(position));

        if (diagnostic && (diagnostic.severity === vscode.DiagnosticSeverity.Error || diagnostic.severity === vscode.DiagnosticSeverity.Warning)) {
          const args = encodeURIComponent(JSON.stringify([diagnostic, document.uri.toString()]));
          const commandUri = vscode.Uri.parse(`command:crush.fixDiagnosticFromHover?${args}`);
          const markdown = new vscode.MarkdownString(`[✨ Fix with Crush](${commandUri})`);
          markdown.isTrusted = true;
          return new vscode.Hover(markdown);
        }
        return undefined;
      },
    }
  );

  // Command for hover link (needs URI parsing)
  const fixFromHoverCmd = vscode.commands.registerCommand(
    'crush.fixDiagnosticFromHover',
    async (diagnostic: vscode.Diagnostic, uriString: string) => {
      const uri = vscode.Uri.parse(uriString);
      const document = await vscode.workspace.openTextDocument(uri);
      const workspaceFolder = vscode.workspace.getWorkspaceFolder(uri);
      const payload = buildPayload(diagnostic, document, workspaceFolder);
      await sendToCrush(payload);
    }
  );

  // Command: Show connection status (for debugging)
  const showStatusCmd = vscode.commands.registerCommand('crush.showStatus', async () => {
    const workspaceFolders = vscode.workspace.workspaceFolders;
    if (!workspaceFolders || workspaceFolders.length === 0) {
      vscode.window.showWarningMessage('No workspace folder open');
      return;
    }

    const workspaceRoot = workspaceFolders[0].uri.fsPath;
    const serverFile = readServerFile(workspaceRoot);

    if (serverFile) {
      const message = `Crush detected!\nPort: ${serverFile.port}\nPID: ${serverFile.pid}`;
      vscode.window.showInformationMessage(message);
    } else {
      vscode.window.showWarningMessage(`Crush not detected. No .crush/server.json found.\nSearched from: ${workspaceRoot}`);
    }
  });

  context.subscriptions.push(fixDiagnosticCmd, fixSelectionCmd, fixFromHoverCmd, showStatusCmd, codeActionProvider, hoverProvider);
}

export function deactivate() {}
