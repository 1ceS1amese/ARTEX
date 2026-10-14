package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/mcphttp"
	actool "github.com/Autumn-27/norma/tool"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Exercises ARTEX's native MCP visibility and tool-assembly path. The MCP
// endpoint is in-process; no machine-specific URL, knowledge base, or key is
// required. The database DSN must point to a disposable database.
func TestAboutSecurityPlannerNativeAssembly(t *testing.T) {
	dsn := os.Getenv("ARTEX_ABOUTSECURITY_TEST_DSN")
	if dsn == "" {
		t.Skip("set ARTEX_ABOUTSECURITY_TEST_DSN to a disposable PostgreSQL database")
	}
	var calls atomic.Int64
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON-RPC", http.StatusBadRequest)
			return
		}
		if req.ID == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "aboutsecurity", "version": "test"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "search_security", "description": "Search knowledge base", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}}}}
		case "tools/call":
			calls.Add(1)
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"items":[]}`}}}
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer endpoint.Close()

	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	planner, err := pg.GetAgentByKey("planner")
	if err != nil || planner == nil {
		t.Fatalf("planner seed: %v", err)
	}
	servers, err := pg.ListMCP()
	if err != nil {
		t.Fatal(err)
	}
	var mcp *db.MCPServer
	for _, server := range servers {
		if server.Name == "aboutsecurity" {
			mcp = server
			break
		}
	}
	if mcp == nil {
		t.Fatal("AboutSecurity MCP placeholder missing")
	}
	original := *mcp
	visible, err := pg.AgentVisible(planner.ID, "mcp")
	if err != nil {
		t.Fatal(err)
	}
	wasVisible := false
	for _, id := range visible {
		wasVisible = wasVisible || id == mcp.ID
	}
	defer func() {
		_, _ = pg.SaveMCP(&original)
		_ = pg.ToggleVisibility(planner.ID, "mcp", mcp.ID, wasVisible)
	}()
	mcp.URL, mcp.Enabled = endpoint.URL, true
	if _, err := pg.SaveMCP(mcp); err != nil {
		t.Fatal(err)
	}
	if err := pg.ToggleVisibility(planner.ID, "mcp", mcp.ID, true); err != nil {
		t.Fatal(err)
	}

	previousAugment := agent.ToolAugment
	defer func() { agent.ToolAugment = previousAugment }()
	wireAgentAugment(pg, filepath.Join("..", "skills"), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	extra, deferred, cleanup := agent.ToolAugment(ctx, "planner")
	if cleanup != nil {
		defer cleanup()
	}
	want := mcphttp.ToolName("aboutsecurity", "search_security")
	var search actool.CoreTool
	for _, tool := range extra {
		if tool.Name() == want {
			search = tool
		}
	}
	global := false
	for _, name := range deferred.GlobalNames {
		global = global || name == want
	}
	if search == nil || !global {
		t.Fatalf("planner native MCP assembly: tool found=%t, globally callable=%t", search != nil, global)
	}
	res, err := search.Call(ctx, json.RawMessage(`{"query":"test"}`), &actool.ToolContext{})
	if err != nil || res.IsError || !strings.Contains(res.Flatten(), "items") || calls.Load() != 1 {
		t.Fatalf("planner MCP call failed: err=%v, isError=%t, calls=%d", err, res.IsError, calls.Load())
	}
}

// Checks the persisted MCP row and ARTEX's agent assembly against a running
// context1337. The database is read-only here; setup is done separately.
func TestAboutSecurityAgentToolAssembly(t *testing.T) {
	endpoint := os.Getenv("ABOUTSECURITY_MCP_TEST_URL")
	if endpoint == "" {
		t.Skip("set ABOUTSECURITY_MCP_TEST_URL and ARTEX_CONFIG or ARTEX_PG_DSN")
	}
	dsn, _, err := db.DSN()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	pg := &db.DB{DB: sqlDB}
	servers, err := pg.ListMCP()
	if err != nil {
		t.Fatal(err)
	}
	registered := false
	for _, server := range servers {
		if server.Name == "aboutsecurity" && server.Transport == "http" && server.URL == endpoint && server.Enabled {
			registered = true
		}
	}
	if !registered {
		t.Fatal("enabled aboutsecurity MCP registration not found at test URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	previousAugment := agent.ToolAugment
	defer func() { agent.ToolAugment = previousAugment }()
	wireAgentAugment(pg, filepath.Join("..", "skills"), nil)
	extra, deferred, cleanup := agent.ToolAugment(ctx, "planner")
	if cleanup != nil {
		defer cleanup()
	}
	want := mcphttp.ToolName("aboutsecurity", "search_security")
	foundTool, foundGlobal := false, false
	for _, tool := range extra {
		if tool.Name() == want {
			foundTool = true
		}
	}
	for _, name := range deferred.GlobalNames {
		if name == want {
			foundGlobal = true
		}
	}
	if !foundTool || !foundGlobal {
		t.Fatalf("planner tool assembly: tool found=%t, globally callable=%t", foundTool, foundGlobal)
	}
}
