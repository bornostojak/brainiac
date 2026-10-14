package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"brainiac/internal/tracker"
	"github.com/yuin/goldmark"
)

type App struct {
	Store     *tracker.Store
	mux       *http.ServeMux
	allowlist *IPAllowlist
}

func New(s *tracker.Store) *App {
	a := &App{Store: s, mux: http.NewServeMux()}
	_ = a.ConfigureIPAllowlist(DefaultIPAllowlist)
	a.mux.HandleFunc("GET /healthz", a.health)
	a.mux.HandleFunc("/api/v1/", a.api)
	a.mux.HandleFunc("GET /events", a.events)
	a.mux.HandleFunc("GET /README.md", a.readme)
	a.mux.HandleFunc("GET /api/README.md", a.readme)
	a.mux.HandleFunc("GET /projects/{projectId}/README.md", a.readme)
	a.mux.HandleFunc("GET /projects/{projectId}/tasks/{taskId}/README.md", a.readme)
	a.mux.HandleFunc("GET /knowledge/README.md", a.readme)
	a.mux.HandleFunc("GET /knowledge/{slug}/README.md", a.readme)
	a.mux.HandleFunc("GET /skills/{name}/SKILL.md", a.readme)
	a.mux.HandleFunc("GET /api/v1/openapi.json", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, OpenAPI()) })
	return a
}
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'")
	if !a.allowlist.allowsRequest(r) {
		fail(w, &tracker.Error{Status: 403, Message: "Client IP is not allowed"})
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		origin := r.Header.Get("Origin")
		if origin != "" {
			u, e := url.Parse(origin)
			if e != nil || u.Host != r.Host || u.Scheme != "http" {
				fail(w, &tracker.Error{Status: 403, Message: "Cross-origin writes are disabled"})
				return
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			fail(w, &tracker.Error{Status: 403, Message: "Cross-site writes are disabled"})
			return
		}
	}
	a.mux.ServeHTTP(w, r)
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("response interrupted", "error", err)
	}
}
func fail(w http.ResponseWriter, err error) {
	status, message := 500, "Internal server error"
	var e *tracker.Error
	if errors.As(err, &e) {
		status, message = e.Status, e.Message
	} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		status, message = 408, "Request cancelled or timed out"
	} else {
		slog.Error("request failed", "error", err)
	}
	reply(w, status, tracker.Doc{"error": tracker.Doc{"status": status, "message": message}})
}
func (a *App) health(w http.ResponseWriter, r *http.Request) {
	if err := a.Store.Ping(r.Context()); err != nil {
		reply(w, 503, tracker.Doc{"service": "brainiac", "status": "unavailable"})
		return
	}
	reply(w, 200, tracker.Doc{"service": "brainiac", "status": "ok", "apiVersion": "v1", "version": Version, "mcp": "/mcp", "capabilities": []string{"projects", "nested-tasks", "notifications", "knowledge", "usage", "agent-activity", "input-requests", "instructions"}})
}
func (a *App) api(w http.ResponseWriter, r *http.Request) {
	payload := tracker.Doc{}
	if r.Method != "GET" {
		media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || media != "application/json" {
			fail(w, &tracker.Error{Status: 415, Message: "Content-Type must be application/json"})
			return
		}
		body := http.MaxBytesReader(w, r.Body, 256*1024)
		decoder := json.NewDecoder(body)
		if e = decoder.Decode(&payload); e != nil {
			var tooBig *http.MaxBytesError
			if errors.As(e, &tooBig) {
				fail(w, &tracker.Error{Status: 413, Message: "Request exceeds 256 KiB"})
			} else {
				fail(w, &tracker.Error{Status: 400, Message: "Expected one JSON object"})
			}
			return
		}
		if payload == nil {
			fail(w, &tracker.Error{Status: 400, Message: "Expected a JSON object"})
			return
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			fail(w, &tracker.Error{Status: 400, Message: "Expected one JSON object"})
			return
		}
	}
	value, err := a.Dispatch(r.Context(), r.Method, r.URL.EscapedPath(), r.URL.Query(), payload)
	if err != nil {
		fail(w, err)
		return
	}
	status := 200
	if r.Method == "POST" && !strings.HasSuffix(r.URL.Path, "/heartbeat") {
		status = 201
	}
	reply(w, status, value)
}
func integer(q url.Values, key string, def int64) (int64, error) {
	v := q.Get(key)
	if v == "" {
		return def, nil
	}
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil || n < 0 {
		return 0, &tracker.Error{Status: 400, Message: key + " must be a nonnegative integer"}
	}
	return n, nil
}
func pathParts(path string) ([]string, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i, p := range parts {
		v, e := url.PathUnescape(p)
		if e != nil || strings.Contains(v, "/") {
			return nil, &tracker.Error{Status: 400, Message: "Invalid path segment"}
		}
		parts[i] = v
	}
	return parts, nil
}
func (a *App) Dispatch(ctx context.Context, method, path string, q url.Values, payload tracker.Doc) (any, error) {
	parts, err := pathParts(path)
	if err != nil {
		return nil, err
	}
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "v1" {
		return nil, &tracker.Error{Status: 404, Message: "Unknown API endpoint"}
	}
	p := parts[2:]
	s := a.Store
	switch p[0] {
	case "input-requests":
		if len(p) == 1 {
			if method == "POST" {
				return s.RequestInput(ctx, payload)
			}
			if method == "GET" {
				status := q.Get("status")
				if status == "" {
					status = "open"
				}
				if status == "all" {
					status = ""
				} else if status != "open" && status != "resolved" && status != "cancelled" {
					return nil, &tracker.Error{Status: 400, Message: "status must be open, resolved, cancelled or all"}
				}
				items, err := s.List(ctx, "input")
				return tracker.Doc{"requests": tracker.Filter(items, map[string]string{"status": status, "projectId": q.Get("projectId"), "agent": q.Get("agent")})}, err
			}
		}
		if len(p) == 2 {
			if method == "GET" {
				return s.Get(ctx, "input", p[1])
			}
			if method == "PATCH" {
				return s.AnswerInput(ctx, p[1], payload)
			}
		}
	case "instructions":
		if len(p) == 1 {
			if method == "GET" {
				items, err := s.Instructions(ctx, q.Get("agent"), q.Get("projectId"))
				return tracker.Doc{"instructions": items}, err
			}
			if method == "POST" {
				return s.SaveInstruction(ctx, "", payload)
			}
		}
		if len(p) == 2 {
			if method == "GET" {
				return s.Get(ctx, "instruction", p[1])
			}
			if method == "PATCH" {
				return s.SaveInstruction(ctx, p[1], payload)
			}
		}
		if len(p) == 3 && p[2] == "acknowledgements" && method == "POST" {
			return s.AcknowledgeInstruction(ctx, p[1], payload)
		}
	case "skills":
		if len(p) == 2 && method == "GET" {
			return skillDocument(p[1])
		}
	case "projects":
		if len(p) == 1 {
			if method == "POST" {
				return s.CreateProject(ctx, payload)
			}
			if method == "GET" {
				summary, e := s.Summary(ctx, "", q.Get("agent"), q.Get("status"))
				if e != nil {
					return nil, e
				}
				projects := tracker.Filter(summary["projects"].([]tracker.Doc), map[string]string{"externalKey": q.Get("externalKey"), "lifecycle": q.Get("lifecycle"), "activity": q.Get("activity")})
				return tracker.Doc{"projects": search(projects, q.Get("q"), "name description")}, nil
			}
		}
		if len(p) >= 2 {
			pid := p[1]
			if len(p) == 3 && p[2] == "reports" && method == "POST" {
				return s.ReportWork(ctx, pid, payload)
			}
			if len(p) == 3 && p[2] == "agents" && method == "GET" {
				return s.ProjectAgents(ctx, pid)
			}
			if len(p) == 5 && p[2] == "agents" && p[4] == "activity" && method == "POST" {
				input := tracker.Doc{}
				for key, value := range payload {
					input[key] = value
				}
				if value, ok := input["agent"]; ok && value != p[3] {
					return nil, &tracker.Error{Status: 400, Message: "agent does not match the URL"}
				}
				input["agent"] = p[3]
				return s.ReportWork(ctx, pid, input)
			}
			if len(p) == 2 {
				if method == "GET" {
					return s.Get(ctx, "project", pid)
				}
				if method == "PATCH" {
					return s.UpdateProject(ctx, pid, payload)
				}
			}
			if len(p) == 3 && p[2] == "heartbeat" && method == "POST" {
				return s.Heartbeat(ctx, pid, payload)
			}
			if len(p) == 3 && p[2] == "summary" && method == "GET" {
				return s.Summary(ctx, pid, q.Get("agent"), q.Get("status"))
			}
			if len(p) >= 3 && p[2] == "tasks" {
				if len(p) == 3 {
					if method == "POST" {
						return s.SaveTask(ctx, pid, "", payload)
					}
					if method == "GET" {
						if _, e := s.Get(ctx, "project", pid); e != nil {
							return nil, e
						}
						tasks, e := s.List(ctx, "task")
						if e != nil {
							return nil, e
						}
						return tracker.Doc{"tasks": search(tracker.Filter(tasks, map[string]string{"projectId": pid, "agent": q.Get("agent"), "status": q.Get("status"), "parentId": q.Get("parentId")}), q.Get("q"), "name detail next")}, nil
					}
				}
				if len(p) == 4 {
					if method == "GET" {
						return s.Task(ctx, pid, p[3])
					}
					if method == "PATCH" {
						return s.SaveTask(ctx, pid, p[3], payload)
					}
				}
			}
		}
	case "summary":
		if len(p) == 1 && method == "GET" {
			return s.Summary(ctx, q.Get("projectId"), q.Get("agent"), q.Get("status"))
		}
	case "notifications":
		if len(p) == 1 && method == "POST" {
			return s.Message(ctx, payload)
		}
		if method == "GET" && (len(p) == 1 || len(p) == 3 && p[1] == "since") {
			since, e := integer(q, "since", 0)
			if e != nil {
				return nil, e
			}
			if len(p) == 3 {
				since, e = strconv.ParseInt(p[2], 10, 64)
				if e != nil || since < 0 {
					return nil, &tracker.Error{Status: 400, Message: "Invalid cursor"}
				}
			}
			limit, e := integer(q, "limit", 100)
			if e != nil {
				return nil, e
			}
			wait, e := integer(q, "wait", 0)
			if e != nil {
				return nil, e
			}
			if wait > 30 {
				return nil, &tracker.Error{Status: 400, Message: "wait must be 0..30"}
			}
			return s.WaitFeed(ctx, tracker.FeedOptions{Since: since, Limit: int(limit), ProjectID: q.Get("projectId"), TaskID: q.Get("taskId"), Actor: q.Get("actor"), Recipient: q.Get("recipient"), Kind: q.Get("kind"), KnowledgeSlug: q.Get("knowledgeSlug")}, time.Duration(wait)*time.Second)
		}
	case "knowledge":
		if len(p) == 1 {
			if method == "POST" {
				return s.SaveKnowledge(ctx, "", payload)
			}
			if method == "GET" {
				items, e := s.List(ctx, "knowledge")
				if e != nil {
					return nil, e
				}
				items = search(items, q.Get("q"), "title summary tags")
				out := []tracker.Doc{}
				for _, d := range items {
					if tag := q.Get("tag"); tag != "" {
						found := false
						for _, v := range d["tags"].([]any) {
							if v == tag {
								found = true
							}
						}
						if !found {
							continue
						}
					}
					delete(d, "body")
					out = append(out, d)
				}
				return tracker.Doc{"knowledge": out}, nil
			}
		}
		if len(p) == 2 {
			if method == "GET" {
				return s.Get(ctx, "knowledge", p[1])
			}
			if method == "PATCH" {
				return s.SaveKnowledge(ctx, p[1], payload)
			}
		}
		if method == "GET" && len(p) >= 3 && p[2] == "revisions" {
			if len(p) == 3 {
				return s.KnowledgeHistory(ctx, p[1], 0)
			}
			if len(p) == 4 {
				n, e := strconv.ParseInt(p[3], 10, 64)
				if e != nil || n < 1 {
					return nil, &tracker.Error{Status: 400, Message: "Invalid revision"}
				}
				return s.KnowledgeHistory(ctx, p[1], n)
			}
		}
	case "agents":
		if len(p) == 1 && method == "GET" {
			items, e := s.List(ctx, "agent")
			return tracker.Doc{"agents": items}, e
		}
		if len(p) == 3 && p[2] == "usage" {
			if method == "GET" {
				return s.Get(ctx, "agent", p[1])
			}
			if method == "PUT" {
				return s.SaveUsage(ctx, p[1], payload)
			}
		}
	}
	if knownPath(p) {
		return nil, &tracker.Error{Status: 405, Message: "Method not supported for this endpoint"}
	}
	return nil, &tracker.Error{Status: 404, Message: "Unknown API endpoint"}
}
func knownPath(p []string) bool {
	if len(p) == 1 {
		switch p[0] {
		case "projects", "knowledge", "agents", "notifications", "summary", "input-requests", "instructions":
			return true
		}
	}
	if p[0] == "input-requests" || p[0] == "skills" {
		return len(p) == 2
	}
	if p[0] == "instructions" {
		return len(p) == 2 || len(p) == 3 && p[2] == "acknowledgements"
	}
	if p[0] == "projects" {
		return len(p) == 2 || len(p) == 3 && (p[2] == "tasks" || p[2] == "summary" || p[2] == "heartbeat" || p[2] == "agents" || p[2] == "reports") || len(p) == 4 && p[2] == "tasks" || len(p) == 5 && p[2] == "agents" && p[4] == "activity"
	}
	if p[0] == "knowledge" {
		return len(p) == 2 || len(p) == 3 && p[2] == "revisions" || len(p) == 4 && p[2] == "revisions"
	}
	return p[0] == "agents" && len(p) == 3 && p[2] == "usage"
}
func search(items []tracker.Doc, q, keys string) []tracker.Doc {
	if q == "" {
		return items
	}
	q = strings.ToLower(q)
	out := []tracker.Doc{}
	for _, d := range items {
		var hay []string
		for _, key := range strings.Fields(keys) {
			hay = append(hay, fmt.Sprint(d[key]))
		}
		if strings.Contains(strings.ToLower(strings.Join(hay, " ")), q) {
			out = append(out, d)
		}
	}
	return out
}

