package mcphttp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	actool "github.com/Autumn-27/norma/tool"
)

// This test runs against an indexed context1337 process. It checks the real
// Streamable HTTP handshake and the search -> detail/file workflow through
// ARTEX's MCP client, rather than echoing a mock server's response.
func TestContext1337Compatibility(t *testing.T) {
	endpoint := os.Getenv("ABOUTSECURITY_MCP_TEST_URL")
	if endpoint == "" {
		t.Skip("set ABOUTSECURITY_MCP_TEST_URL to a running context1337 /mcp endpoint")
	}
	headers := map[string]string{}
	if auth := os.Getenv("ABOUTSECURITY_MCP_TEST_AUTHORIZATION"); auth != "" {
		headers["Authorization"] = auth
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := New(ctx, "aboutsecurity", endpoint, headers, false)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	defer client.Close()

	tools, err := client.Tools(ctx)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	want := map[string]bool{
		ToolName("aboutsecurity", "search_security"):     false,
		ToolName("aboutsecurity", "get_security_detail"): false,
		ToolName("aboutsecurity", "read_security_file"):  false,
	}
	var searchTool actool.CoreTool
	for _, tool := range tools {
		if _, ok := want[tool.Name()]; ok {
			want[tool.Name()] = true
		}
		if tool.Name() == ToolName("aboutsecurity", "search_security") {
			searchTool = tool
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("required tool %s missing from %d discovered tools", name, len(tools))
		}
	}

	var skillSearch struct {
		Items []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"items"`
	}
	// Exercise the CoreTool wrapper that the ARTEX agent actually invokes.
	input, _ := json.Marshal(map[string]any{"query": "SQL injection", "type": "skill", "limit": 10})
	result, err := searchTool.Call(ctx, input, &actool.ToolContext{MaxOutputChars: 150000})
	if err != nil {
		t.Fatalf("wrapped search_security(skill): %v", err)
	}
	if result.IsError {
		t.Fatalf("wrapped search_security(skill) returned MCP error")
	}
	search := result.Flatten()
	if err := json.Unmarshal([]byte(search), &skillSearch); err != nil {
		t.Fatalf("decode skill search: %v", err)
	}
	if len(skillSearch.Items) == 0 || !strings.HasPrefix(skillSearch.Items[0].ID, "absec://") {
		t.Fatalf("skill search returned no stable resource ID")
	}
	var detail struct {
		ID   string `json:"id"`
		Body string `json:"body"`
	}
	rawDetail, err := client.Call(ctx, "get_security_detail", map[string]any{"id": skillSearch.Items[0].ID, "depth": "summary"})
	if err != nil {
		t.Fatalf("get_security_detail: %v", err)
	}
	if err := json.Unmarshal([]byte(rawDetail), &detail); err != nil {
		t.Fatalf("decode skill detail: %v", err)
	}
	if detail.ID != skillSearch.Items[0].ID || strings.TrimSpace(detail.Body) == "" {
		t.Fatalf("skill detail missing matching ID or body")
	}

	var fileSearch struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	search, err = client.Call(ctx, "search_security", map[string]any{"type": "dict", "limit": 10})
	if err != nil {
		t.Fatalf("search_security(dict): %v", err)
	}
	if err := json.Unmarshal([]byte(search), &fileSearch); err != nil {
		t.Fatalf("decode dict search: %v", err)
	}
	if len(fileSearch.Items) == 0 || fileSearch.Items[0].ID == "" {
		t.Fatal("dictionary search returned no stable resource ID")
	}
	var file struct {
		ID            string `json:"id"`
		TotalLines    int    `json:"total_lines"`
		ReturnedLines int    `json:"returned_lines"`
	}
	rawFile, err := client.Call(ctx, "read_security_file", map[string]any{"id": fileSearch.Items[0].ID, "limit": 1})
	if err != nil {
		t.Fatalf("read_security_file: %v", err)
	}
	if err := json.Unmarshal([]byte(rawFile), &file); err != nil {
		t.Fatalf("decode dictionary file: %v", err)
	}
	if file.ID != fileSearch.Items[0].ID || file.TotalLines < 1 || file.ReturnedLines != 1 {
		t.Fatalf("dictionary file pagination or ID mismatch: total=%d returned=%d", file.TotalLines, file.ReturnedLines)
	}
}
