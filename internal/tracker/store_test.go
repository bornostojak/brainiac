package tracker

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"), 90*time.Minute, 5*time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func must(t *testing.T, d Doc, err error) Doc {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func project(t *testing.T, s *Store, name string) Doc {
	t.Helper()
	d, e := s.CreateProject(context.Background(), Doc{"name": name})
	return must(t, d, e)
}
func TestTaskIsolationManualProgressAndRevision(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := project(t, s, "Alpha")
	q := project(t, s, "Beta")
	root, e := s.SaveTask(ctx, Text(p, "id"), "", Doc{"name": "Build", "percent": float64(70)})
	root = must(t, root, e)
	child, e := s.SaveTask(ctx, Text(p, "id"), "", Doc{"name": "Verify", "parentId": Text(root, "id"), "percent": float64(20)})
	child = must(t, child, e)
	if _, e = s.SaveTask(ctx, Text(q, "id"), "", Doc{"name": "Wrong", "parentId": Text(root, "id")}); e == nil {
		t.Fatal("cross-project parent accepted")
	}
	if _, e = s.SaveTask(ctx, Text(q, "id"), Text(root, "id"), Doc{"percent": float64(10)}); e == nil {
		t.Fatal("cross-project update accepted")
	}
	if _, e = s.SaveTask(ctx, Text(p, "id"), Text(child, "id"), Doc{"parentId": ""}); e == nil {
		t.Fatal("parent mutation accepted")
	}
	summary, e := s.Summary(ctx, Text(p, "id"), "", "")
	summary = must(t, summary, e)
	if Num(summary["projects"].([]Doc)[0], "percent") != 70 {
		t.Fatal("child changed parent/project estimate")
	}
	if _, e = s.SaveTask(ctx, Text(p, "id"), Text(root, "id"), Doc{"readme": "Context"}); e == nil {
		t.Fatal("unguarded Markdown replacement accepted")
	}
	updated, e := s.SaveTask(ctx, Text(p, "id"), Text(root, "id"), Doc{"readme": "Context", "expectedRevision": float64(1)})
	must(t, updated, e)
	if _, e = s.SaveTask(ctx, Text(p, "id"), Text(root, "id"), Doc{"percent": float64(100), "expectedRevision": float64(1)}); e == nil {
		t.Fatal("stale write accepted")
	}
}
func TestActivityBoundaryAndRestart(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "db")
	s, e := Open(path, 90*time.Minute, 5*time.Minute, func() time.Time { return now })
	if e != nil {
		t.Fatal(e)
	}
	p := project(t, s, "Expiry")
	id := Text(p, "id")
	now = now.Add(89 * time.Minute)
	if e = s.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	p, e = s.Get(ctx, "project", id)
	p = must(t, p, e)
	if Text(p, "activity") != "active" {
		t.Fatal("expired too soon")
	}
	now = now.Add(time.Minute)
	if e = s.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	p, e = s.Get(ctx, "project", id)
	p = must(t, p, e)
	if Text(p, "activity") != "inactive" {
		t.Fatal("boundary did not expire")
	}
	f, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	if len(f.Notifications) != 2 {
		t.Fatalf("unexpected expiry events: %v", f)
	}
	s.Sweep(ctx)
	f, _ = s.Feed(ctx, FeedOptions{Limit: 100})
	if len(f.Notifications) != 2 {
		t.Fatal("duplicate inactivity event")
	}
	p, e = s.UpdateProject(ctx, id, Doc{"lifecycle": "completed", "activity": "active"})
	must(t, p, e)
	now = now.Add(2 * time.Hour)
	s.Close()
	s, e = Open(path, 90*time.Minute, 5*time.Minute, func() time.Time { return now })
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p, e = s.Get(ctx, "project", id)
	p = must(t, p, e)
	if Text(p, "lifecycle") != "completed" || Text(p, "activity") != "inactive" {
		t.Fatal("restart lost completion or expiry")
	}
	p, e = s.Heartbeat(ctx, id, Doc{})
	p = must(t, p, e)
	if Text(p, "lifecycle") != "completed" {
		t.Fatal("heartbeat reopened project")
	}
}
func TestConcurrentNotificationsAndFilteredPaging(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := project(t, s, "Feed")
	q := project(t, s, "Other")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.Message(ctx, Doc{"projectId": Text(p, "id"), "message": "progress"}); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	s.Message(ctx, Doc{"projectId": Text(q, "id"), "message": "unrelated"})
	cursor := int64(0)
	count := 0
	for {
		f, e := s.Feed(ctx, FeedOptions{Since: cursor, Limit: 3, ProjectID: Text(p, "id")})
		if e != nil {
			t.Fatal(e)
		}
		for _, d := range f.Notifications {
			n := int64(Num(d, "number"))
			if n <= cursor {
				t.Fatal("non-increasing cursor")
			}
			cursor = n
			count++
		}
		cursor = f.NextCursor
		if !f.HasMore {
			if cursor != f.LatestCursor {
				t.Fatal("filtered scan did not reach head")
			}
			break
		}
	}
	if count != 21 {
		t.Fatalf("missed notifications: %d", count)
	}
	f, e := s.Feed(ctx, FeedOptions{Since: cursor, Limit: 3})
	if e != nil || len(f.Notifications) != 0 {
		t.Fatal("since is not exclusive")
	}
}
func TestKnowledgeHistoryAndAtomicRejection(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	d, e := s.SaveKnowledge(ctx, "", Doc{"slug": "build-tips", "title": "Build tips", "body": "Original", "tags": []any{"go"}})
	must(t, d, e)
	if _, e = s.SaveKnowledge(ctx, "build-tips", Doc{"body": "Unsafe overwrite"}); e == nil {
		t.Fatal("missing revision accepted")
	}
	d, e = s.SaveKnowledge(ctx, "build-tips", Doc{"body": "Updated", "expectedRevision": float64(1)})
	must(t, d, e)
	old, e := s.KnowledgeHistory(ctx, "build-tips", 1)
	if e != nil || Text(old.(Doc), "body") != "Original" {
		t.Fatal("lost revision")
	}
	history, e := s.KnowledgeHistory(ctx, "build-tips", 0)
	if e != nil || len(history.([]Doc)) != 2 || history.([]Doc)[0]["body"] != nil {
		t.Fatal("history index includes bodies or lost entries")
	}
	f, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	if len(f.Notifications) != 2 {
		t.Fatal("rejected write emitted notification")
	}
}
func TestUsageUnknownFreshnessAndSessions(t *testing.T) {
	s := testStore(t)
	now := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	ctx := context.Background()
	u, e := s.SaveUsage(ctx, "builder", Doc{"tokenBudget": float64(20000), "tokensUsed": float64(4000), "contextTokens": float64(30000), "contextLimit": float64(100000), "model": "test"})
	u = must(t, u, e)
	if Num(u, "tokensRemaining") != 16000 || Num(u, "contextPercent") != 30 || u["stale"] != false {
		t.Fatal(u)
	}
	now = now.Add(5 * time.Minute)
	u, e = s.Get(ctx, "agent", "builder")
	u = must(t, u, e)
	if u["stale"] != true {
		t.Fatal("stale observation labelled fresh")
	}
	u, e = s.SaveUsage(ctx, "builder", Doc{"sessionId": "new"})
	u = must(t, u, e)
	if u["tokensRemaining"] != nil || u["contextPercent"] != nil {
		t.Fatal("old metrics leaked into new observation")
	}
}
func TestLongPollWakeAndCancel(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := project(t, s, "Wait")
	head, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	done := make(chan Feed, 1)
	go func() {
		f, e := s.WaitFeed(ctx, FeedOptions{Since: head.LatestCursor, Limit: 100}, time.Second)
		if e != nil {
			t.Error(e)
		}
		done <- f
	}()
	s.Message(ctx, Doc{"projectId": Text(p, "id"), "message": "wake"})
	select {
	case f := <-done:
		if len(f.Notifications) != 1 {
			t.Fatal(f)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("missed wakeup")
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := s.WaitFeed(cancelCtx, FeedOptions{Since: head.LatestCursor + 1, Limit: 100}, time.Second); e == nil {
		t.Fatal("ignored cancellation")
	}
}
