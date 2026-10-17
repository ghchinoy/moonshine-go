package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestValidateGeminiModel(t *testing.T) {
	allowed := []string{
		"gemini-3.5-flash-lite",
		"gemini-3.8-flash",
		"gemini-4-argon",
	}
	for _, m := range allowed {
		if err := ValidateGeminiModel(m); err != nil {
			t.Errorf("ValidateGeminiModel(%q) returned unexpected error: %v", m, err)
		}
	}

	rejected := []string{
		"gemini-2.5-flash",
		"gemini-2.5-flash-lite",
		"gemini-2.0-flash",
		"gemini-1.5-pro",
	}
	for _, m := range rejected {
		if err := ValidateGeminiModel(m); err == nil {
			t.Errorf("ValidateGeminiModel(%q) succeeded, expected rejection error", m)
		}
	}
}

func TestMCPBridgeReadOnlyAndSandboxing(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	sampleFile := filepath.Join(tmpDir, "hello.txt")
	if err := os.WriteFile(sampleFile, []byte("Line 1\nLine 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Read-only mode (allowWrite = false)
	srvRO, err := buildWorkspaceMCPServer(tmpDir, false)
	if err != nil {
		t.Fatal(err)
	}
	tServer, tClient := mcp.NewInMemoryTransports()
	srvSess, err := srvRO.Connect(ctx, tServer, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srvSess.Close()

	bridgeRO := NewMCPBridge(false, false)
	defer bridgeRO.Close()
	if err := bridgeRO.ConnectTransport(ctx, "workspace", tClient); err != nil {
		t.Fatal(err)
	}

	toolsRO := bridgeRO.ToolNames()
	for _, name := range toolsRO {
		if name == "write_note" {
			t.Fatalf("expected mutating tool 'write_note' to be filtered out in read-only mode, got %v", toolsRO)
		}
	}

	// Calling write_note directly in read-only mode must fail
	if _, _, err := bridgeRO.CallTool(ctx, "write_note", json.RawMessage(`{"path":"x.txt","content":"hi"}`)); err == nil {
		t.Fatal("expected CallTool('write_note') to be rejected in read-only mode")
	}

	// Calling read_file inside sandbox succeeds
	resBytes, _, err := bridgeRO.CallTool(ctx, "read_file", json.RawMessage(`{"path":"hello.txt"}`))
	if err != nil {
		t.Fatalf("CallTool('read_file') failed: %v", err)
	}
	if !strings.Contains(string(resBytes), "Line 1") {
		t.Errorf("expected 'Line 1' in read_file output, got %s", string(resBytes))
	}

	// Path traversal outside sandbox is blocked
	escapeBytes, _, err := bridgeRO.CallTool(ctx, "read_file", json.RawMessage(`{"path":"../outside.txt"}`))
	if err != nil {
		t.Fatalf("CallTool('read_file') unexpected protocol error: %v", err)
	}
	if !strings.Contains(string(escapeBytes), "escapes workspace root sandbox") {
		t.Errorf("expected sandbox rejection message, got %s", string(escapeBytes))
	}

	// 2. Write-enabled mode (allowWrite = true)
	srvRW, err := buildWorkspaceMCPServer(tmpDir, true)
	if err != nil {
		t.Fatal(err)
	}
	tServer2, tClient2 := mcp.NewInMemoryTransports()
	srvSess2, err := srvRW.Connect(ctx, tServer2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srvSess2.Close()

	bridgeRW := NewMCPBridge(true, false)
	defer bridgeRW.Close()
	if err := bridgeRW.ConnectTransport(ctx, "workspace-rw", tClient2); err != nil {
		t.Fatal(err)
	}

	writeRes, _, err := bridgeRW.CallTool(ctx, "write_note", json.RawMessage(`{"path":"note.txt","content":"saved via mcp"}`))
	if err != nil {
		t.Fatalf("CallTool('write_note') failed in allowWrite mode: %v", err)
	}
	if !strings.Contains(string(writeRes), "wrote 13 bytes") {
		t.Errorf("unexpected write_note output: %s", string(writeRes))
	}
}

type fakeLLMClient struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeLLMClient) GenerateWithTools(ctx context.Context, history []serveapi.Turn) (string, []serveapi.ToolCall, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++

	// If the latest turn is a user query without tool responses yet, request list_directory
	last := history[len(history)-1]
	if last.Role == "user" {
		return "", []serveapi.ToolCall{
			{
				ID:   "call-1",
				Name: "list_directory",
				Args: json.RawMessage(`{"path":"."}`),
			},
		}, nil
	}
	// After receiving tool response, synthesize spoken answer
	return "The workspace contains hello.txt.", nil, nil
}

func TestVoiceMCPAgentToolLoopAndStandby(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmpDir, "hello.txt"), []byte("hello"), 0o644)

	srv, err := buildWorkspaceMCPServer(tmpDir, false)
	if err != nil {
		t.Fatal(err)
	}
	tServer, tClient := mcp.NewInMemoryTransports()
	srvSess, err := srv.Connect(ctx, tServer, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srvSess.Close()

	bridge := NewMCPBridge(false, false)
	defer bridge.Close()
	if err := bridge.ConnectTransport(ctx, "workspace", tClient); err != nil {
		t.Fatal(err)
	}

	fakeLLM := &fakeLLMClient{}
	agent := NewVoiceMCPAgent(fakeLLM, bridge, 4, false)

	// 1. Voice query triggers MCP tool call -> tool response -> speak + display actions
	actions := agent.OnFinalizedLine(ctx, serveapi.Line{ID: 1, Text: "What files are in the workspace?", IsComplete: true})
	var foundToolDisplay, foundSpeak bool
	for _, a := range actions {
		if a.Verb == "display" && strings.Contains(string(a.Args), "list_directory") {
			foundToolDisplay = true
		}
		if a.Verb == "speak" && strings.Contains(string(a.Args), "hello.txt") {
			foundSpeak = true
		}
	}
	if !foundToolDisplay || !foundSpeak {
		t.Fatalf("expected tool display card and spoken answer, got actions: %+v", actions)
	}

	// 2. "stop listening" emits session.pause with PauseArgs.Passthrough
	pauseActions := agent.OnFinalizedLine(ctx, serveapi.Line{ID: 2, Text: "stop listening", IsComplete: true})
	var foundPause bool
	for _, a := range pauseActions {
		if a.Verb == "session.pause" {
			var pa serveapi.PauseArgs
			if err := json.Unmarshal(a.Args, &pa); err == nil && len(pa.Passthrough) == 2 {
				foundPause = true
			}
		}
	}
	if !foundPause {
		t.Fatalf("expected session.pause with passthrough wake phrases, got %+v", pauseActions)
	}

	// 3. "start listening" emits session.resume
	resumeActions := agent.OnFinalizedLine(ctx, serveapi.Line{ID: 3, Text: "start listening", IsComplete: true})
	var foundResume bool
	for _, a := range resumeActions {
		if a.Verb == "session.resume" {
			foundResume = true
		}
	}
	if !foundResume {
		t.Fatalf("expected session.resume action, got %+v", resumeActions)
	}
}
