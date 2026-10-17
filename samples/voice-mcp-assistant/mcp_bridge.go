package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/genai"
)

// MCPToolCatalog wraps a discovered MCP tool and the session that owns it.
type MCPToolEntry struct {
	ServerName string
	Tool       *mcp.Tool
	Session    *mcp.ClientSession
}

// MCPBridge connects one or more MCP servers (stdio subprocesses or in-memory
// transports) to Gemini function-calling, enforcing read-only tool filtering by
// default and logging every tool invocation with measured latency.
type MCPBridge struct {
	mu         sync.RWMutex
	allowWrite bool
	debug      bool
	sessions   []*mcp.ClientSession
	tools      map[string]MCPToolEntry
	toolOrder  []string
}

// NewMCPBridge creates an MCPBridge. When allowWrite is false (the default),
// mutating tools (write/delete/create/move/commit/push/exec/run) are filtered
// out during tool discovery and rejected at call time.
func NewMCPBridge(allowWrite, debug bool) *MCPBridge {
	return &MCPBridge{
		allowWrite: allowWrite,
		debug:      debug,
		tools:      make(map[string]MCPToolEntry),
	}
}

// ConnectCommand spawns a stdio MCP server subprocess via mcp.CommandTransport
// and discovers its tools.
func (b *MCPBridge) ConnectCommand(ctx context.Context, serverName string, cmdName string, args ...string) error {
	cmd := exec.CommandContext(ctx, cmdName, args...)
	cmd.Stderr = os.Stderr
	transport := &mcp.CommandTransport{
		Command:           cmd,
		TerminateDuration: 3 * time.Second,
	}
	return b.ConnectTransport(ctx, serverName, transport)
}

// ConnectTransport connects to an MCP server over any mcp.Transport (such as
// mcp.CommandTransport for stdio subprocesses or mcp.NewInMemoryTransports for unit tests)
// and registers its allowed tools.
func (b *MCPBridge) ConnectTransport(ctx context.Context, serverName string, transport mcp.Transport) error {
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "moonshine-voice-mcp-assistant",
		Version: "0.11.1",
	}, nil)

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("connecting to MCP server %q: %w", serverName, err)
	}

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		_ = session.Close()
		return fmt.Errorf("listing tools from MCP server %q: %w", serverName, err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessions = append(b.sessions, session)

	for _, tool := range res.Tools {
		if tool == nil || tool.Name == "" {
			continue
		}
		if !b.allowWrite && isMutatingToolName(tool.Name) {
			if b.debug {
				fmt.Printf("[%s] [mcp] skipping mutating tool %q (pass -allow-write to enable)\n", ts(), tool.Name)
			}
			continue
		}
		if _, exists := b.tools[tool.Name]; !exists {
			b.toolOrder = append(b.toolOrder, tool.Name)
		}
		b.tools[tool.Name] = MCPToolEntry{
			ServerName: serverName,
			Tool:       tool,
			Session:    session,
		}
	}
	return nil
}

// Close cleanly shuts down all connected MCP client sessions.
func (b *MCPBridge) Close() error {
	b.mu.Lock()
	sessions := append([]*mcp.ClientSession(nil), b.sessions...)
	b.sessions = nil
	b.mu.Unlock()

	var firstErr error
	for _, s := range sessions {
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ToolNames returns the ordered list of registered MCP tool names.
func (b *MCPBridge) ToolNames() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]string(nil), b.toolOrder...)
}

// FunctionDeclarations converts the discovered MCP tools into Gemini FunctionDeclarations.
func (b *MCPBridge) FunctionDeclarations() []*genai.FunctionDeclaration {
	b.mu.RLock()
	defer b.mu.RUnlock()

	decls := make([]*genai.FunctionDeclaration, 0, len(b.toolOrder))
	for _, name := range b.toolOrder {
		entry := b.tools[name]
		decl := &genai.FunctionDeclaration{
			Name:        entry.Tool.Name,
			Description: entry.Tool.Description,
		}
		if entry.Tool.InputSchema != nil {
			decl.ParametersJsonSchema = sanitizeSchemaForGemini(entry.Tool.InputSchema)
		}
		decls = append(decls, decl)
	}
	return decls
}

