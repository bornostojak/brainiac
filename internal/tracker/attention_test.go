package tracker

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestInputRequestsBatchRetriesAndAnswers(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	pid := Text(project(t, s, "Questions"), "id")
	other := Text(project(t, s, "Other"), "id")
	task, e := s.SaveTask(ctx, pid, "", Doc{"name": "Build"})
	task = must(t, task, e)
	foreign, e := s.SaveTask(ctx, other, "", Doc{"name": "Foreign"})
	foreign = must(t, foreign, e)
	report := Doc{"agent": "builder", "taskUpdates": []any{map[string]any{"taskId": Text(task, "id"), "status": "blocked"}}, "inputRequest": map[string]any{"title": "Choose target", "message": "Which output format?", "taskId": Text(task, "id"), "externalKey": "choose-format"}}
	d, e := s.ReportWork(ctx, pid, report)
	d = must(t, d, e)
	request := d["inputRequest"].(Doc)
	retry, e := s.ReportWork(ctx, pid, report)
	retry = must(t, retry, e)
	if Text(retry["inputRequest"].(Doc), "id") != Text(request, "id") {
		t.Fatal("retry duplicated question")
	}
	feed, e := s.Feed(ctx, FeedOptions{Limit: 100})
	if e != nil {
		t.Fatal(e)
	}
	questions := 0
	for _, n := range feed.Notifications {
		if Text(n, "kind") == "input.required" {
			questions++
			if Text(n, "inputRequestId") != Text(request, "id") {
				t.Fatal("missing durable question reference")
			}
		}
	}
	if questions != 1 {
		t.Fatal("duplicate pop-up events", questions)
	}
	report["inputRequest"] = map[string]any{"title": "Bad", "message": "Cross project", "taskId": Text(foreign, "id")}
	if _, e = s.ReportWork(ctx, pid, report); e == nil {
		t.Fatal("foreign task accepted")
	}
	after, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	if feed.LatestCursor != after.LatestCursor {
		t.Fatal("bad batch committed events")
	}
	if _, e = s.AnswerInput(ctx, Text(request, "id"), Doc{"status": "resolved", "response": "Markdown", "expectedRevision": float64(2)}); e == nil {
		t.Fatal("stale revision accepted")
	}
	answer, e := s.AnswerInput(ctx, Text(request, "id"), Doc{"status": "resolved", "response": "Markdown", "expectedRevision": float64(1), "actor": "user"})
	answer = must(t, answer, e)
	if Text(answer, "response") != "Markdown" {
		t.Fatal(answer)
	}
	if _, e = s.AnswerInput(ctx, Text(request, "id"), Doc{"status": "resolved", "response": "Overwrite", "expectedRevision": float64(2)}); e == nil {
		t.Fatal("closed question overwritten")
	}
	feed, _ = s.Feed(ctx, FeedOptions{Since: after.LatestCursor, Limit: 100, Recipient: "builder"})
	if len(feed.Notifications) != 1 || Text(feed.Notifications[0], "kind") != "input.resolved" {
		t.Fatal("answer did not reach requesting agent", feed)
	}
}

func TestInstructionsAreIndependentTargetedAndAcknowledged(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	pid := Text(project(t, s, "Control"), "id")
	before, _ := s.Get(ctx, "project", pid)
	head, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	for _, d := range []Doc{
		{"key": "global", "title": "Read guidance", "message": "Read current docs"},
		{"key": "targeted", "title": "Review", "message": "Check project guidance", "recipient": "builder", "projectId": pid},
	} {
		if _, e := s.SaveInstruction(ctx, "", d); e != nil {
			t.Fatal(e)
		}
	}
	items, e := s.Instructions(ctx, "builder", pid)
	if e != nil || len(items) != 2 {
		t.Fatal(items, e)
	}
	items, e = s.Instructions(ctx, "reviewer", pid)
	if e != nil || len(items) != 1 {
		t.Fatal("recipient filtering", items, e)
	}
	items, e = s.Instructions(ctx, "builder", "")
	if e != nil || len(items) != 1 {
		t.Fatal("project filtering", items, e)
	}
	if _, e = s.AcknowledgeInstruction(ctx, "global", Doc{"agent": "builder", "revision": float64(1)}); e != nil {
		t.Fatal(e)
	}
	items, e = s.Instructions(ctx, "builder", pid)
	if e != nil || len(items) != 1 {
		t.Fatal("receipt not applied", items, e)
	}
	if _, e = s.SaveInstruction(ctx, "global", Doc{"message": "Updated docs", "expectedRevision": float64(1)}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcknowledgeInstruction(ctx, "global", Doc{"agent": "builder", "revision": float64(1)}); e == nil {
		t.Fatal("stale acknowledgement accepted")
	}
	if _, e = s.SaveInstruction(ctx, "targeted", Doc{"active": false, "expectedRevision": float64(1)}); e != nil {
		t.Fatal(e)
	}
	items, e = s.Instructions(ctx, "builder", pid)
	if e != nil || len(items) != 1 || Num(items[0], "revision") != 2 {
		t.Fatal("updated instructions were not redelivered or retirement failed", items, e)
	}
	after, _ := s.Get(ctx, "project", pid)
	feed, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	if Num(after, "revision") != Num(before, "revision") || feed.LatestCursor != head.LatestCursor {
		t.Fatal("control instructions changed project activity or numbered feed")
	}
	batch, e := s.ReportWork(ctx, pid, Doc{"agent": "builder"})
	batch = must(t, batch, e)
	if len(batch["instructions"].([]Doc)) != 1 {
		t.Fatal("work report missing pending instructions")
	}
}

func TestInstructionAndInputPersistenceAndSkillChanges(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, e := Open(path, 90*time.Minute, 5*time.Minute, nil)
	if e != nil {
		t.Fatal(e)
	}
	pid := Text(project(t, s, "Persistent"), "id")
	r, e := s.RequestInput(ctx, Doc{"projectId": pid, "agent": "builder", "title": "Question", "message": "Choose an option"})
	r = must(t, r, e)
	key := "brainiac.skill.progress-tracker"
	if _, e = s.SyncSkillInstruction(ctx, "progress-tracker", "fingerprint-a"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcknowledgeInstruction(ctx, key, Doc{"agent": "builder", "revision": float64(1)}); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(path, 90*time.Minute, 5*time.Minute, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	d, e := s.SyncSkillInstruction(ctx, "progress-tracker", "fingerprint-a")
	d = must(t, d, e)
	if Num(d, "revision") != 1 {
		t.Fatal("unchanged startup produced another instruction")
	}
	items, e := s.Instructions(ctx, "builder", pid)
	if e != nil || len(items) != 0 {
		t.Fatal("acknowledgement lost on restart", items, e)
	}
	d, e = s.SyncSkillInstruction(ctx, "progress-tracker", "fingerprint-b")
	d = must(t, d, e)
	items, e = s.Instructions(ctx, "builder", pid)
	if e != nil || len(items) != 1 || Num(d, "revision") != 2 {
		t.Fatal("new package was not redelivered", items, e)
	}
	r, e = s.Get(ctx, "input", Text(r, "id"))
	r = must(t, r, e)
	if Text(r, "status") != "open" {
		t.Fatal("pending input lost on restart")
	}
}
