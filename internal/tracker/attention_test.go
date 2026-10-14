package tracker

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInputNoticesBatchRetriesAndClosure(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	pid := Text(project(t, s, "Questions"), "id")
	other := Text(project(t, s, "Other"), "id")
	task, e := s.SaveTask(ctx, pid, "", Doc{"name": "Build"})
	task = must(t, task, e)
	foreign, e := s.SaveTask(ctx, other, "", Doc{"name": "Foreign"})
	foreign = must(t, foreign, e)
	report := Doc{"agent": "builder", "taskUpdates": []any{map[string]any{"taskId": Text(task, "id"), "status": "blocked"}}, "inputRequest": map[string]any{"source": "Codex Desktop", "sourceUrl": "https://example.com/chat/1", "taskId": Text(task, "id"), "externalKey": "choose-format"}}
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
	report["inputRequest"] = map[string]any{"taskId": Text(foreign, "id")}
	if _, e = s.ReportWork(ctx, pid, report); e == nil {
		t.Fatal("foreign task accepted")
	}
	after, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	if feed.LatestCursor != after.LatestCursor {
		t.Fatal("bad batch committed events")
	}
	if _, e = s.CloseInput(ctx, Text(request, "id"), Doc{"status": "resolved", "expectedRevision": float64(2)}); e == nil {
		t.Fatal("stale revision accepted")
	}
	answer, e := s.CloseInput(ctx, Text(request, "id"), Doc{"status": "resolved", "expectedRevision": float64(1), "actor": "user"})
	answer = must(t, answer, e)
	if Text(answer, "status") != "resolved" || answer["response"] != nil {
		t.Fatal(answer)
	}
	if _, e = s.CloseInput(ctx, Text(request, "id"), Doc{"status": "resolved", "expectedRevision": float64(2)}); e == nil {
		t.Fatal("closed question overwritten")
	}
	feed, _ = s.Feed(ctx, FeedOptions{Since: after.LatestCursor, Limit: 100, Recipient: "builder"})
	if len(feed.Notifications) != 1 || Text(feed.Notifications[0], "kind") != "input.resolved" {
		t.Fatal("closure did not reach requesting agent", feed)
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
	r, e := s.RequestInput(ctx, Doc{"projectId": pid, "agent": "builder", "source": "Claude Code"})
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

// Upgrades must not re-publish old question/answer bodies in REST, MCP, SSE or
// generated READMEs, while preserving stored history and its numbered cursors.
func TestAttentionMetadataAndLegacyProjection(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	pid := Text(project(t, s, "Attention"), "id")
	payload := Doc{"projectId": pid, "agent": "builder", "title": "private question", "message": "private question body", "externalKey": "legacy-client", "source": "Codex Desktop"}
	notice, e := s.RequestInput(ctx, payload)
	notice = must(t, notice, e)
	check := func(d Doc) {
		t.Helper()
		for _, key := range []string{"title", "message", "response"} {
			if _, ok := d[key]; ok {
				t.Fatalf("notice leaked %s: %v", key, d)
			}
		}
	}
	check(notice)
	raw, e := get(ctx, s.db, "input", Text(notice, "id"))
	check(must(t, raw, e))
	if _, e = s.CloseInput(ctx, Text(notice, "id"), Doc{"status": "resolved", "expectedRevision": float64(1), "response": "private answer"}); e == nil {
		t.Fatal("accepted answer content")
	}
	for _, link := range []string{"javascript:alert(1)", "data:text/plain,question", "/relative", "https://user:pass@example.com/chat"} {
		badPayload := clone(payload)
		badPayload["sourceUrl"] = link
		if _, e = s.RequestInput(ctx, badPayload); e == nil {
			t.Fatal("unsafe source URL accepted", link)
		}
	}
	// Simulate a pre-upgrade record and event, retaining the original revision.
	legacy := clone(notice)
	legacy["title"] = "old private question"
	legacy["message"] = "old private body"
	legacy["response"] = "old private answer"
	var number int64
	_, e = s.write(ctx, func(tx *sql.Tx) (Doc, error) {
		if e := put(ctx, tx, "input", Text(legacy, "id"), pid, legacy); e != nil {
			return nil, e
		}
		var err error
		number, err = s.event(ctx, tx, "input.required", "builder", pid, "", "", "old private question: old private body", "builder")
		return legacy, err
	})
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.Get(ctx, "input", Text(notice, "id"))
	check(must(t, d, e))
	items, e := s.List(ctx, "input")
	if e != nil {
		t.Fatal(e)
	}
	for _, d := range items {
		check(d)
	}
	retry, e := s.RequestInput(ctx, payload)
	check(must(t, retry, e))
	batch, e := s.ReportWork(ctx, pid, Doc{"agent": "builder", "inputRequest": map[string]any{"externalKey": "legacy-client"}})
	batch = must(t, batch, e)
	check(batch["inputRequest"].(Doc))
	feed, e := s.Feed(ctx, FeedOptions{Since: number - 1, Limit: 100, Kind: "input.required"})
	if e != nil || len(feed.Notifications) != 1 || Num(feed.Notifications[0], "number") != float64(number) {
		t.Fatal(feed, e)
	}
	encoded, _ := json.Marshal(feed)
	if strings.Contains(string(encoded), "private") {
		t.Fatal("historical feed leaked body", string(encoded))
	}
	raw, e = get(ctx, s.db, "input", Text(notice, "id"))
	raw = must(t, raw, e)
	if Text(raw, "response") != "old private answer" {
		t.Fatal("read projection changed stored history")
	}
	closed, e := s.CloseInput(ctx, Text(notice, "id"), Doc{"status": "resolved", "expectedRevision": float64(1), "actor": "builder"})
	check(must(t, closed, e))
}
