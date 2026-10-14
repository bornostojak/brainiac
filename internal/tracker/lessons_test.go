package tracker

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLessonsIsolationRevisionsAndIndex(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	pid := Text(project(t, s, "Lessons"), "id")
	other := Text(project(t, s, "Other"), "id")
	task, err := s.SaveTask(ctx, pid, "", Doc{"name": "Build"})
	task = must(t, task, err)
	foreign, err := s.SaveTask(ctx, other, "", Doc{"name": "Foreign"})
	foreign = must(t, foreign, err)
	payload := Doc{"title": "Check measured units", "summary": "Unit conversion must precede comparison", "kind": "miscalculation", "attempt": "Compared values in different units", "outcome": "Incorrect estimate", "correction": "Normalize units before comparing", "evidence": "Verified by the conversion test", "tags": []any{"units"}, "taskId": task["id"], "agent": "builder", "externalKey": "incident-1"}
	d, err := s.SaveLesson(ctx, pid, "", payload)
	d = must(t, d, err)
	id := Text(d, "id")
	if Text(d, "confidence") != "observed" || Num(d, "revision") != 1 {
		t.Fatal(d)
	}
	before, _ := s.Get(ctx, "project", pid)
	if _, err = s.Lesson(ctx, other, id); err == nil {
		t.Fatal("cross-project read")
	}
	if _, err = s.SaveLesson(ctx, other, id, Doc{"correction": "Wrong", "expectedRevision": float64(1)}); err == nil {
		t.Fatal("cross-project update")
	}
	if _, err = s.LessonHistory(ctx, other, id, 1); err == nil {
		t.Fatal("cross-project history")
	}
	badRef := clone(payload)
	badRef["taskId"] = foreign["id"]
	badRef["externalKey"] = "foreign"
	if _, err = s.SaveLesson(ctx, pid, "", badRef); err == nil {
		t.Fatal("foreign task accepted")
	}
	items, err := s.Lessons(ctx, pid, map[string]string{"q": "CONVERSION", "tag": "units", "kind": "miscalculation", "taskId": Text(task, "id"), "agent": "builder"})
	if err != nil || len(items) != 1 || items[0]["correction"] != nil || items[0]["attempt"] != nil {
		t.Fatal(items, err)
	}
	if items, err = s.Lessons(ctx, pid, map[string]string{"tag": "unit"}); err != nil || len(items) != 0 {
		t.Fatal("tag was not exact", items, err)
	}
	if _, err = s.Lessons(ctx, pid, map[string]string{"status": "invalid"}); err == nil {
		t.Fatal("invalid filter accepted")
	}
	after, _ := s.Get(ctx, "project", pid)
	if after["revision"] != before["revision"] {
		t.Fatal("reads/failed writes touched activity")
	}
	for _, patch := range []Doc{{"correction": "Overwrite"}, {"correction": "Overwrite", "expectedRevision": float64(2)}, {"projectId": other, "expectedRevision": float64(1)}, {"externalKey": "new", "expectedRevision": float64(1)}} {
		if _, err = s.SaveLesson(ctx, pid, id, patch); err == nil {
			t.Fatal("unguarded/immutable update accepted", patch)
		}
	}
	d, err = s.SaveLesson(ctx, pid, id, Doc{"correction": "Use verified conversion factors", "status": "superseded", "confidence": "confirmed", "expectedRevision": float64(1), "actor": "reviewer"})
	d = must(t, d, err)
	if Num(d, "revision") != 2 || Text(d, "updatedBy") != "reviewer" {
		t.Fatal(d)
	}
	old, err := s.LessonHistory(ctx, pid, id, 1)
	if err != nil || Text(old.(Doc), "correction") != Text(payload, "correction") {
		t.Fatal(old, err)
	}
	history, err := s.LessonHistory(ctx, pid, id, 0)
	if err != nil || len(history.([]Doc)) != 2 || history.([]Doc)[0]["evidence"] != nil {
		t.Fatal(history, err)
	}
	if _, err = s.LessonHistory(ctx, pid, id, 3); err == nil {
		t.Fatal("nonexistent revision")
	}
	if items, err = s.Lessons(ctx, pid, map[string]string{"status": "active"}); err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
	feed, err := s.Feed(ctx, FeedOptions{Limit: 100, LessonID: id})
	if err != nil || len(feed.Notifications) != 2 || Text(feed.Notifications[0], "kind") != "lesson.created" || Text(feed.Notifications[1], "kind") != "lesson.updated" {
		t.Fatal(feed, err)
	}
}

