package tracker

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

const AgentActivityTimeout = time.Hour

func canonicalProvider(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "openai", "chatgpt", "codex":
		return "OpenAI"
	case "anthropic", "claude", "claude code":
		return "Anthropic"
	case "z.ai", "zai", "zhipu", "zhipuai", "glm":
		return "Z.ai"
	case "google", "gemini":
		return "Google"
	case "":
		return "Unknown"
	default:
		return value
	}
}

func (s *Store) decorateActivity(d Doc) {
	active, _ := d["active"].(bool)
	d["active"] = active && s.Now().Before(parse(Text(d, "activeUntil")))
}

// ReportWork atomically batches task progress, agent presence and optional usage.
// Presence is per project/agent; one agent on several tasks is counted only once.
func (s *Store) ReportWork(ctx context.Context, pid string, payload Doc) (Doc, error) {
	if err := fields(payload, "agent provider model active workingOn tasks taskUpdates usage message inputRequest"); err != nil {
		return nil, err
	}
	if err := texts(payload, "agent", map[string]int{"agent": 128, "provider": 128, "model": 128, "workingOn": 2000, "message": 8000}); err != nil {
		return nil, err
	}
	if strings.Contains(Text(payload, "agent"), "/") {
		return nil, bad("agent cannot contain slashes")
	}
	if active, ok := payload["active"]; ok {
		if _, valid := active.(bool); !valid {
			return nil, bad("active must be boolean")
		}
	}
	updates := []any{}
	if value, ok := payload["taskUpdates"]; ok {
		var valid bool
		updates, valid = value.([]any)
		if !valid || len(updates) > 100 {
			return nil, bad("taskUpdates must be an array of at most 100 patches")
		}
	}
	return s.write(ctx, func(tx *sql.Tx) (Doc, error) {
		project, err := get(ctx, tx, "project", pid)
		if err != nil {
			return nil, err
		}
		agent := Text(payload, "agent")
		actor := Text(payload, "actor")
		if actor == "" {
			actor = agent
		}
		result := Doc{"taskUpdates": []Doc{}}
		if value, ok := payload["usage"]; ok {
			usage, valid := value.(map[string]any)
			if !valid {
				return nil, bad("usage must be an object")
			}
			input := clone(Doc(usage))
			input["actor"] = actor
			d, err := s.saveUsageTx(ctx, tx, agent, input)
			if err != nil {
				return nil, err
			}
			result["usageRevision"] = d["revision"]
		}
		seen := map[string]bool{}
		for _, value := range updates {
			patch, valid := value.(map[string]any)
			if !valid {
				return nil, bad("taskUpdates must contain objects")
			}
			input := clone(Doc(patch))
			tid := Text(input, "taskId")
			if tid == "" || seen[tid] {
				return nil, bad("taskUpdates require distinct existing taskId values")
			}
			seen[tid] = true
			delete(input, "taskId")
			input["actor"] = actor
			d, err := s.saveTaskTx(ctx, tx, pid, tid, input)
			if err != nil {
				return nil, err
			}
			result["taskUpdates"] = append(result["taskUpdates"].([]Doc), Doc{"id": tid, "revision": d["revision"], "status": d["status"], "percent": d["percent"]})
		}
		id := pid + "/" + agent
		d, err := get(ctx, tx, "presence", id)
		if err != nil {
			if e, ok := err.(*Error); !ok || e.Status != 404 {
				return nil, err
			}
			d = Doc{"agent": agent, "projectId": pid, "provider": "Unknown", "model": "", "workingOn": "", "tasks": []any{}, "revision": float64(0)}
		}
		if err = revision(d, payload, false); err != nil {
			return nil, err
		}
		if usage, e := get(ctx, tx, "agent", agent); e == nil {
			if Text(d, "provider") == "Unknown" && Text(usage, "provider") != "" {
				d["provider"] = canonicalProvider(Text(usage, "provider"))
			}
			if Text(d, "model") == "" {
				d["model"] = Text(usage, "model")
			}
		} else if known, ok := e.(*Error); !ok || known.Status != 404 {
			return nil, e
		}
		for _, key := range []string{"provider", "model", "workingOn"} {
			if value, ok := payload[key]; ok {
				d[key] = value
			}
		}
		d["provider"] = canonicalProvider(Text(d, "provider"))
		if value, ok := payload["tasks"]; ok {
			items, valid := value.([]any)
			if !valid || len(items) > 100 {
				return nil, bad("tasks must contain at most 100 activity assignments")
			}
			assignments := []any{}
			seen = map[string]bool{}
			for _, value := range items {
				item, valid := value.(map[string]any)
				if !valid {
					return nil, bad("tasks must contain taskId/workingOn objects")
				}
				assignment := Doc(item)
				if err = fields(assignment, "taskId workingOn"); err != nil {
					return nil, err
				}
				if err = texts(assignment, "taskId", map[string]int{"taskId": 64, "workingOn": 2000}); err != nil {
					return nil, err
				}
				tid := Text(assignment, "taskId")
				if seen[tid] {
					return nil, bad("Duplicate activity taskId")
				}
				seen[tid] = true
				task, e := get(ctx, tx, "task", tid)
				if e != nil {
					return nil, e
				}
				if Text(task, "projectId") != pid {
					return nil, bad("Activity task belongs to another project")
				}
				assignments = append(assignments, Doc{"taskId": tid, "workingOn": Text(assignment, "workingOn")})
			}
			d["tasks"] = assignments
		} else if len(updates) > 0 {
			// Patches add/refresh assignments without dropping other ongoing work.
			byID := map[string]Doc{}
			for _, value := range d["tasks"].([]any) {
				item := Doc(value.(map[string]any))
				byID[Text(item, "taskId")] = item
			}
			assignments := []any{}
			for _, value := range updates {
				patch := Doc(value.(map[string]any))
				task, e := get(ctx, tx, "task", Text(patch, "taskId"))
				if e != nil {
					return nil, e
				}
				if Text(task, "status") != "done" {
					item := byID[Text(task, "id")]
					if item == nil {
						item = Doc{"taskId": Text(task, "id"), "workingOn": ""}
					}
					if value, ok := patch["detail"]; ok {
						item["workingOn"] = value
					}
					byID[Text(task, "id")] = item
				} else {
					delete(byID, Text(task, "id"))
				}
			}
			ids := []string{}
			for id := range byID {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				assignments = append(assignments, byID[id])
			}
			d["tasks"] = assignments
		}
		d["active"] = true
		if value, ok := payload["active"]; ok {
			d["active"] = value
		}
		d["lastActivityAt"] = stamp(s.Now())
		d["activeUntil"] = stamp(s.Now().Add(AgentActivityTimeout))
		d["revision"] = Num(d, "revision") + 1
		if err = put(ctx, tx, "presence", id, pid, d); err != nil {
			return nil, err
		}
		// Task patches may have already touched the project inside this transaction.
		project, err = get(ctx, tx, "project", pid)
		if err != nil {
			return nil, err
		}
		s.touch(project)
		if err = put(ctx, tx, "project", pid, "", project); err != nil {
			return nil, err
		}
		message := agent + " · " + Text(d, "workingOn")
		if d["active"] == false {
			message = agent + " stopped work"
		}
		if _, err = s.event(ctx, tx, "agent.activity", actor, pid, "", "", message, ""); err != nil {
			return nil, err
		}
		if Text(payload, "message") != "" {
			if _, err = s.event(ctx, tx, "message", actor, pid, "", "", Text(payload, "message"), ""); err != nil {
				return nil, err
			}
		}
		if value, ok := payload["inputRequest"]; ok {
			input, valid := value.(map[string]any)
			if !valid {
				return nil, bad("inputRequest must be an object")
			}
			request := clone(Doc(input))
			if err = fields(request, "title message taskId externalKey"); err != nil {
				return nil, err
			}
			request["projectId"], request["agent"], request["actor"] = pid, agent, actor
			d, err := s.requestInputTx(ctx, tx, request)
			if err != nil {
				return nil, err
			}
			result["inputRequest"] = d
		}
		instructions, err := pendingInstructions(ctx, tx, agent, pid)
		if err != nil {
			return nil, err
		}
		result["instructions"] = instructions
		var cursor int64
		if err = tx.QueryRowContext(ctx, "SELECT coalesce(max(number),0) FROM notifications").Scan(&cursor); err != nil {
			return nil, err
		}
		result["activity"] = d
		result["cursor"] = cursor
		return result, nil
	})
}

