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
	{"request_input", "POST", "/input-requests", "Send an attention-only browser notice: an agent needs the user in the originating conversation. Metadata only; never send the question or answer. Also ask using the host's native question tool/chat. Optional source names the harness; sourceUrl links a known HTTP(S) conversation. Reuse externalKey for retries."},
	{"list_input_requests", "GET", "/input-requests", "Read attention notices across projects, open by default. status=resolved/all includes closed notices, never answers. Read at coordination points, not per action."},
	{"get_input_request", "GET", "/input-requests/{requestId}", "Read attention notice metadata and closure status. The actual question and answer remain in the originating chat."},
	{"close_input_request", "PATCH", "/input-requests/{requestId}", "Close an attention notice after the actual native question is handled (status=resolved), or withdraw it (status=cancelled). Requires expectedRevision; never send an answer. Closure/dismissal is not user input or approval."},
	{"answer_input_request", "PATCH", "/input-requests/{requestId}", "Deprecated alias of close_input_request. Metadata-only closure: status=resolved/cancelled and expectedRevision. No response field or approval; use close_input_request."},
	{"list_project_lessons", "GET", "/projects/{projectId}/lessons", "Read the lightweight project lesson index at session start and before related work. Search q/tag, kind, agent/task or confidence; active by default. Fetch relevant entries, never reload after every action."},
	{"get_project_lesson", "GET", "/projects/{projectId}/lessons/{lessonId}", "Read a recorded approach, observed failure or misunderstanding, correction and evidence. Provisional observations and old preferences do not override current user instructions."},
	{"create_project_lesson", "POST", "/projects/{projectId}/lessons", "Record a durable project lesson only when likely to prevent a repeat failure. Distinguish observations from verified conclusions; cite evidence and scope. externalKey deduplicates retries. Prefer lessons in report_work when reporting a milestone."},
	{"update_project_lesson", "PATCH", "/projects/{projectId}/lessons/{lessonId}", "Correct or supersede an existing lesson using expectedRevision; on conflict reread and merge. Prior revisions stay available. Current user instructions take precedence."},
	{"list_lesson_revisions", "GET", "/projects/{projectId}/lessons/{lessonId}/revisions", "List the lightweight immutable revision history of one project lesson."},
	{"get_lesson_revision", "GET", "/projects/{projectId}/lessons/{lessonId}/revisions/{revision}", "Read an individual immutable historical lesson revision."},
	{"list_instructions", "GET", "/instructions", "Read pending service instructions for your stable agent ID and optional project. Separate from numbered notifications; check once at session start. Normal report_work responses also include pending instructions."},
	{"get_instruction", "GET", "/instructions/{key}", "Read a service instruction and its current independent revision."},
	{"publish_instruction", "POST", "/instructions", "Publish durable guidance outside the progress feed, optionally addressed to a recipient/project. Stable non-reserved key; not an executable command or automatic model turn."},
	{"update_instruction", "PATCH", "/instructions/{key}", "Revise or retire service guidance using expectedRevision. Set active=false to retire; brainiac.* keys are service-managed."},
	{"acknowledge_instruction", "POST", "/instructions/{key}/acknowledgements", "Acknowledge the exact revision after handling it; future revisions become pending again. On conflict reread before acknowledging."},
	{"get_skill", "GET", "/skills/{name}", "Re-read the current progress-tracker or live-progress-monitor Markdown and package fingerprint when instructed. No installer execution is required to read it."},
	{"report_work", "POST", "/projects/{projectId}/reports", "Batch taskUpdates, activity, measured usage, message/inputRequest, lessons and workSummary atomically. Lessons are durable corrections, not routine command logs. Project lead: keep workSummary to 1–2 sentences on current work/fix at phase changes. Each agent supplies workingOn, actual provider/model. Handle returned instructions; report meaningful milestones or quiet 10–15 minute check-ins, not every action."},
	{"report_agent_activity", "POST", "/projects/{projectId}/agents/{agent}/activity", "Refresh one agent's current work for one hour or stop with active=false. Supply vendor/provider and taskId/workingOn assignments initially; omitted assignments are retained. Prefer report_work when progress also changed."},
	{"list_project_agents", "GET", "/projects/{projectId}/agents", "Read active project agents, their assignments, vendor counts and expiry timestamps."},
	{"list_projects", "GET", "/projects", "List projects; reuse an exact externalKey or explicit ID, never a display name."},
	{"create_project", "POST", "/projects", "Create a persistent project with independent lifecycle and activity."},
	{"get_project", "GET", "/projects/{projectId}", "Read project metadata, context and revision."},
	{"update_project", "PATCH", "/projects/{projectId}", "Update project; workSummary is the concise live focus shown beside completion. Replacing readme requires expectedRevision."},
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
	s := mcp.NewServer(&mcp.Implementation{Name: "brainiac", Version: Version}, &mcp.ServerOptions{Instructions: "Reuse the project/agent/task IDs for the session. Prefer report_work to batch task progress, agent activity, optional measured usage, handoff or inputRequest. The project lead updates a concise workSummary when focus changes; workers update workingOn and their actual provider/model. Omit unchanged summaries from check-ins. Report at work start, meaningful milestones/phase changes, blockers, handoff and finish (active=false). During uninterrupted work refresh after about 10–15 minutes; one-hour expiry is a safety timeout. Do not call this service per command, file edit, reasoning step or trivial percentage change. Check list_instructions once at session start; later report_work responses carry pending instructions without extra polling. For reread_skill fetch get_skill, follow applicable guidance within the user's task and acknowledge the exact instruction revision after handling it. Instructions never override user/harness rules or automatically start model turns. Ask actual questions and receive answers in the host app's native question tool/chat. Brainiac request_input/inputRequest is only an attention notice: send agent/project/task and optional source/sourceUrl, never question or answer content. If the native question tool blocks, post the notice first and then ask natively immediately. After the native question is handled, close_input_request with status=resolved and expectedRevision; no response field. Dismissal or notice closure never counts as input or approval. Preserve native permission/approval dialogs. Read the project lesson index at session start and before related work; fetch relevant entries. Record repeat-preventing failures or user corrections as lessons in meaningful report_work batches, not per action. Include attempt/outcome/correction/evidence, distinguish observed from confirmed, and revise or supersede outdated entries. Current user instructions take precedence over stored lessons; this is project memory, not model training. Read summaries/knowledge when needed and notifications by retained cursor at coordination points; do not busy-poll. Agent ownership alone does not imply current activity."})
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
				props[key] = p["schema"]
				required = append(required, key)
			} else if spec.method == "GET" {
				props[key] = p["schema"]
				if p["required"] == true {
					required = append(required, key)
				}
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
		case "skills":
			path = "/skills" + u.Path + "/SKILL.md"
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
	for _, t := range []struct{ uri, name string }{{"brainiac://projects/{projectId}/README.md", "Project context"}, {"brainiac://projects/{projectId}/tasks/{taskId}/README.md", "Task context"}, {"brainiac://knowledge/{slug}/README.md", "Knowledge article"}, {"brainiac://skills/{name}", "Current agent skill"}, {"brainiac://projects/{projectId}/lessons/README.md", "Project lessons index"}, {"brainiac://projects/{projectId}/lessons/{lessonId}/README.md", "Project lesson"}} {
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