// sanitizeSchemaForGemini strips JSON Schema draft keywords ($schema, additionalProperties)
// that Gemini's function declaration validator may reject while preserving type/properties/required.
func sanitizeSchemaForGemini(raw any) map[string]any {
	b, err := json.Marshal(raw)
	if err != nil {
		return map[string]any{"type": "object"}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{"type": "object"}
	}
	cleanSchemaMap(m)
	if _, ok := m["type"]; !ok {
		m["type"] = "object"
	}
	return m
}

func cleanSchemaMap(m map[string]any) {
	delete(m, "$schema")
	delete(m, "additionalProperties")
	if props, ok := m["properties"].(map[string]any); ok {
		for _, v := range props {
			if sub, ok := v.(map[string]any); ok {
				cleanSchemaMap(sub)
			}
		}
	}
}

// CallTool invokes a registered MCP tool by name and returns its JSON-serialized
// result and measured execution latency.
func (b *MCPBridge) CallTool(ctx context.Context, name string, argsJSON json.RawMessage) (json.RawMessage, time.Duration, error) {
	b.mu.RLock()
	entry, ok := b.tools[name]
	allowWrite := b.allowWrite
	b.mu.RUnlock()

	if !allowWrite && isMutatingToolName(name) {
		return nil, 0, fmt.Errorf("tool %q is blocked in read-only mode (pass -allow-write to enable)", name)
	}
	if !ok {
		return nil, 0, fmt.Errorf("unknown MCP tool %q", name)
	}

	var argsMap map[string]any
	if len(argsJSON) > 0 && string(argsJSON) != "null" {
		if err := json.Unmarshal(argsJSON, &argsMap); err != nil {
			return nil, 0, fmt.Errorf("unmarshaling arguments for %q: %w", name, err)
		}
	}
	if argsMap == nil {
		argsMap = map[string]any{}
	}

	t0 := time.Now()
	res, err := entry.Session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: argsMap,
	})
	elapsed := time.Since(t0)
	if err != nil {
		return nil, elapsed, fmt.Errorf("calling MCP tool %q: %w", name, err)
	}

	var textParts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && tc.Text != "" {
			textParts = append(textParts, tc.Text)
		} else if b, err := json.Marshal(c); err == nil {
			textParts = append(textParts, string(b))
		}
	}

	payload := map[string]any{
		"tool":     name,
		"server":   entry.ServerName,
		"is_error": res.IsError,
		"output":   strings.Join(textParts, "\n"),
	}
	outBytes, _ := json.Marshal(payload)
	return outBytes, elapsed, nil
}

func isMutatingToolName(name string) bool {
	lower := strings.ToLower(name)
	mutatingPrefixes := []string{
		"write_", "create_", "delete_", "remove_", "move_", "edit_",
		"patch_", "git_commit", "git_push", "git_reset", "git_checkout",
		"run_", "exec_", "shell_",
	}
	for _, p := range mutatingPrefixes {
		if strings.HasPrefix(lower, p) || strings.Contains(lower, "_"+strings.TrimSuffix(p, "_")) {
			return true
		}
	}
	return false
}

// --- Built-in Sandboxed Workspace MCP Server (Filesystem + Git) ---

type listDirInput struct {
	Path string `json:"path,omitempty" jsonschema:"Relative directory path inside the workspace root (default: '.' for root)"`
}

type readFileInput struct {
	Path     string `json:"path" jsonschema:"Relative file path inside the workspace root to read"`
	MaxLines int    `json:"max_lines,omitempty" jsonschema:"Maximum number of lines to return (default: 60)"`
}

type gitLogInput struct {
	Count int `json:"count,omitempty" jsonschema:"Number of recent commits to return (default: 5, max: 20)"`
}

type gitStatusInput struct{}

type writeNoteInput struct {
	Path    string `json:"path" jsonschema:"Relative file path inside the workspace root"`
	Content string `json:"content" jsonschema:"Text content to write"`
}