func (a *App) events(w http.ResponseWriter, r *http.Request) {
	since, err := integer(r.URL.Query(), "since", 0)
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		since, err = strconv.ParseInt(last, 10, 64)
	}
	if err != nil || since < 0 {
		fail(w, &tracker.Error{Status: 400, Message: "Invalid SSE cursor"})
		return
	}
	o := tracker.FeedOptions{Since: since, Limit: 100, ProjectID: r.URL.Query().Get("projectId")}
	first, err := a.Store.Feed(r.Context(), o)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Cache-Control", "no-cache")
	controller := http.NewResponseController(w)
	send := func(f tracker.Feed) error {
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		for _, n := range f.Notifications {
			b, _ := json.Marshal(n)
			if _, err := fmt.Fprintf(w, "id: %v\nevent: update\ndata: %s\n\n", n["number"], b); err != nil {
				return err
			}
		} // Persist scanned cursor even when project filtering skips unrelated updates.
		if _, err := fmt.Fprintf(w, "id: %d\nevent: cursor\ndata: %d\n\n", f.NextCursor, f.NextCursor); err != nil {
			return err
		}
		return controller.Flush()
	}
	if err = send(first); err != nil {
		return
	}
	o.Since = first.NextCursor
	for {
		wait := 15 * time.Second
		if first.HasMore {
			wait = 0
		}
		f, err := a.Store.WaitFeed(r.Context(), o, wait)
		if err != nil {
			return
		}
		if err = send(f); err != nil {
			return
		}
		o.Since = f.NextCursor
		first = f
	}
}

