package server

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"brainiac/internal/tracker"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func exerciseMCP(t *testing.T, transport mcp.Transport) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "acceptance-agent", Version: "1"}, nil)
	session, e := client.Connect(ctx, transport, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	list, e := session.ListTools(ctx, nil)
	if e != nil || len(list.Tools) != len(tools) {
		t.Fatal("tool discovery", e)
	}
	templates, e := session.ListResourceTemplates(ctx, nil)
	if e != nil || len(templates.ResourceTemplates) != 3 {
		t.Fatal("resource template discovery", e)
	}
	invoke := func(name string, args tracker.Doc) tracker.Doc {
		t.Helper()
		result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil {
			t.Fatal(e)
		}
		if result.IsError {
			t.Fatalf("%s: %v", name, result.Content)
		}
		b, e := json.Marshal(result.StructuredContent)
		if e != nil {
			t.Fatal(e)
		}
		var d tracker.Doc
		if e = json.Unmarshal(b, &d); e != nil {
			t.Fatal(e)
		}
		return d
	}
	project := invoke("create_project", tracker.Doc{"name": "MCP project", "actor": "test"})["result"].(map[string]any)
	pid := project["id"].(string)
	task := invoke("create_task", tracker.Doc{"projectId": pid, "name": "Build", "agent": "builder"})["result"].(map[string]any)
	tid := task["id"].(string)
	invoke("update_task", tracker.Doc{"projectId": pid, "taskId": tid, "percent": 40, "readme": "MCP handoff", "expectedRevision": 1})
	invoke("report_agent_usage", tracker.Doc{"agent": "builder", "tokensRemaining": 5000, "contextTokens": 2000, "contextLimit": 10000})
	invoke("report_work", tracker.Doc{"projectId": pid, "agent": "builder", "provider": "OpenAI", "workingOn": "API implementation", "taskUpdates": []any{map[string]any{"taskId": tid, "percent": 45}}, "usage": map[string]any{"tokensRemaining": 4900}})
	agents := invoke("list_project_agents", tracker.Doc{"projectId": pid})["result"].(map[string]any)
	if len(agents["agents"].([]any)) != 1 {
		t.Fatal("project activity not reported")
	}
	invoke("report_agent_activity", tracker.Doc{"projectId": pid, "agent": "builder", "active": false})
	invoke("create_knowledge", tracker.Doc{"slug": "mcp-tips", "title": "Tips", "body": "Reusable", "tags": []string{"mcp"}})
	invoke("list_notifications", tracker.Doc{"since": 0, "limit": 2})
	resource, e := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "brainiac://projects/" + pid + "/tasks/" + tid + "/README.md"})
	if e != nil || len(resource.Contents) != 1 || !strings.Contains(resource.Contents[0].Text, "MCP handoff") {
		t.Fatal("task resource", e)
	}
	resource, e = session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "brainiac://knowledge/mcp-tips/README.md"})
	if e != nil || !strings.Contains(resource.Contents[0].Text, "Reusable") {
		t.Fatal("knowledge resource", e)
	}
}
func TestMCPHTTP(t *testing.T) {
	app, s := testApp(t)
	app.EnableMCP()
	exerciseMCP(t, &mcp.StreamableClientTransport{Endpoint: s.URL + "/mcp"})
}
func TestMCPStdioBridge(t *testing.T) {
	binary := os.Getenv("BRAINIAC_TEST_BINARY")
	if binary == "" {
		t.Skip("set BRAINIAC_TEST_BINARY to exercise the compiled stdio bridge")
	}
	_, s := testApp(t)
	cmd := exec.Command(binary, "mcp-stdio", "--url", s.URL)
	cmd.Dir = t.TempDir()
	exerciseMCP(t, &mcp.CommandTransport{Command: cmd})
	files, e := os.ReadDir(cmd.Dir)
	if e != nil {
		t.Fatal(e)
	}
	if len(files) != 0 {
		t.Fatal("stdio bridge created local database/state")
	}
}