func (s *Store) ProjectAgents(ctx context.Context, pid string) (Doc, error) {
	if _, err := s.Get(ctx, "project", pid); err != nil {
		return nil, err
	}
	items, err := s.List(ctx, "presence")
	if err != nil {
		return nil, err
	}
	items = Filter(items, map[string]string{"projectId": pid})
	return Doc{"agents": items, "vendors": vendorCounts(items)}, nil
}
func vendorCounts(items []Doc) []Doc {
	groups := map[string]int{}
	for _, a := range items {
		provider := canonicalProvider(Text(a, "provider"))
		if _, ok := groups[provider]; !ok {
			groups[provider] = 0
		}
		if a["active"] == true {
			groups[provider]++
		}
	}
	names := []string{}
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	out := []Doc{}
	for _, name := range names {
		out = append(out, Doc{"provider": name, "activeCount": groups[name]})
	}
	return out
}
func attachActivity(tasks, projects, items []Doc) {
	for _, t := range tasks {
		t["activeAgents"] = []Doc{}
		if Text(t, "status") == "done" {
			continue
		}
		for _, a := range items {
			if a["active"] != true || Text(a, "projectId") != Text(t, "projectId") {
				continue
			}
			for _, value := range a["tasks"].([]any) {
				assignment := Doc(value.(map[string]any))
				if Text(assignment, "taskId") == Text(t, "id") {
					view := clone(a)
					delete(view, "tasks")
					view["workingOn"] = Text(assignment, "workingOn")
					if Text(view, "workingOn") == "" {
						view["workingOn"] = Text(a, "workingOn")
					}
					t["activeAgents"] = append(t["activeAgents"].([]Doc), view)
				}
			}
		}
	}
	for _, p := range projects {
		presence := Filter(items, map[string]string{"projectId": Text(p, "id")})
		p["agentActivity"] = presence
		p["vendors"] = vendorCounts(presence)
	}
}

func (s *Store) expireAgentsTx(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT id,document FROM entities WHERE kind='presence' AND json_extract(document,'$.active')=1")
	if err != nil {
		return err
	}
	pending := map[string]Doc{}
	for rows.Next() {
		var id string
		var data []byte
		if err = rows.Scan(&id, &data); err != nil {
			rows.Close()
			return err
		}
		var d Doc
		if err = json.Unmarshal(data, &d); err != nil {
			rows.Close()
			return err
		}
		if !s.Now().Before(parse(Text(d, "activeUntil"))) {
			pending[id] = d
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for id, d := range pending {
		d["active"] = false
		d["revision"] = Num(d, "revision") + 1
		if err = put(ctx, tx, "presence", id, Text(d, "projectId"), d); err != nil {
			return err
		}
		if _, err = s.event(ctx, tx, "agent.inactive", "system", Text(d, "projectId"), "", "", Text(d, "agent")+" inactive after one hour without a report", ""); err != nil {
			return err
		}
	}
	return nil
}
