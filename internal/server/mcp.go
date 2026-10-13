package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"brainiac/internal/tracker"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Backend interface {
	Dispatch(context.Context, string, string, url.Values, tracker.Doc) (any, error)
	Markdown(context.Context, string) (string, error)
}
type toolSpec struct{ name, method, path, description string }

var tools = []toolSpec{
	{"list_projects", "GET", "/projects", "List projects; reuse an exact externalKey or explicit ID, never a display name."},
	{"create_project", "POST", "/projects", "Create a persistent project with independent lifecycle and activity."},
	{"get_project", "GET", "/projects/{projectId}", "Read project metadata, context and revision."},
	{"update_project", "PATCH", "/projects/{projectId}", "Update project. Replacing readme requires expectedRevision."},
	{"heartbeat", "POST", "/projects/{projectId}/heartbeat", "Report actual continued work; reads do not refresh activity."},
	{"list_tasks", "GET", "/projects/{projectId}/tasks", "List tasks including subtasks, with optional agent/status filters."},
	{"create_task", "POST", "/projects/{projectId}/tasks", "Create a task or subtask using parentId. Every percentage is manually estimated."},
	{"get_task", "GET", "/projects/{projectId}/tasks/{taskId}", "Read task state, context and revision."},
	{"update_task", "PATCH", "/projects/{projectId}/tasks/{taskId}", "Update observed progress. Replacing readme requires expectedRevision."},
	{"get_summary", "GET", "/summary", "Read current global or filtered project progress and reported agent usage."},
	{"list_notifications", "GET", "/notifications", "Read numbered updates since a cursor; retain nextCursor, drain hasMore. wait supports 0..30 seconds."},
	{"post_message", "POST", "/notifications", "Share an observation or handoff. Recipient is a public routing label."},
	{"list_knowledge", "GET", "/knowledge", "Fetch the lightweight global knowledge index. Search q or exact tag before fetching bodies."},
	{"get_knowledge", "GET", "/knowledge/{slug}", "Fetch a reusable Markdown knowledge article."},
	{"create_knowledge", "POST", "/knowledge", "Publish Markdown likely to help future work, with a concise summary and tags."},
	{"update_knowledge", "PATCH", "/knowledge/{slug}", "Revise shared knowledge using expectedRevision; merge if a conflict occurs."},
	{"list_knowledge_revisions", "GET", "/knowledge/{slug}/revisions", "List metadata for historical knowledge revisions."},
	{"get_knowledge_revision", "GET", "/knowledge/{slug}/revisions/{revision}", "Read an immutable historical knowledge revision."},
	{"list_agents", "GET", "/agents", "List reported agent usage. Missing readings are unknown; stale readings remain labelled."},
	{"get_agent_usage", "GET", "/agents/{agent}/usage", "Read reported remaining tokens/context/allowance independently of progress."},
	{"report_agent_usage", "PUT", "/agents/{agent}/usage", "Replace an agent usage observation using actual readings. Omitted values become unknown; do not invent measurements."},
}

