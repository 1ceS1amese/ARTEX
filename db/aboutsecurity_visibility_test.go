package db

import (
	"os"
	"testing"
)

// Use only a disposable database: this test changes the planner MCP binding.
func TestAboutSecurityPlannerVisibility(t *testing.T) {
	dsn := os.Getenv("ARTEX_ABOUTSECURITY_TEST_DSN")
	if dsn == "" {
		t.Skip("set ARTEX_ABOUTSECURITY_TEST_DSN to a disposable PostgreSQL database")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	planner, err := d.GetAgentByKey("planner")
	if err != nil || planner == nil {
		t.Fatalf("planner seed: %v", err)
	}
	servers, err := d.ListMCP()
	if err != nil {
		t.Fatal(err)
	}
	var aboutID int64
	for _, server := range servers {
		if server.Name == "aboutsecurity" {
			aboutID = server.ID
			break
		}
	}
	if aboutID == 0 {
		t.Fatal("AboutSecurity MCP placeholder missing")
	}
	// Simulate an existing database with a disabled binding and no migration
	// flag. Resetting these two rows makes the test repeatable in the same
	// disposable database.
	if _, err := d.Exec(`DELETE FROM settings WHERE key='aboutsecurity_planner_visibility_v1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO agent_visibility(agent_id, resource_kind, resource_id, enabled)
VALUES ($1, 'mcp', $2, false)
ON CONFLICT (agent_id, resource_kind, resource_id, mcp_tool_name)
DO UPDATE SET enabled=false`, planner.ID, aboutID); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	migrated, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	assertVisible := func(store *DB, want bool) {
		t.Helper()
		ids, err := store.AgentVisible(planner.ID, "mcp")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, id := range ids {
			found = found || id == aboutID
		}
		if found != want {
			t.Fatalf("planner AboutSecurity visibility=%t, want %t", found, want)
		}
	}
	assertVisible(migrated, true)

	// A later UI revocation must survive another database Open/startup.
	if err := migrated.ToggleVisibility(planner.ID, "mcp", aboutID, false); err != nil {
		t.Fatal(err)
	}
	if err := migrated.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertVisible(reopened, false)
}
