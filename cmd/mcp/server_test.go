package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestRuntimeForIntegration() globals.Runtime {
	return globals.Runtime{
		Config: &config.Config{
			Cloud: config.Cloud{
				Provider: "aws",
				AWS: config.AWS{
					AccountID: "123456789012",
					Region:    "us-west-2",
				},
			},
			State: config.State{
				Bucket: "fabrica-state-123456789012",
				Table:  "fabrica-state-lock",
			},
		},
		ConfigPath: "fabrica.yaml",
	}
}

func setupIntegrationTest(t *testing.T) (*mcp.ClientSession, func()) {
	t.Helper()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	rt := newTestRuntimeForIntegration()
	server := NewServer(rt)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	// Start server in background.
	serverCtx, serverCancel := context.WithCancel(ctx)
	go func() {
		_ = server.Run(serverCtx, serverTransport)
	}()

	// Connect client.
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		serverCancel()
		t.Fatalf("client connect: %v", err)
	}

	cleanup := func() {
		session.Close()
		serverCancel()
		cancel()
	}
	return session, cleanup
}

func TestIntegration_Version(t *testing.T) {
	session, cleanup := setupIntegrationTest(t)
	defer cleanup()

	ctx := context.Background()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "fabrica_version",
	})
	if err != nil {
		t.Fatalf("call fabrica_version: %v", err)
	}

	if result.IsError {
		t.Fatalf("fabrica_version returned error: %v", result.Content)
	}

	// Verify structured output contains version info.
	if result.StructuredContent == nil {
		t.Fatal("expected structured output")
	}

	out := result.StructuredContent.(map[string]any)
	if out["version"] == "" {
		t.Error("expected non-empty version")
	}
	if out["go"] == "" {
		t.Error("expected non-empty go version")
	}
	if out["os"] == "" {
		t.Error("expected non-empty os")
	}
}

func TestIntegration_Doctor(t *testing.T) {
	session, cleanup := setupIntegrationTest(t)
	defer cleanup()

	ctx := context.Background()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "fabrica_doctor",
	})
	if err != nil {
		t.Fatalf("call fabrica_doctor: %v", err)
	}

	if result.IsError {
		t.Fatalf("fabrica_doctor returned error: %v", result.Content)
	}

	out := result.StructuredContent.(map[string]any)
	checks, ok := out["checks"].([]any)
	if !ok {
		t.Fatal("expected checks to be an array")
	}
	if len(checks) == 0 {
		t.Error("expected at least one doctor check")
	}
}

func TestIntegration_Status(t *testing.T) {
	session, cleanup := setupIntegrationTest(t)
	defer cleanup()

	ctx := context.Background()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "fabrica_status",
	})
	if err != nil {
		t.Fatalf("call fabrica_status: %v", err)
	}

	if result.IsError {
		t.Fatalf("fabrica_status returned error: %v", result.Content)
	}

	out := result.StructuredContent.(map[string]any)
	if _, ok := out["backend"]; !ok {
		t.Error("expected backend in status result")
	}
	if _, ok := out["modules"]; !ok {
		t.Error("expected modules in status result")
	}
	if _, ok := out["summary"]; !ok {
		t.Error("expected summary in status result")
	}
}

func TestIntegration_CostReport(t *testing.T) {
	session, cleanup := setupIntegrationTest(t)
	defer cleanup()

	ctx := context.Background()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "fabrica_cost_report",
	})
	if err != nil {
		t.Fatalf("call fabrica_cost_report: %v", err)
	}

	if result.IsError {
		t.Fatalf("fabrica_cost_report returned error: %v", result.Content)
	}

	out := result.StructuredContent.(map[string]any)
	if _, ok := out["total"]; !ok {
		t.Error("expected total in cost report")
	}
	if _, ok := out["confidence"]; !ok {
		t.Error("expected confidence in cost report")
	}
}

func TestIntegration_ConfigShow(t *testing.T) {
	session, cleanup := setupIntegrationTest(t)
	defer cleanup()

	ctx := context.Background()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "fabrica_config_show",
	})
	if err != nil {
		t.Fatalf("call fabrica_config_show: %v", err)
	}

	if result.IsError {
		t.Fatalf("fabrica_config_show returned error: %v", result.Content)
	}

	out := result.StructuredContent.(map[string]any)
	if _, ok := out["config"]; !ok {
		t.Error("expected config in result")
	}
	if _, ok := out["configPath"]; !ok {
		t.Error("expected configPath in result")
	}
}

func TestIntegration_Drift_NoProvider(t *testing.T) {
	// Drift requires a provider; without one it should return an error.
	session, cleanup := setupIntegrationTest(t)
	defer cleanup()

	ctx := context.Background()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "fabrica_drift",
	})
	if err != nil {
		t.Fatalf("call fabrica_drift: %v", err)
	}

	// Without a provider, drift should return an error result.
	if !result.IsError {
		t.Error("expected fabrica_drift to return error when no provider")
	}
}

func TestIntegration_ListTools(t *testing.T) {
	session, cleanup := setupIntegrationTest(t)
	defer cleanup()

	ctx := context.Background()
	result, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	expectedTools := map[string]bool{
		"fabrica_version":     false,
		"fabrica_doctor":      false,
		"fabrica_status":      false,
		"fabrica_drift":       false,
		"fabrica_cost_report": false,
		"fabrica_config_show": false,
	}

	for _, tool := range result.Tools {
		if _, ok := expectedTools[tool.Name]; ok {
			expectedTools[tool.Name] = true
		}
	}

	for name, found := range expectedTools {
		if !found {
			t.Errorf("tool %q not found in list", name)
		}
	}
}

func TestIntegration_UnknownTool(t *testing.T) {
	session, cleanup := setupIntegrationTest(t)
	defer cleanup()

	ctx := context.Background()
	_, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "fabrica_nonexistent",
	})
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}

func TestIntegration_ServerIdentity(t *testing.T) {
	session, cleanup := setupIntegrationTest(t)
	defer cleanup()

	initResult := session.InitializeResult()
	if initResult == nil {
		t.Fatal("expected initialize result")
	}
	if initResult.ServerInfo.Name != "fabrica" {
		t.Errorf("server name = %q, want fabrica", initResult.ServerInfo.Name)
	}
}