func MCPServer(backend Backend) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "brainiac", Version: "1.0.0"}, nil)
	contract := OpenAPI()
	paths := contract["paths"].(map[string]any)
	schemas := contract["components"].(map[string]any)["schemas"].(map[string]any)
	for _, spec := range tools {
		op := paths[spec.path].(map[string]any)[strings.ToLower(spec.method)].(map[string]any)
		props := map[string]any{}
		required := []string{}
		if body, ok := op["requestBody"].(map[string]any); ok {
			ref := body["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["$ref"].(string)
			schema := schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
			for k, v := range schema["properties"].(map[string]any) {
				props[k] = v
			}
			if keys, ok := schema["required"].([]string); ok {
				required = append(required, keys...)
			}
		}
		for _, param := range op["parameters"].([]any) {
			p := param.(map[string]any)
			key := p["name"].(string)
			if p["in"] == "path" {
				props[key] = map[string]any{"type": "string"}
				required = append(required, key)
			} else if spec.method == "GET" {
				typ := "string"
				if key == "since" || key == "limit" || key == "wait" {
					typ = "integer"
				}
				props[key] = map[string]any{"type": typ}
			}
		}
		tool := &mcp.Tool{Name: spec.name, Description: spec.description, InputSchema: map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}}
		mcp.AddTool(s, tool, func(ctx context.Context, req *mcp.CallToolRequest, input tracker.Doc) (*mcp.CallToolResult, tracker.Doc, error) {
			path := spec.path
			payload := tracker.Doc{}
			query := url.Values{}
			for k, v := range input {
				placeholder := "{" + k + "}"
				if strings.Contains(path, placeholder) {
					path = strings.ReplaceAll(path, placeholder, url.PathEscape(fmt.Sprint(v)))
				} else if spec.method == "GET" {
					query.Set(k, fmt.Sprint(v))
				} else {
					payload[k] = v
				}
			}
			value, err := backend.Dispatch(ctx, spec.method, "/api/v1"+path, query, payload)
			if err != nil {
				return nil, nil, err
			}
			return nil, tracker.Doc{"result": value}, nil
		})
	}
	resource := func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := req.Params.URI
		u, err := url.Parse(uri)
		if err != nil {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		var path string
		switch u.Host {
		case "guide":
			path = "/README.md"
		case "projects":
			path = "/projects" + u.Path
		case "knowledge":
			if u.Path == "/index" {
				path = "/knowledge/README.md"
			} else {
				path = "/knowledge" + u.Path
			}
		default:
			return nil, mcp.ResourceNotFoundError(uri)
		}
		text, err := backend.Markdown(ctx, path)
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "text/markdown", Text: text}}}, nil
	}
	s.AddResource(&mcp.Resource{URI: "brainiac://guide", Name: "API guide", MIMEType: "text/markdown"}, resource)
	s.AddResource(&mcp.Resource{URI: "brainiac://knowledge/index", Name: "Global knowledge index", MIMEType: "text/markdown"}, resource)
	for _, t := range []struct{ uri, name string }{{"brainiac://projects/{projectId}/README.md", "Project context"}, {"brainiac://projects/{projectId}/tasks/{taskId}/README.md", "Task context"}, {"brainiac://knowledge/{slug}/README.md", "Knowledge article"}} {
		s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: t.uri, Name: t.name, MIMEType: "text/markdown"}, resource)
	}
	return s
}
func (a *App) EnableMCP() {
	s := MCPServer(a)
	a.mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
}

// RemoteBackend connects the stdio MCP process to the shared REST service. It
// intentionally has no Store and never reads or writes a database file.
type RemoteBackend struct {
	Base   string
	Client *http.Client
}

func NewRemote(base string) (*RemoteBackend, error) {
	u, e := url.Parse(base)
	if e != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("url must be an HTTP(S) service origin")
	}
	return &RemoteBackend{strings.TrimRight(base, "/"), &http.Client{Timeout: 40 * time.Second}}, nil
}
func (b *RemoteBackend) request(ctx context.Context, method, path string, q url.Values, payload tracker.Doc) ([]byte, error) {
	var body io.Reader
	if method != "GET" {
		data, e := json.Marshal(payload)
		if e != nil {
			return nil, e
		}
		body = bytes.NewReader(data)
	}
	target := b.Base + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	r, e := http.NewRequestWithContext(ctx, method, target, body)
	if e != nil {
		return nil, e
	}
	r.Header.Set("Accept", "text/markdown, application/json")
	if method != "GET" {
		r.Header.Set("Content-Type", "application/json")
	}
	response, e := b.Client.Do(r)
	if e != nil {
		return nil, e
	}
	defer response.Body.Close()
	data, e := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024))
	if e != nil {
		return nil, e
	}
	if response.StatusCode >= 400 {
		var d struct {
			Error struct {
				Message string `json:"message"`
			}
		}
		json.Unmarshal(data, &d)
		if d.Error.Message == "" {
			d.Error.Message = http.StatusText(response.StatusCode)
		}
		return nil, &tracker.Error{Status: response.StatusCode, Message: d.Error.Message}
	}
	return data, nil
}
func (b *RemoteBackend) Dispatch(ctx context.Context, method, path string, q url.Values, payload tracker.Doc) (any, error) {
	data, e := b.request(ctx, method, path, q, payload)
	if e != nil {
		return nil, e
	}
	var out any
	e = json.Unmarshal(data, &out)
	return out, e
}
func (b *RemoteBackend) Markdown(ctx context.Context, path string) (string, error) {
	data, e := b.request(ctx, "GET", path, nil, nil)
	return string(data), e
}