// buildWorkspaceMCPServer constructs an MCP server exposing sandboxed filesystem
// and git inspection tools rooted at rootDir.
func buildWorkspaceMCPServer(rootDir string, allowWrite bool) (*mcp.Server, error) {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolving workspace root %q: %w", rootDir, err)
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "moonshine-workspace-mcp",
		Version: "0.11.1",
	}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_directory",
		Description: "List files and subdirectories at a relative path within the workspace.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listDirInput) (*mcp.CallToolResult, any, error) {
		rel := strings.TrimSpace(in.Path)
		if rel == "" {
			rel = "."
		}
		target, err := resolveSafePath(absRoot, rel)
		if err != nil {
			return toolErrorResult(err.Error()), nil, nil
		}
		entries, err := os.ReadDir(target)
		if err != nil {
			return toolErrorResult(fmt.Sprintf("reading directory %q: %v", rel, err)), nil, nil
		}
		var names []string
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			suffix := ""
			if e.IsDir() {
				suffix = "/"
			}
			names = append(names, e.Name()+suffix)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("Entries in %s (%d): %s", rel, len(names), strings.Join(names, ", "))},
			},
		}, nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "read_file",
		Description: "Read the beginning lines of a text file at a relative path within the workspace.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in readFileInput) (*mcp.CallToolResult, any, error) {
		target, err := resolveSafePath(absRoot, in.Path)
		if err != nil {
			return toolErrorResult(err.Error()), nil, nil
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return toolErrorResult(fmt.Sprintf("reading file %q: %v", in.Path, err)), nil, nil
		}
		maxLines := in.MaxLines
		if maxLines <= 0 || maxLines > 150 {
			maxLines = 60
		}
		lines := strings.Split(string(data), "\n")
		if len(lines) > maxLines {
			lines = append(lines[:maxLines], fmt.Sprintf("... (truncated after %d lines)", maxLines))
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: strings.Join(lines, "\n")},
			},
		}, nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "git_status",
		Description: "Return the current git branch and working tree status for the workspace repository.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ gitStatusInput) (*mcp.CallToolResult, any, error) {
		cmd := exec.CommandContext(ctx, "git", "-C", absRoot, "status", "-sb")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return toolErrorResult(fmt.Sprintf("git status failed: %v (%s)", err, strings.TrimSpace(string(out)))), nil, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: strings.TrimSpace(string(out))},
			},
		}, nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "git_log",
		Description: "Return recent git commits (hash and subject) in the workspace repository.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in gitLogInput) (*mcp.CallToolResult, any, error) {
		n := in.Count
		if n <= 0 {
			n = 5
		}
		if n > 20 {
			n = 20
		}
		cmd := exec.CommandContext(ctx, "git", "-C", absRoot, "log", "--oneline", fmt.Sprintf("-%d", n))
		out, err := cmd.CombinedOutput()
		if err != nil {
			return toolErrorResult(fmt.Sprintf("git log failed: %v (%s)", err, strings.TrimSpace(string(out)))), nil, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: strings.TrimSpace(string(out))},
			},
		}, nil, nil
	})

	// Register write_note so unit tests and -allow-write mode can verify read-only gating.
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "write_note",
		Description: "Write a text note to a relative file path inside the workspace (requires -allow-write).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in writeNoteInput) (*mcp.CallToolResult, any, error) {
		if !allowWrite {
			return toolErrorResult("write_note is disabled in read-only mode"), nil, nil
		}
		target, err := resolveSafePath(absRoot, in.Path)
		if err != nil {
			return toolErrorResult(err.Error()), nil, nil
		}
		if err := os.WriteFile(target, []byte(in.Content), 0o644); err != nil {
			return toolErrorResult(fmt.Sprintf("writing %q: %v", in.Path, err)), nil, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("wrote %d bytes to %s", len(in.Content), in.Path)},
			},
		}, nil, nil
	})

	return srv, nil
}

func resolveSafePath(absRoot, relPath string) (string, error) {
	cleaned := filepath.Clean(relPath)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("absolute paths are not permitted (%q); pass a path relative to workspace root", relPath)
	}
	target := filepath.Join(absRoot, cleaned)
	relToRoot, err := filepath.Rel(absRoot, target)
	if err != nil || strings.HasPrefix(relToRoot, "..") {
		return "", fmt.Errorf("path %q escapes workspace root sandbox", relPath)
	}
	return target, nil
}

func toolErrorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
	}
}
