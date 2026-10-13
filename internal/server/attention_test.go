package server

import (
	"context"
	"encoding/json"
	"testing"

	"brainiac/internal/tracker"
)

func TestAttentionRoutesAndSkillSynchronization(t *testing.T) {
	a, s := testApp(t)
	ctx := context.Background()
	feed, _ := a.Store.Feed(ctx, tracker.FeedOptions{Limit: 100})
	if e := a.EnableDistribution(); e != nil {
		t.Fatal(e)
	}
	code, body := call(t, s, "GET", "/api/v1/instructions?agent=builder", "", nil)
	var result struct{ Instructions []tracker.Doc }
	json.Unmarshal(body, &result)
	if code != 200 || len(result.Instructions) != 2 {
		t.Fatal(code, string(body))
	}
	key := "brainiac.skill.progress-tracker"
	var notice tracker.Doc
	for _, item := range result.Instructions {
		if tracker.Text(item, "key") == key {
			notice = item
		}
	}
	code, body = call(t, s, "GET", "/api/v1/skills/progress-tracker", "", nil)
	var skill tracker.Doc
	json.Unmarshal(body, &skill)
	if code != 200 || tracker.Text(skill, "contentVersion") != tracker.Text(notice, "contentVersion") {
		t.Fatal("package fingerprint mismatch", code)
	}
	code, _ = call(t, s, "POST", "/api/v1/instructions/"+key+"/acknowledgements", `{"agent":"builder","revision":1}`, nil)
	if code != 201 {
		t.Fatal(code)
	}
	second := New(a.Store)
	if e := second.EnableDistribution(); e != nil {
		t.Fatal(e)
	}
	items, e := a.Store.Instructions(ctx, "builder", "")
	if e != nil || len(items) != 1 {
		t.Fatal("unchanged package generated a repeated notice", items, e)
	}
	after, _ := a.Store.Feed(ctx, tracker.FeedOptions{Limit: 100})
	if after.LatestCursor != feed.LatestCursor {
		t.Fatal("skill notices leaked into numbered notifications")
	}
	code, _ = call(t, s, "GET", "/api/v1/instructions", "", nil)
	if code != 400 {
		t.Fatal("missing agent accepted", code)
	}
	code, _ = call(t, s, "PATCH", "/api/v1/instructions/"+key, `{"message":"Override","expectedRevision":1}`, nil)
	if code != 400 {
		t.Fatal("service-managed instruction overwritten", code)
	}
	code, _ = call(t, s, "GET", "/api/v1/skills/missing", "", nil)
	if code != 404 {
		t.Fatal(code)
	}
	p, e := a.Store.CreateProject(ctx, tracker.Doc{"name": "Prompt"})
	if e != nil {
		t.Fatal(e)
	}
	data, _ := json.Marshal(tracker.Doc{"projectId": p["id"], "agent": "builder", "title": "Decision", "message": "Pick an option"})
	code, body = call(t, s, "POST", "/api/v1/input-requests", string(data), nil)
	if code != 201 {
		t.Fatal(code, string(body))
	}
	var question tracker.Doc
	json.Unmarshal(body, &question)
	code, _ = call(t, s, "PATCH", "/api/v1/input-requests/"+tracker.Text(question, "id"), `{"status":"resolved","response":"Yes"}`, nil)
	if code != 400 {
		t.Fatal("unguarded answer accepted", code)
	}
	code, _ = call(t, s, "GET", "/api/v1/input-requests?status=invalid", "", nil)
	if code != 400 {
		t.Fatal(code)
	}
}
