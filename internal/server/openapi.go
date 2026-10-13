package server

import "strings"

// Shared route shapes are documented with concrete mutation fields, filters and
// error responses rather than a catch-all arbitrary-object request schema.
func OpenAPI() map[string]any {
	str := func() any { return map[string]any{"type": "string"} }
	props := map[string]any{}
	for _, k := range strings.Fields("name description readme externalKey lifecycle activity parentId agent detail next verification actor slug title summary body sourceProjectId sourceTaskId sourceAgent projectId taskId message recipient provider model sessionId resetsAt observedAt source") {
		props[k] = str()
	}
	props["percent"] = map[string]any{"type": "number", "minimum": 0, "maximum": 100}
	props["workingOn"] = str()
	props["expectedRevision"] = map[string]any{"type": "integer", "minimum": 1}
	props["tags"] = map[string]any{"type": "array", "items": str(), "maxItems": 32}
	props["status"] = map[string]any{"type": "string", "enum": []string{"pending", "partial", "active", "done", "blocked"}}
	for _, k := range strings.Fields("tokenBudget tokensUsed tokensRemaining contextTokens contextLimit accountRemainingPercent") {
		props[k] = map[string]any{"type": []string{"number", "null"}, "minimum": 0}
	}
	schemas := map[string]any{}
	schema := func(name, fields, required string) {
		p := map[string]any{}
		for _, k := range strings.Fields(fields) {
			p[k] = props[k]
		}
		d := map[string]any{"type": "object", "properties": p, "additionalProperties": false}
		if required != "" {
			d["required"] = strings.Fields(required)
		}
		schemas[name] = d
	}
	schema("ProjectCreate", "name description readme externalKey lifecycle activity actor", "name")
	schema("ProjectPatch", "name description readme lifecycle activity actor expectedRevision", "")
	schema("TaskCreate", "name parentId status percent agent detail next verification readme actor", "name")
	schema("TaskPatch", "name status percent agent detail next verification readme actor expectedRevision", "")
	schema("Heartbeat", "actor expectedRevision", "")
	schema("Message", "actor projectId taskId recipient message", "message")
	schema("KnowledgeCreate", "slug title summary tags body sourceProjectId sourceTaskId sourceAgent actor", "slug title body")
	schema("KnowledgePatch", "title summary tags body sourceProjectId sourceTaskId sourceAgent actor expectedRevision", "expectedRevision")
	schema("Usage", "provider model sessionId source observedAt tokenBudget tokensUsed tokensRemaining contextTokens contextLimit accountRemainingPercent resetsAt actor expectedRevision", "")
	activityFields := "agent provider model workingOn actor expectedRevision"
	schema("WorkReport", activityFields, "agent")
	report := schemas["WorkReport"].(map[string]any)
	reportProps := report["properties"].(map[string]any)
	reportProps["active"] = map[string]any{"type": "boolean", "default": true}
	assignment := map[string]any{"type": "object", "properties": map[string]any{"taskId": str(), "workingOn": str()}, "required": []string{"taskId"}, "additionalProperties": false}
	reportProps["tasks"] = map[string]any{"type": "array", "items": assignment, "maxItems": 100}
	patchProperties := map[string]any{"taskId": str()}
	for key, value := range schemas["TaskPatch"].(map[string]any)["properties"].(map[string]any) {
		patchProperties[key] = value
	}
	reportProps["taskUpdates"] = map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "object", "properties": patchProperties, "required": []string{"taskId"}, "additionalProperties": false}}
	reportProps["message"] = str()
	// MCP tool schemas are standalone, so embed the usage schema rather than a document-local reference.
	reportProps["usage"] = schemas["Usage"]
	activityProps := map[string]any{}
	for _, key := range strings.Fields("agent provider model workingOn active tasks actor expectedRevision") {
		activityProps[key] = reportProps[key]
	}
	schemas["ActivityReport"] = map[string]any{"type": "object", "properties": activityProps, "additionalProperties": false}
	paths := map[string]any{}
	add := func(path, method, summary, request string) {
		ops, ok := paths[path].(map[string]any)
		if !ok {
			ops = map[string]any{}
			paths[path] = ops
		}
		op := map[string]any{"summary": summary, "responses": map[string]any{"200": map[string]any{"description": "Success"}, "201": map[string]any{"description": "Created"}, "400": map[string]any{"description": "Invalid request"}, "404": map[string]any{"description": "Unknown entity"}, "409": map[string]any{"description": "Revision/key/cursor conflict"}, "413": map[string]any{"description": "Body exceeds 256 KiB"}}}
		params := []any{}
		for _, segment := range strings.Split(path, "/") {
			if strings.HasPrefix(segment, "{") {
				params = append(params, map[string]any{"name": strings.Trim(segment, "{}"), "in": "path", "required": true, "schema": str()})
			}
		}
		if method == "get" {
			query := ""
			switch path {
			case "/projects":
				query = "externalKey lifecycle activity q agent status"
			case "/projects/{projectId}/tasks":
				query = "agent status parentId q"
			case "/summary":
				query = "projectId agent status"
			case "/projects/{projectId}/summary":
				query = "agent status"
			case "/notifications", "/notifications/since/{number}":
				query = "since limit wait projectId taskId actor recipient kind knowledgeSlug"
			case "/knowledge":
				query = "q tag"
			}
			for _, k := range strings.Fields(query) {
				param := str()
				if k == "since" || k == "limit" || k == "wait" {
					param = map[string]any{"type": "integer", "minimum": 0}
				}
				params = append(params, map[string]any{"name": k, "in": "query", "schema": param})
			}
		}
		op["parameters"] = params
		if request != "" {
			op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/" + request}}}}
		}
		ops[method] = op
	}
	add("/projects", "get", "List projects", "")
	add("/projects", "post", "Create project", "ProjectCreate")
	add("/projects/{projectId}", "get", "Read project", "")
	add("/projects/{projectId}", "patch", "Update project; readme replacement requires expectedRevision", "ProjectPatch")
	add("/projects/{projectId}/heartbeat", "post", "Refresh activity", "Heartbeat")
	add("/projects/{projectId}/reports", "post", "Batch task updates, one-hour agent activity, optional usage and handoff message atomically", "WorkReport")
	add("/projects/{projectId}/agents", "get", "Per-project agent activity and vendor counts", "")
	add("/projects/{projectId}/agents/{agent}/activity", "post", "Report agent work or stop; omitted tasks retain previous assignments", "ActivityReport")
	add("/projects/{projectId}/tasks", "get", "List tasks", "")
	add("/projects/{projectId}/tasks", "post", "Create task/subtask", "TaskCreate")
	add("/projects/{projectId}/tasks/{taskId}", "get", "Read task", "")
	add("/projects/{projectId}/tasks/{taskId}", "patch", "Update task; readme replacement requires expectedRevision", "TaskPatch")
	add("/summary", "get", "Global summary", "")
	add("/projects/{projectId}/summary", "get", "Project summary", "")
	add("/notifications", "get", "Read numbered updates", "")
	add("/notifications", "post", "Post agent message", "Message")
	add("/notifications/since/{number}", "get", "Read updates strictly after number; continue using nextCursor", "")
	add("/knowledge", "get", "Knowledge index without bodies", "")
	add("/knowledge", "post", "Publish reusable Markdown", "KnowledgeCreate")
	add("/knowledge/{slug}", "get", "Fetch knowledge article", "")
	add("/knowledge/{slug}", "patch", "Revise knowledge", "KnowledgePatch")
	add("/knowledge/{slug}/revisions", "get", "List revision metadata", "")
	add("/knowledge/{slug}/revisions/{revision}", "get", "Fetch historical revision", "")
	add("/agents", "get", "Reported agent usage", "")
	add("/agents/{agent}/usage", "get", "Fetch usage", "")
	add("/agents/{agent}/usage", "put", "Replace usage observation", "Usage")
	return map[string]any{"openapi": "3.1.0", "info": map[string]any{"title": "Brainiac", "version": "1.0.0", "description": "Persistent progress, collaboration, usage and Markdown knowledge. See /README.md for cursor, activity and negotiation semantics."}, "servers": []any{map[string]any{"url": "/api/v1"}}, "paths": paths, "components": map[string]any{"schemas": schemas}}
}