func TestLessonBatchRollbackAndRetry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	pid := Text(project(t, s, "Batch"), "id")
	task, err := s.SaveTask(ctx, pid, "", Doc{"name": "Build"})
	task = must(t, task, err)
	before, _ := s.Get(ctx, "project", pid)
	feed, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	entry := map[string]any{"title": "Wrong assumption", "correction": "Check the documented contract", "externalKey": "incident"}
	report := Doc{"agent": "builder", "taskUpdates": []any{map[string]any{"taskId": task["id"], "percent": 30}}, "lessons": []any{entry, map[string]any{"title": "Missing task", "correction": "Fail the entire batch", "taskId": "unknown"}}}
	if _, err = s.ReportWork(ctx, pid, report); err == nil {
		t.Fatal("invalid batch committed")
	}
	items, _ := s.Lessons(ctx, pid, nil)
	after, _ := s.Get(ctx, "project", pid)
	head, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	unchanged, _ := s.Task(ctx, pid, Text(task, "id"))
	if len(items) != 0 || before["revision"] != after["revision"] || head.LatestCursor != feed.LatestCursor || Num(unchanged, "percent") != 0 {
		t.Fatal("batch partially committed")
	}
	report["lessons"] = []any{entry}
	result, err := s.ReportWork(ctx, pid, report)
	result = must(t, result, err)
	index := result["lessons"].([]Doc)
	if len(index) != 1 || index[0]["correction"] != nil || Text(index[0], "agent") != "builder" {
		t.Fatal(index)
	}
	result, err = s.ReportWork(ctx, pid, report)
	result = must(t, result, err)
	if Text(result["lessons"].([]Doc)[0], "id") != Text(index[0], "id") {
		t.Fatal("retry duplicated lesson")
	}
	events, err := s.Feed(ctx, FeedOptions{Since: feed.LatestCursor, Limit: 100, Kind: "lesson.created"})
	if err != nil || len(events.Notifications) != 1 {
		t.Fatal(events, err)
	}
}

func TestLessonValidationConcurrencyAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lessons.db")
	s, err := Open(path, 90*time.Minute, 5*time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	pid := Text(project(t, s, "Persistent"), "id")
	for _, payload := range []Doc{{"title": "Missing correction"}, {"title": "Huge", "correction": strings.Repeat("x", 32769)}, {"title": "Bad", "correction": "Fix", "kind": "punishment"}, {"title": "Bad", "correction": "Fix", "confidence": "guess"}, {"title": "Bad", "correction": "Fix", "tags": []any{42.0}}} {
		if _, err = s.SaveLesson(ctx, pid, "", payload); err == nil {
			t.Fatal("invalid lesson accepted", payload["title"])
		}
	}
	var wg sync.WaitGroup
	ids := make(chan string, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := s.SaveLesson(ctx, pid, "", Doc{"title": "Concurrent retry", "correction": "Use stable incident keys", "externalKey": "once"})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- Text(d, "id")
		}()
	}
	wg.Wait()
	close(ids)
	id := ""
	for value := range ids {
		if id != "" && value != id {
			t.Fatal("concurrent retries created duplicates")
		}
		id = value
	}
	if id == "" {
		t.Fatal("no lesson created")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, 90*time.Minute, 5*time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Lesson(ctx, pid, id)
	if err != nil || Text(d, "correction") != "Use stable incident keys" {
		t.Fatal(d, err)
	}
	history, err := s.LessonHistory(ctx, pid, id, 0)
	if err != nil || len(history.([]Doc)) != 1 {
		t.Fatal(history, err)
	}
	feed, err := s.Feed(ctx, FeedOptions{Limit: 100, LessonID: id})
	if err != nil || len(feed.Notifications) != 1 {
		t.Fatal(feed, err)
	}
}