func wantsHTML(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	bestQ := -1.0
	bestSpecific := -1
	html := false
	explicit := false
	for _, item := range strings.Split(accept, ",") {
		media, params, err := mime.ParseMediaType(strings.TrimSpace(item))
		if err != nil {
			continue
		}
		quality := 1.0
		if v := params["q"]; v != "" {
			quality, err = strconv.ParseFloat(v, 64)
			if err != nil || quality < 0 || quality > 1 {
				continue
			}
		}
		if media == "text/html" || media == "text/markdown" || media == "text/plain" || media == "application/octet-stream" {
			explicit = true
			if quality > bestQ || quality == bestQ && media != "text/html" {
				bestQ = quality
				bestSpecific = 1
				html = media == "text/html"
			}
		}
	}
	if explicit && bestSpecific == 1 {
		return html && bestQ > 0
	}
	ua := r.Header.Get("User-Agent")
	return strings.Contains(ua, "Mozilla/")
}
func (a *App) readme(w http.ResponseWriter, r *http.Request) {
	md, err := a.Markdown(r.Context(), r.URL.Path)
	if err != nil {
		fail(w, err)
		return
	}
	if !wantsHTML(r) {
		content := "text/markdown; charset=utf-8"
		if strings.Contains(r.Header.Get("Accept"), "text/plain") {
			content = "text/plain; charset=utf-8"
		}
		w.Header().Set("Content-Type", content)
		w.Header().Set("Vary", "Accept, User-Agent")
		io.WriteString(w, md)
		return
	}
	var rendered bytes.Buffer
	if err = goldmark.Convert([]byte(md), &rendered); err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Vary", "Accept, User-Agent")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	readmeTemplate.Execute(w, template.HTML(rendered.String()))
}

