package server

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/mcphttp"
	_ "github.com/jackc/pgx/v5/stdlib"
)

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
	extra, deferred, cleanup := agent.ToolAugment(ctx, "worker")
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
		t.Fatalf("worker tool assembly: tool found=%t, globally callable=%t", foundTool, foundGlobal)
	}
}