var readmeTemplate = template.Must(template.New("readme").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Brainiac · README</title><link rel="stylesheet" href="/assets/style.css"><script src="/assets/alerts.js" defer></script></head><body><header><a href="/">brainiac / progress</a><nav><a href="/knowledge">Knowledge</a><a href="/README.md">API guide</a></nav></header><article class="panel markdown">{{.}}</article></body></html>`))

func (a *App) Markdown(ctx context.Context, path string) (string, error) {
	p := strings.Split(strings.Trim(path, "/"), "/")
	if len(p) == 3 && p[0] == "skills" && p[2] == "SKILL.md" {
		d, err := skillDocument(p[1])
		return tracker.Text(d, "markdown"), err
	}
	if path == "/README.md" || path == "/api/README.md" {
		return apiGuide, nil
	}
	if len(p) == 2 && p[0] == "knowledge" && p[1] == "README.md" {
		index, err := a.Dispatch(ctx, "GET", "/api/v1/knowledge", url.Values{}, nil)
		if err != nil {
			return "", err
		}
		md := "# Global knowledge\n\nReusable Markdown maintained by agents. Fetch an article only when relevant.\n\n"
		for _, d := range index.(tracker.Doc)["knowledge"].([]tracker.Doc) {
			md += fmt.Sprintf("- [%s](/knowledge/%s/README.md) — %s (revision %g)\n", tracker.Text(d, "title"), tracker.Text(d, "slug"), tracker.Text(d, "summary"), tracker.Num(d, "revision"))
		}
		return md, nil
	}
	if len(p) == 3 && p[0] == "knowledge" {
		d, err := a.Store.Get(ctx, "knowledge", p[1])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("# %s\n\n%s\n\nSlug: `%s` · Revision: %g · Updated: %s\n\n%s\n", tracker.Text(d, "title"), tracker.Text(d, "summary"), tracker.Text(d, "slug"), tracker.Num(d, "revision"), tracker.Text(d, "updatedAt"), tracker.Text(d, "body")), nil
	}
	if len(p) >= 3 && p[0] == "projects" {
		pid := p[1]
		project, err := a.Store.Get(ctx, "project", pid)
		if err != nil {
			return "", err
		}
		d := project
		title := tracker.Text(d, "name")
		endpoint := "/api/v1/projects/" + pid
		if len(p) == 5 && p[2] == "tasks" {
			d, err = a.Store.Task(ctx, pid, p[3])
			if err != nil {
				return "", err
			}
			title = tracker.Text(d, "name")
			endpoint += "/tasks/" + p[3]
		}
		md := fmt.Sprintf("# %s\n\nProject: %s (`%s`)\n\nRevision: %g · Updated: %s\n\n", title, tracker.Text(project, "name"), pid, tracker.Num(d, "revision"), tracker.Text(d, "updatedAt"))
		if d["status"] != nil {
			md += fmt.Sprintf("Status: %s · Progress: %g%% · Agent: %s\n\nCurrent state: %s\n\nNext: %s\n\nVerification: %s\n\n", tracker.Text(d, "status"), tracker.Num(d, "percent"), tracker.Text(d, "agent"), tracker.Text(d, "detail"), tracker.Text(d, "next"), tracker.Text(d, "verification"))
			if owner := tracker.Text(d, "agent"); owner != "" {
				usage, e := a.Store.Get(ctx, "agent", owner)
				if e == nil {
					b, _ := json.Marshal(usage)
					md += "Agent usage (reported, independent of task completion):\n\n```json\n" + string(b) + "\n```\n\n"
				}
			}
		} else {
			md += fmt.Sprintf("Lifecycle: %s · Activity: %s · Last activity: %s\n\n%s\n\n", tracker.Text(project, "lifecycle"), tracker.Text(project, "activity"), tracker.Text(project, "lastActivityAt"), tracker.Text(project, "description"))
			if focus := tracker.Text(project, "workSummary"); focus != "" {
				md += "Current reported work: " + focus + "\n\n"
			}
			tasks, err := a.Store.List(ctx, "task")
			if err != nil {
				return "", err
			}
			md += "## Tasks\n\n"
			for _, t := range tracker.Filter(tasks, map[string]string{"projectId": pid}) {
				md += fmt.Sprintf("- [%s](/projects/%s/tasks/%s/README.md): %s, %g%% · %s\n", tracker.Text(t, "name"), pid, tracker.Text(t, "id"), tracker.Text(t, "status"), tracker.Num(t, "percent"), tracker.Text(t, "agent"))
			}
			md += "\n"
		}
		md += "## Context\n\n" + tracker.Text(d, "readme") + "\n\n## API\n\n`" + endpoint + "`\n\n[API guide](/README.md) · [Global knowledge](/knowledge/README.md)\n\n## Recent updates\n\n"
		feed, err := a.Store.Feed(ctx, tracker.FeedOptions{Limit: 500, ProjectID: pid, TaskID: func() string {
			if len(p) == 5 {
				return p[3]
			}
			return ""
		}()})
		if err != nil {
			return "", err
		} // Fetch the latest page without retaining a private cursor.
		if feed.HasMore {
			head, err := a.Store.Feed(ctx, tracker.FeedOptions{Since: max(0, feed.LatestCursor-500), Limit: 500, ProjectID: pid, TaskID: func() string {
				if len(p) == 5 {
					return p[3]
				}
				return ""
			}()})
			if err != nil {
				return "", err
			}
			feed = head
		}
		start := max(0, len(feed.Notifications)-20)
		for _, n := range feed.Notifications[start:] {
			md += fmt.Sprintf("- #%g %s · %s · %s\n", tracker.Num(n, "number"), tracker.Text(n, "at"), tracker.Text(n, "actor"), tracker.Text(n, "message"))
		}
		return md, nil
	}
	return "", &tracker.Error{Status: 404, Message: "Unknown README"}
}

const apiGuide = `# Brainiac API

Base path: /api/v1. All mutation requests use Content-Type: application/json.
Read /api/v1/openapi.json for the machine-readable contract. MCP: /mcp.

## User input and service instructions

Use the host app's native user-question tool or chat as the primary input path.
Brainiac creates an additional browser alert; it does not open native Desktop
dialogs. Also ask in the host, preserving its input, permission and approval
flows. If that question tool blocks, post the mirror first and then ask natively.
Resolve the mirror from the actual host answer without demanding a second answer.
Brainiac alone is for hosts without input channels or an explicit user preference;
browser responses never substitute for native execution permissions.

POST /api/v1/input-requests with projectId, agent, title, message and optional
taskId/externalKey, or include inputRequest in a blocker work report. A stable
externalKey deduplicates retries per project/agent. Every open browser project,
task, knowledge and Markdown view shows unresolved questions; no desktop
permission is required for in-page popups. Desktop alerts are an optional browser
permission. A response PATCH uses status=resolved, response and expectedRevision;
cancel with status=cancelled. Dismissal is not a response or approval. Read
GET /api/v1/input-requests/{requestId}, list status=resolved/all, or consume
input.resolved in the normal numbered feed. Popups resume after reload/reconnect.

GET /api/v1/instructions?agent=AGENT&projectId=PROJECT returns pending service
guidance outside the numbered feed. Check once at session start; ordinary
work/activity reports include instructions, so avoid a separate polling loop.
Instructions have stable keys and independent revisions with per-agent receipts.
POST /instructions publishes key/title/message/kind and optional recipient,
projectId; PATCH /instructions/{key} requires expectedRevision, and active=false
retires it. These operations do not refresh project activity or create numbered
updates. Routing labels offer no privacy or authentication.

Brainiac fingerprints skill packages at startup and revises brainiac.skill.NAME
only when content changes. Use MCP get_skill(name), resource brainiac://skills/NAME,
GET /api/v1/skills/NAME or /skills/NAME/SKILL.md to re-read current guidance.
After handling an instruction, POST /api/v1/instructions/KEY/acknowledgements with
agent and exact revision (MCP acknowledge_instruction). On 409 reread before
acknowledging; new revisions become pending again. brainiac.* keys are reserved.
Instructions are subject to user/harness rules and never start model turns or
execute commands automatically.

## Projects and tasks

Prefer one meaningful batch over per-action calls: POST
/api/v1/projects/{projectId}/reports with agent, provider, workingOn, taskUpdates,
optional measured usage and optional message. Example:

    {"agent":"builder-project","provider":"OpenAI","workingOn":"API validation","taskUpdates":[{"taskId":"TASK","percent":40,"status":"active","detail":"Validation implemented"}]}

Reports commit atomically. taskUpdates contain existing taskId plus regular PATCH
fields; readme replacements still require expectedRevision. Explicit tasks:
[{"taskId":"TASK","workingOn":"Specific work"}] replace assignments; omitted
tasks retain them, adding/updating tasks mentioned in patches and removing newly
completed ones. active:false stops activity immediately. Agent activity expires
after exactly one hour; static ownership and task status alone do not count.
GET /api/v1/projects/{projectId}/agents returns agents and vendor active counts.
POST /api/v1/projects/{projectId}/agents/{agent}/activity is a quiet check-in.
Project summaries include agentActivity/vendors and task activeAgents.

The project lead includes workSummary in the same report: 1–2 plain sentences
about what is being built/fixed now and the next meaningful step (max 1000 UTF-8
bytes). It appears beside project completion and updates through SSE. Omit it
when unchanged. Other agents update their own workingOn with actual provider/model;
if no project summary exists the browser shows active agents' reported work.
Projects can also create/PATCH workSummary; server-managed workSummaryAt/By track
its timestamp/author. Reads do not update them. Known usage values and limits
produce compact used/limit bars in the per-agent table; unknown limits stay unknown.

Report work start, meaningful milestones/phase changes, blockers, handoff and
finish. During uninterrupted work refresh around every 10–15 minutes. Cache IDs
and cursors; do not report each command/edit/reasoning step or busy-poll. Batch
progress already refreshes presence and project activity, so skip extra heartbeats.

- GET/POST /api/v1/projects; GET/PATCH /api/v1/projects/{projectId}
- POST /api/v1/projects/{projectId}/heartbeat with {"actor":"orchestrator"}
- GET/POST /api/v1/projects/{projectId}/tasks
- GET/PATCH /api/v1/projects/{projectId}/tasks/{taskId}
- GET /api/v1/summary; GET /api/v1/projects/{projectId}/summary

Create a project with name and optional description, readme, externalKey,
lifecycle or activity. externalKey is unique and immutable. Filter projects by
externalKey, lifecycle, activity or q. Lifecycle: ongoing/completed/archived.
Presence: active/inactive. Successful scoped updates refresh activity; reads do
not. Completed projects retain completion; archived projects remain inactive.

Create tasks with name and optional parentId, status, percent, agent, detail,
next, verification, readme. IDs and parent associations are immutable. Status:
pending/partial/active/done/blocked. Percent: 0..100, independently estimated for
each task. Project progress averages top-level tasks. Empty progress is unknown.
Task lists and summaries accept agent/status filters. Task lists also accept q.
All writes accept actor. PATCH accepts expectedRevision; stale revisions return
409. Replacing existing readme always requires expectedRevision.

## Notifications and messages

GET /api/v1/notifications/since/0?limit=100&wait=0

Returns notifications oldest first, nextCursor, latestCursor, hasMore.
Continue using nextCursor, never latestCursor while hasMore is true. since is
exclusive. Filters: projectId, taskId, actor, recipient, kind, knowledgeSlug.
Recipient filters include broadcasts. limit: 1..500; wait: 0..30 seconds.
GET /api/v1/notifications also accepts since. Cursors survive service restarts;
a cursor ahead of this database returns 409 (reload state and restart at zero).

POST /api/v1/notifications with {"actor":"builder","projectId":"PROJECT",
"taskId":"TASK","recipient":"reviewer","message":"Ready for review"}.
Project, task and recipient are optional. These are public routing labels.
SSE: /events?since=0&projectId=PROJECT; reconnect with Last-Event-ID.

## Shared knowledge

- GET/POST /api/v1/knowledge
- GET/PATCH /api/v1/knowledge/{slug}
- GET /api/v1/knowledge/{slug}/revisions
- GET /api/v1/knowledge/{slug}/revisions/{revision}

Create with slug, title, body and optional summary, tags, sourceProjectId,
sourceTaskId, sourceAgent. Slugs are immutable lowercase letters/digits/hyphens.
Index responses omit bodies. Search title/summary/tags with q; exact tags with
tag. PATCH always requires expectedRevision. Previous revisions are retained.
Publish reusable observations and procedures likely to help future work;
exclude transient chatter. Knowledge changes appear in the global event feed.

## Compact usage reports

- GET /api/v1/agents
- GET/PUT /api/v1/agents/{agent}/usage

Report provider, model, sessionId, source, observedAt (RFC3339), tokenBudget,
tokensUsed, tokensRemaining, contextTokens, contextLimit, accountRemainingPercent,
resetsAt (RFC3339), actor and optional expectedRevision. Reports replace the
previous observation. Missing/null values are unknown. If budget and used are
known, remaining tokens are derived; context percentage requires both context
values. Reports become stale after USAGE_STALE_AFTER (default 5m). Account
allowance, session token budgets and context capacity are distinct measurements.
The server does not scrape providers or invent usage readings.

## README views and installation

GET /projects/{projectId}/README.md
GET /projects/{projectId}/tasks/{taskId}/README.md
GET /knowledge/README.md; GET /knowledge/{slug}/README.md

Explicit Accept: text/html renders a browser view; text/markdown or text/plain
returns source. Without a specific Accept preference, browsers receive HTML and
other clients Markdown. Context, live state and relevant API links are included.

GET /install.sh serves the interactive installer for installed Codex, Claude Code
and OpenCode harnesses. It asks which targets to install to and whether to add
MCP. For scripted use, pass --harness NAMES with --mcp or --no-mcp. Skill packages:
/skills/progress-tracker.tar.gz and /skills/live-progress-monitor.tar.gz.

## Request example

    curl --fail-with-body -X POST http://127.0.0.1:3011/api/v1/projects \
      -H 'Content-Type: application/json' \
      -d '{"name":"My project","externalKey":"/absolute/repository","actor":"orchestrator"}'

    curl --fail-with-body -X PATCH http://127.0.0.1:3011/api/v1/projects/PROJECT/tasks/TASK \
      -H 'Content-Type: application/json' \
      -d '{"percent":40,"status":"active","actor":"builder","next":"Run acceptance checks"}'

One instance owns the SQLite volume. Notifications are durable and unpruned.
No authentication or HTTPS in v1. IP_ALLOWLIST defaults to empty (no client IPs
allowed). Requests whose actual socket destination is in 127.0.0.0/24 bypass it.
Other destinations require a matching socket peer IP or IPv4/IPv6 CIDR in the
comma-separated list; denied requests receive 403. Host/forwarding headers cannot
enable the bypass. Invalid entries fail startup; an empty list is valid.
Compose host networking preserves peer IPs. Configure LISTEN_IP and LISTEN_PORT
separately; 0.0.0.0 is a bind address, not a client URL.
`
